/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2eopenshift

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
)

var lowLoad = numPrompts <= 2000 && requestRate <= 8

// Load generation configuration constants
const (
	numLoadWorkers     = 10    // Number of parallel load generation workers
	requestsPerWorker  = 500   // Requests each worker sends
	batchSize          = 50    // Concurrent requests per batch
	curlTimeoutSeconds = 120   // Timeout for each curl request
	maxTokens          = 150   // Max tokens for completion requests
	batchSleepDuration = "0.1" // Sleep duration between batches to control rate
)

// modelTestConfig holds configuration for testing a specific model
type modelTestConfig struct {
	name       string // Human-readable name for logging
	namespace  string // Kubernetes namespace
	deployment string // Deployment name
}

// getModelsToTest returns the list of models to test based on configuration
func getModelsToTest() []modelTestConfig {
	models := []modelTestConfig{
		{
			name:       "Model A1",
			namespace:  llmDNamespace,
			deployment: deployment,
		},
	}

	// Add Model A2 if multi-model mode is enabled
	if multiModelMode && deploymentA2 != "" {
		models = append(models, modelTestConfig{
			name:       "Model A2",
			namespace:  llmDNamespace,
			deployment: deploymentA2,
		})
	}

	// Add Model B if secondary namespace is configured
	if multiModelMode && llmDNamespaceB != "" {
		models = append(models, modelTestConfig{
			name:       "Model B",
			namespace:  llmDNamespaceB,
			deployment: deployment, // Model B uses the same deployment name as A1
		})
	}

	return models
}

var _ = Describe("ShareGPT Scale-Up Test", Ordered, func() {
	var ctx context.Context

	BeforeAll(func() {
		ctx = context.Background()
	})

	// Test each model sequentially
	models := getModelsToTest()
	for _, model := range models {
		// Capture model in closure
		model := model

		Context(fmt.Sprintf("Testing %s in namespace %s", model.name, model.namespace), Ordered, func() {
			var (
				jobBaseName          string
				initialReplicas      int32
				initialOptimized     int32
				hpaMinReplicas       int32
				hpaName              string
				vaName               string
				vllmServiceName      string
				scaledReplicas       int32
				scaledOptimized      int32
				jobCompletionTimeout = 10 * time.Minute
			)

			BeforeAll(func() {
				jobBaseName = fmt.Sprintf("load-gen-%s", model.name)

				_, _ = fmt.Fprintf(GinkgoWriter, "\n========================================\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "Starting test for %s\n", model.name)
				_, _ = fmt.Fprintf(GinkgoWriter, "  Namespace: %s\n", model.namespace)
				_, _ = fmt.Fprintf(GinkgoWriter, "  Deployment: %s\n", model.deployment)
				_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")

				By(fmt.Sprintf("recording initial state of %s deployment", model.name))
				deploy, err := k8sClient.AppsV1().Deployments(model.namespace).Get(ctx, model.deployment, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Should be able to get vLLM deployment")
				initialReplicas = deploy.Status.ReadyReplicas
				_, _ = fmt.Fprintf(GinkgoWriter, "Initial ready replicas: %d\n", initialReplicas)

				By("recording initial VariantAutoscaling state")
				vaList := &v1alpha1.VariantAutoscalingList{}
				err = crClient.List(ctx, vaList, client.InNamespace(model.namespace), client.MatchingLabels{
					"app.kubernetes.io/name": "workload-variant-autoscaler",
				})
				Expect(err).NotTo(HaveOccurred(), "Should be able to list VariantAutoscalings")
				Expect(vaList.Items).NotTo(BeEmpty(), "At least one WVA VariantAutoscaling should exist")

				// Select the VA that targets the expected deployment
				var va *v1alpha1.VariantAutoscaling
				for i := range vaList.Items {
					if vaList.Items[i].Spec.ScaleTargetRef.Name == model.deployment {
						va = &vaList.Items[i]
						break
					}
				}
				Expect(va).NotTo(BeNil(), "A VariantAutoscaling targeting deployment %s should exist", model.deployment)
				vaName = va.Name
				_, _ = fmt.Fprintf(GinkgoWriter, "Found VariantAutoscaling: %s (targets %s)\n", vaName, model.deployment)

				initialOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
				_, _ = fmt.Fprintf(GinkgoWriter, "Initial optimized replicas: %d\n", initialOptimized)

				By("verifying HPA exists and is configured correctly")
				hpaList, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(model.namespace).List(ctx, metav1.ListOptions{
					LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
				})
				Expect(err).NotTo(HaveOccurred(), "Should be able to list HPAs")
				Expect(hpaList.Items).NotTo(BeEmpty(), "At least one WVA HPA should exist")

				// Select the HPA that targets the expected deployment
				var hpa *autoscalingv2.HorizontalPodAutoscaler
				for i := range hpaList.Items {
					if hpaList.Items[i].Spec.ScaleTargetRef.Name == model.deployment {
						hpa = &hpaList.Items[i]
						break
					}
				}
				Expect(hpa).NotTo(BeNil(), "An HPA targeting deployment %s should exist", model.deployment)
				hpaName = hpa.Name
				_, _ = fmt.Fprintf(GinkgoWriter, "Found HPA: %s (targets %s)\n", hpaName, model.deployment)

				By("finding vllm-service by label selector")
				// Use release-specific label selector if WVA_RELEASE_NAME is set
				// This prevents picking up services from previous/parallel test runs
				labelSelector := "app.kubernetes.io/name=workload-variant-autoscaler"
				if wvaReleaseName != "" {
					labelSelector = fmt.Sprintf("%s,app.kubernetes.io/instance=%s", labelSelector, wvaReleaseName)
					_, _ = fmt.Fprintf(GinkgoWriter, "Using release-specific label selector: %s\n", labelSelector)
				}
				svcList, err := k8sClient.CoreV1().Services(model.namespace).List(ctx, metav1.ListOptions{
					LabelSelector: labelSelector,
				})
				Expect(err).NotTo(HaveOccurred(), "Should be able to list services")
				Expect(svcList.Items).NotTo(BeEmpty(), "At least one WVA vllm-service should exist for release %s", wvaReleaseName)

				// Find service that matches this model's deployment
				for _, svc := range svcList.Items {
					// Check if service selector matches the deployment
					if svc.Spec.Selector != nil {
						vllmServiceName = svc.Name
						break
					}
				}
				if vllmServiceName == "" {
					vllmServiceName = svcList.Items[0].Name
				}
				_, _ = fmt.Fprintf(GinkgoWriter, "Found vllm-service: %s\n", vllmServiceName)

				Expect(hpa.Spec.Metrics).To(HaveLen(1), "HPA should have one metric")
				Expect(hpa.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType), "HPA should use external metrics")
				Expect(hpa.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.InfernoDesiredReplicas), "HPA should use inferno_desired_replicas metric")

				hpaMinReplicas = *hpa.Spec.MinReplicas
				_, _ = fmt.Fprintf(GinkgoWriter, "HPA minReplicas: %d\n", hpaMinReplicas)
			})

			It("should verify external metrics API is accessible", func() {
				By("querying external metrics API for inferno_desired_replicas")
				Eventually(func(g Gomega) {
					result, err := k8sClient.RESTClient().
						Get().
						AbsPath("/apis/external.metrics.k8s.io/v1beta1/namespaces/" + model.namespace + "/" + constants.InfernoDesiredReplicas).
						DoRaw(ctx)
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to query external metrics API")
					g.Expect(string(result)).To(ContainSubstring(constants.InfernoDesiredReplicas), "Metric should be available")
					g.Expect(string(result)).To(ContainSubstring(model.deployment), "Metric should be for the correct variant")
				}, 5*time.Minute, 5*time.Second).Should(Succeed())
			})

			It("should create and run parallel load generation jobs", func() {
				By("cleaning up any existing jobs")
				deleteParallelLoadJobs(ctx, jobBaseName, model.namespace, numLoadWorkers)
				time.Sleep(2 * time.Second)

				By("waiting for vllm-service endpoints to exist")
				Eventually(func(g Gomega) {
					endpoints, err := k8sClient.CoreV1().Endpoints(model.namespace).Get(ctx, vllmServiceName, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "vllm-service endpoints should exist")
					g.Expect(endpoints.Subsets).NotTo(BeEmpty(), "vllm-service should have endpoints")

					readyCount := 0
					for _, subset := range endpoints.Subsets {
						readyCount += len(subset.Addresses)
					}
					_, _ = fmt.Fprintf(GinkgoWriter, "%s has %d ready endpoints\n", vllmServiceName, readyCount)
					g.Expect(readyCount).To(BeNumerically(">", 0), "vllm-service should have at least one ready endpoint")
				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				By("waiting for vLLM to be ready to accept requests")
				healthCheckBackoffLimit := int32(15)
				healthCheckJobName := fmt.Sprintf("vllm-health-check-%s", model.name)
				healthCheckJob := &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      healthCheckJobName,
						Namespace: model.namespace,
					},
					Spec: batchv1.JobSpec{
						BackoffLimit: &healthCheckBackoffLimit,
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								RestartPolicy: corev1.RestartPolicyNever,
								Containers: []corev1.Container{{
									Name:    "health-check",
									Image:   "quay.io/curl/curl:8.11.1",
									Command: []string{"/bin/sh", "-c"},
									Args: []string{fmt.Sprintf(`
echo "Checking vLLM readiness at %s:8200..."
curl -sf --max-time 10 http://%s:8200/v1/models && echo "vLLM is ready!" && exit 0
echo "vLLM not ready yet"
exit 1`,
										vllmServiceName, vllmServiceName)},
								}},
							},
						},
					},
				}

				backgroundPropagation := metav1.DeletePropagationBackground
				_ = k8sClient.BatchV1().Jobs(model.namespace).Delete(ctx, healthCheckJobName, metav1.DeleteOptions{
					PropagationPolicy: &backgroundPropagation,
				})
				time.Sleep(2 * time.Second)

				_, createErr := k8sClient.BatchV1().Jobs(model.namespace).Create(ctx, healthCheckJob, metav1.CreateOptions{})
				Expect(createErr).NotTo(HaveOccurred(), "Should be able to create health check job")

				Eventually(func(g Gomega) {
					job, err := k8sClient.BatchV1().Jobs(model.namespace).Get(ctx, healthCheckJobName, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					_, _ = fmt.Fprintf(GinkgoWriter, "Health check job: succeeded=%d, failed=%d, active=%d\n",
						job.Status.Succeeded, job.Status.Failed, job.Status.Active)
					g.Expect(job.Status.Succeeded).To(BeNumerically(">=", 1), "Health check should succeed")
				}, 10*time.Minute, 10*time.Second).Should(Succeed())

				_ = k8sClient.BatchV1().Jobs(model.namespace).Delete(ctx, healthCheckJobName, metav1.DeleteOptions{
					PropagationPolicy: &backgroundPropagation,
				})

				_, _ = fmt.Fprintf(GinkgoWriter, "%s is ready and accepting requests, creating load generation jobs\n", vllmServiceName)

				By("cleaning up any existing load generation jobs")
				_ = k8sClient.BatchV1().Jobs(model.namespace).DeleteCollection(ctx,
					metav1.DeleteOptions{
						PropagationPolicy: &backgroundPropagation,
					},
					metav1.ListOptions{
						LabelSelector: fmt.Sprintf("experiment=%s", jobBaseName),
					})
				time.Sleep(2 * time.Second)

				By(fmt.Sprintf("creating %d parallel load generation jobs", numLoadWorkers))
				loadErr := createParallelLoadJobsForModel(ctx, jobBaseName, model.namespace, vllmServiceName, numLoadWorkers, requestsPerWorker)
				Expect(loadErr).NotTo(HaveOccurred(), "Should be able to create load generation jobs")

				By("waiting for job pods to be running")
				Eventually(func(g Gomega) {
					podList, err := k8sClient.CoreV1().Pods(model.namespace).List(ctx, metav1.ListOptions{
						LabelSelector: fmt.Sprintf("experiment=%s", jobBaseName),
					})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
					g.Expect(len(podList.Items)).To(BeNumerically(">=", numLoadWorkers), "All job pods should exist")

					runningCount := 0
					for _, pod := range podList.Items {
						if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded {
							runningCount++
						}
					}
					g.Expect(runningCount).To(BeNumerically(">=", numLoadWorkers),
						fmt.Sprintf("At least %d job pods should be running, got %d", numLoadWorkers, runningCount))
				}, 3*time.Minute, 5*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "All %d load generation jobs are running\n", numLoadWorkers)
			})

			It("should detect increased load and trigger scale-up", func() {
				By("waiting for load generation to ramp up (30 seconds)")
				time.Sleep(30 * time.Second)

				By("monitoring VariantAutoscaling and HPA for scale-up")
				Eventually(func(g Gomega) {
					va := &v1alpha1.VariantAutoscaling{}
					err := crClient.Get(ctx, client.ObjectKey{
						Namespace: model.namespace,
						Name:      vaName,
					}, va)
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get VariantAutoscaling")

					scaledOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
					currentRateStr := va.Status.CurrentAlloc.Load.ArrivalRate
					_, _ = fmt.Fprintf(GinkgoWriter, "VA optimized replicas: %d (initial: %d, minReplicas: %d), arrival rate: %s\n",
						scaledOptimized, initialOptimized, hpaMinReplicas, currentRateStr)

					if !lowLoad {
						g.Expect(scaledOptimized).To(BeNumerically(">", hpaMinReplicas),
							fmt.Sprintf("WVA should recommend more replicas than minReplicas under load (current: %d, min: %d)", scaledOptimized, hpaMinReplicas))
					} else {
						_, _ = fmt.Fprintf(GinkgoWriter, "Low load detected, skipping scale-up recommendation check\n")
					}

					hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(model.namespace).Get(ctx, hpaName, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get HPA")

					_, _ = fmt.Fprintf(GinkgoWriter, "HPA desiredReplicas: %d, currentReplicas: %d\n",
						hpa.Status.DesiredReplicas, hpa.Status.CurrentReplicas)

					if !lowLoad {
						g.Expect(hpa.Status.DesiredReplicas).To(BeNumerically(">", hpaMinReplicas),
							fmt.Sprintf("HPA should desire more replicas than minReplicas (desired: %d, min: %d)", hpa.Status.DesiredReplicas, hpaMinReplicas))
					}

				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "WVA detected load and recommended %d replicas (up from %d)\n", scaledOptimized, initialOptimized)
			})

			It("should scale deployment to match recommended replicas", func() {
				By("monitoring deployment for actual scale-up")
				Eventually(func(g Gomega) {
					deploy, err := k8sClient.AppsV1().Deployments(model.namespace).Get(ctx, model.deployment, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get deployment")

					scaledReplicas = deploy.Status.ReadyReplicas
					_, _ = fmt.Fprintf(GinkgoWriter, "Current ready replicas: %d (initial: %d, desired: %d)\n",
						scaledReplicas, initialReplicas, scaledOptimized)

					if !lowLoad {
						g.Expect(deploy.Status.Replicas).To(BeNumerically(">", hpaMinReplicas),
							fmt.Sprintf("Deployment should have more total replicas than minReplicas under high load (current: %d, min: %d)", deploy.Status.Replicas, hpaMinReplicas))
						g.Expect(scaledReplicas).To(BeNumerically(">=", scaledOptimized),
							fmt.Sprintf("Deployment should have at least %d ready replicas to match optimizer recommendation", scaledOptimized))
					} else {
						_, _ = fmt.Fprintf(GinkgoWriter, "Low load detected, skipping scale-up check\n")
					}

				}, 10*time.Minute, 10*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "Deployment scaled to %d replicas (up from %d, target was %d)\n", scaledReplicas, initialReplicas, scaledOptimized)
			})

			It("should maintain scaled state while load is active", func() {
				By("verifying deployment stays scaled for at least 1 minute")
				Consistently(func(g Gomega) {
					deploy, err := k8sClient.AppsV1().Deployments(model.namespace).Get(ctx, model.deployment, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get deployment")
					g.Expect(deploy.Status.ReadyReplicas).To(BeNumerically(">=", scaledOptimized),
						fmt.Sprintf("Deployment should maintain at least %d replicas while job is running", scaledOptimized))
				}, 1*time.Minute, 5*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "Deployment maintained %d replicas under load (target: %d)\n", scaledReplicas, scaledOptimized)
			})

			It("should complete the load generation jobs successfully", func() {
				By("waiting for jobs to complete")
				Eventually(func(g Gomega) {
					succeededCount := 0
					for i := 1; i <= numLoadWorkers; i++ {
						jobName := fmt.Sprintf("%s-%d", jobBaseName, i)
						job, err := k8sClient.BatchV1().Jobs(model.namespace).Get(ctx, jobName, metav1.GetOptions{})
						if err != nil {
							continue
						}
						if job.Status.Succeeded >= 1 {
							succeededCount++
						}
					}
					_, _ = fmt.Fprintf(GinkgoWriter, "Jobs completed: %d / %d\n", succeededCount, numLoadWorkers)
					g.Expect(succeededCount).To(BeNumerically(">=", numLoadWorkers),
						fmt.Sprintf("All %d jobs should have succeeded, got %d", numLoadWorkers, succeededCount))
				}, jobCompletionTimeout, 15*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "All load generation jobs completed successfully\n")
			})

			AfterAll(func() {
				By("cleaning up load generation jobs")
				deleteParallelLoadJobs(ctx, jobBaseName, model.namespace, numLoadWorkers)

				_, _ = fmt.Fprintf(GinkgoWriter, "\n========================================\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "%s test completed - scaled from %d to %d replicas\n", model.name, initialReplicas, scaledReplicas)
				_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")
			})
		})
	}
})

// createLoadGenerationJob creates a lightweight Kubernetes Job that generates load using curl
func createLoadGenerationJob(name, namespace, vllmService, experimentLabel string, workerID, numRequests int) *batchv1.Job {
	backoffLimit := int32(0)

	script := fmt.Sprintf(`#!/bin/sh
# =============================================================================
# Load Generator Configuration (injected from Go constants)
# =============================================================================
WORKER_ID=%d
TOTAL_REQUESTS=%d
BATCH_SIZE=%d
CURL_TIMEOUT=%d
MAX_TOKENS=%d
BATCH_SLEEP=%s
MODEL_ID="%s"
VLLM_SERVICE="%s"
MAX_RETRIES=24
RETRY_DELAY=5

# =============================================================================
# Script Start
# =============================================================================
echo "Load generator worker $WORKER_ID starting..."
echo "Sending $TOTAL_REQUESTS requests to $VLLM_SERVICE:8200"

# Wait for vllm-service to be ready
echo "Waiting for $VLLM_SERVICE to be ready..."
CONNECTED=false
for i in $(seq 1 $MAX_RETRIES); do
  if curl -s -o /dev/null -w "%%{http_code}" http://$VLLM_SERVICE:8200/v1/models 2>/dev/null | grep -q 200; then
    echo "Connection test passed on attempt $i"
    CONNECTED=true
    break
  fi
  echo "Attempt $i failed, retrying in ${RETRY_DELAY}s..."
  sleep $RETRY_DELAY
done

if [ "$CONNECTED" != "true" ]; then
  echo "ERROR: Cannot connect to $VLLM_SERVICE after $MAX_RETRIES attempts"
  exit 1
fi

# Send requests aggressively in parallel batches (ignore individual curl failures)
SENT=0
while [ $SENT -lt $TOTAL_REQUESTS ]; do
  for i in $(seq 1 $BATCH_SIZE); do
    if [ $SENT -ge $TOTAL_REQUESTS ]; then break; fi
    (curl -s -o /dev/null --max-time $CURL_TIMEOUT -X POST http://$VLLM_SERVICE:8200/v1/completions \
      -H "Content-Type: application/json" \
      -d "{\"model\":\"$MODEL_ID\",\"prompt\":\"Write a detailed explanation of machine learning algorithms.\",\"max_tokens\":$MAX_TOKENS}" || true) &
    SENT=$((SENT + 1))
  done
  echo "Worker $WORKER_ID: sent $SENT / $TOTAL_REQUESTS requests..."
  sleep $BATCH_SLEEP
done

# Wait for all to complete at the end
wait || true

echo "Worker $WORKER_ID: completed all $TOTAL_REQUESTS requests"
exit 0
`, workerID, numRequests, batchSize, curlTimeoutSeconds, maxTokens, batchSleepDuration, modelID, vllmService)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"experiment": experimentLabel,
				"worker":     fmt.Sprintf("%d", workerID),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"experiment": experimentLabel,
						"worker":     fmt.Sprintf("%d", workerID),
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "load-generator",
							Image:   "quay.io/curl/curl:8.11.1",
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{script},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("2Gi"),
									corev1.ResourceCPU:    resource.MustParse("2"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
}

// createParallelLoadJobsForModel creates multiple parallel load generation jobs for a specific model
func createParallelLoadJobsForModel(ctx context.Context, baseName, namespace, vllmService string, numWorkers, requestsPerWorker int) error {
	for i := 1; i <= numWorkers; i++ {
		jobName := fmt.Sprintf("%s-%d", baseName, i)
		job := createLoadGenerationJob(jobName, namespace, vllmService, baseName, i, requestsPerWorker)
		_, err := k8sClient.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create job %s: %w", jobName, err)
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Created load generation job: %s\n", jobName)
	}
	return nil
}

// deleteParallelLoadJobs deletes all parallel load generation jobs
func deleteParallelLoadJobs(ctx context.Context, baseName, namespace string, numWorkers int) {
	propagationPolicy := metav1.DeletePropagationBackground
	for i := 1; i <= numWorkers; i++ {
		jobName := fmt.Sprintf("%s-%d", baseName, i)
		err := k8sClient.BatchV1().Jobs(namespace).Delete(ctx, jobName, metav1.DeleteOptions{
			PropagationPolicy: &propagationPolicy,
		})
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: failed to delete job %s: %v\n", jobName, err)
		}
	}
}
