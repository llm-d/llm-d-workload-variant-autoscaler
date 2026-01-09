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
	"math"
	"regexp"
	"strings"
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
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
)

// sanitizeK8sName converts a human-readable name to a valid Kubernetes resource name.
// Kubernetes names must be lowercase alphanumeric, may contain '-' and '.', and
// must start and end with an alphanumeric character.
func sanitizeK8sName(name string) string {
	// Convert to lowercase and replace spaces with hyphens
	result := strings.ToLower(name)
	result = strings.ReplaceAll(result, " ", "-")
	// Remove any characters that aren't lowercase alphanumeric or hyphens
	re := regexp.MustCompile(`[^a-z0-9-]`)
	result = re.ReplaceAllString(result, "")
	// Trim leading/trailing hyphens
	result = strings.Trim(result, "-")
	return result
}

var lowLoad = numPrompts <= 2000 && requestRate <= 8

// Load generation configuration constants
// These values were tuned empirically to achieve ~2-3 replica scale-up without excessive scaling.
// Original values (baseLoadWorkers=10, batchSize=50, batchSleepDuration=0.1) caused cascade
// scaling to 8+ replicas because WVA reconciles frequently (around every 30s with the tested
// configuration) while pods take 5-7 min to become ready.
//
// NOTE: These values are environment-specific and may need tuning for different hardware
// configurations, model sizes, or inference implementations. The current values target
// sustainable load that triggers scale-up but caps at ~3 replicas under the tested
// reconciliation interval and pod readiness characteristics.
const (
	baseLoadWorkers         = 2     // Reduced from 10 to limit concurrent load (targets max ~3 replicas)
	baseReplicas            = 2     // The replica count baseLoadWorkers is tuned for
	maxSingleReplicaWorkers = 1     // Max workers when deployment has only 1 replica (prevents queue explosion)
	batchSize               = 10    // Reduced from 50 to limit concurrent requests per batch
	curlTimeoutSeconds      = 180   // Timeout for each curl request (increased for longer outputs)
	batchSleepDuration      = "0.5" // Increased from 0.1 to slow request rate between batches
	// Note: maxTokens and requestsPerWorker are now per-model - see modelTestConfig
)

// modelTestConfig holds configuration for testing a specific model
type modelTestConfig struct {
	name       string // Human-readable name for logging
	namespace  string // Kubernetes namespace
	deployment string // Deployment name
	// gatewayService is the Istio gateway service name for routing traffic
	// Traffic should go through the gateway to be properly routed via InferencePool
	gatewayService string
	// maxTokens controls max tokens per request - use lower values (64) for fast responses,
	// higher values (1500) for sustained GPU load testing
	maxTokens int
	// requestsPerWorker controls how many requests each worker sends - use higher values
	// for fast-completing requests (low tokens) to sustain load longer
	requestsPerWorker int
}

// getModelsToTest returns the list of models to test based on configuration
func getModelsToTest() []modelTestConfig {
	// Gateway service name pattern for llm-d infrastructure
	// This is created by the llm-d-infra chart
	gatewayServiceName := "infra-inference-scheduling-inference-gateway-istio"

	models := []modelTestConfig{
		{
			name:              "Model A1",
			namespace:         llmDNamespace,
			deployment:        deployment,
			gatewayService:    gatewayServiceName,
			maxTokens:         400,  // Moderate tokens for ~3s requests, sustains queue during test
			requestsPerWorker: 1000, // Balanced request count for moderate-length requests
		},
	}

	// Add Model B if secondary namespace is configured (multi-model mode)
	if multiModelMode && llmDNamespaceB != "" {
		models = append(models, modelTestConfig{
			name:              "Model B",
			namespace:         llmDNamespaceB,
			deployment:        deployment, // Model B uses the same deployment name as A1
			gatewayService:    gatewayServiceName,
			maxTokens:         1500, // Long requests to test sustained GPU load
			requestsPerWorker: 500,  // Fewer requests since each takes ~10s
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
				sanitizedName        string // Kubernetes-safe version of model name
				jobBaseName          string
				initialReplicas      int32
				initialOptimized     int32
				hpaMinReplicas       int32
				hpaName              string
				vaName               string
				scaledReplicas       int32
				scaledOptimized      int32
				scaledLoadWorkers    int // Load workers scaled to initial replicas
				jobCompletionTimeout = 10 * time.Minute
			)

			BeforeAll(func() {
				// Use sanitized model name for Kubernetes resource names
				sanitizedName = sanitizeK8sName(model.name)
				jobBaseName = fmt.Sprintf("load-gen-%s", sanitizedName)

				_, _ = fmt.Fprintf(GinkgoWriter, "\n========================================\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "Starting test for %s\n", model.name)
				_, _ = fmt.Fprintf(GinkgoWriter, "  Namespace: %s\n", model.namespace)
				_, _ = fmt.Fprintf(GinkgoWriter, "  Deployment: %s\n", model.deployment)
				_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")

				By(fmt.Sprintf("recording initial state of %s deployment", model.name))
				deploy, err := k8sClient.AppsV1().Deployments(model.namespace).Get(ctx, model.deployment, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Should be able to get vLLM deployment")
				initialReplicas = deploy.Status.ReadyReplicas
				if initialReplicas == 0 {
					initialReplicas = 1 // Avoid division issues, treat 0 as 1
				}
				// Scale load workers proportionally to initial replicas
				// Formula: scaledWorkers = baseLoadWorkers * initialReplicas / baseReplicas
				// This ensures consistent load pressure per replica
				// Use floating-point with explicit rounding to avoid integer division truncation
				scaledLoadWorkers = int(math.Round(float64(baseLoadWorkers*initialReplicas) / float64(baseReplicas)))
				if scaledLoadWorkers < 1 {
					scaledLoadWorkers = 1 // Minimum 1 worker
				}
				// Cap workers for single-replica deployments to avoid cold-start overwhelm
				// With 1 replica, EPP routes all traffic to one pod - too many workers causes
				// queue explosion before scale-up kicks in
				if initialReplicas == 1 && scaledLoadWorkers > maxSingleReplicaWorkers {
					scaledLoadWorkers = maxSingleReplicaWorkers
				}
				_, _ = fmt.Fprintf(GinkgoWriter, "Initial ready replicas: %d\n", initialReplicas)
				_, _ = fmt.Fprintf(GinkgoWriter, "Scaled load workers: %d (base: %d for %d replicas)\n", scaledLoadWorkers, baseLoadWorkers, baseReplicas)

				// Get HPA first to know minReplicas for VA stabilization check
				By("verifying HPA exists and getting minReplicas")
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
				hpaMinReplicas = *hpa.Spec.MinReplicas
				_, _ = fmt.Fprintf(GinkgoWriter, "Found HPA: %s (targets %s, minReplicas=%d)\n", hpaName, model.deployment, hpaMinReplicas)

				Expect(hpa.Spec.Metrics).To(HaveLen(1), "HPA should have one metric")
				Expect(hpa.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType), "HPA should use external metrics")
				Expect(hpa.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.InfernoDesiredReplicas), "HPA should use inferno_desired_replicas metric")

				By("verifying gateway service exists for load routing")
				// Traffic goes through the Istio gateway to be properly routed via InferencePool/EPP
				// The gateway service is created by the llm-d-infra chart
				gatewaySvc, err := k8sClient.CoreV1().Services(model.namespace).Get(ctx, model.gatewayService, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Gateway service %s should exist in namespace %s", model.gatewayService, model.namespace)
				_, _ = fmt.Fprintf(GinkgoWriter, "Found gateway service: %s (ClusterIP: %s)\n", gatewaySvc.Name, gatewaySvc.Spec.ClusterIP)

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

				// Wait for VA to stabilize at minReplicas before recording initial state
				// This ensures we're measuring scale-up from load, not residual scale from prior activity
				By("waiting for VA to stabilize at minReplicas")
				Eventually(func(g Gomega) {
					currentVA := &v1alpha1.VariantAutoscaling{}
					err := crClient.Get(ctx, client.ObjectKey{
						Namespace: model.namespace,
						Name:      va.Name,
					}, currentVA)
					g.Expect(err).NotTo(HaveOccurred())
					optimized := int32(currentVA.Status.DesiredOptimizedAlloc.NumReplicas)
					_, _ = fmt.Fprintf(GinkgoWriter, "Waiting for VA to be ready: optimized=%d, minReplicas=%d\n", optimized, hpaMinReplicas)
					// Wait for optimized >= minReplicas (allows for initial 0 during engine startup)
					g.Expect(optimized).To(BeNumerically(">=", hpaMinReplicas), "VA should have optimized >= minReplicas")
				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				// Wait for deployment to be fully stable (no pods in transition)
				// This prevents starting load while pods are terminating from scale-down
				By("waiting for deployment to stabilize (no pods in transition)")
				Eventually(func(g Gomega) {
					currentDeploy, err := k8sClient.AppsV1().Deployments(model.namespace).Get(ctx, model.deployment, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					specReplicas := *currentDeploy.Spec.Replicas
					statusReplicas := currentDeploy.Status.Replicas
					readyReplicas := currentDeploy.Status.ReadyReplicas
					_, _ = fmt.Fprintf(GinkgoWriter, "Waiting for deployment stability: spec=%d, status=%d, ready=%d\n",
						specReplicas, statusReplicas, readyReplicas)
					// All replica counts must match - no pods starting or terminating
					g.Expect(statusReplicas).To(Equal(specReplicas), "Status replicas should match spec")
					g.Expect(readyReplicas).To(Equal(specReplicas), "Ready replicas should match spec")
				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				// Re-read VA to get stabilized state
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: model.namespace,
					Name:      va.Name,
				}, va)
				Expect(err).NotTo(HaveOccurred())
				initialOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
				_, _ = fmt.Fprintf(GinkgoWriter, "Initial optimized replicas (after stabilization): %d\n", initialOptimized)
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
				deleteParallelLoadJobs(ctx, jobBaseName, model.namespace, scaledLoadWorkers)
				time.Sleep(2 * time.Second)

				By("waiting for gateway endpoints to exist")
				Eventually(func(g Gomega) {
					endpoints, err := k8sClient.CoreV1().Endpoints(model.namespace).Get(ctx, model.gatewayService, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "gateway endpoints should exist")
					g.Expect(endpoints.Subsets).NotTo(BeEmpty(), "gateway should have endpoints")

					readyCount := 0
					for _, subset := range endpoints.Subsets {
						readyCount += len(subset.Addresses)
					}
					_, _ = fmt.Fprintf(GinkgoWriter, "Gateway %s has %d ready endpoints\n", model.gatewayService, readyCount)
					g.Expect(readyCount).To(BeNumerically(">", 0), "gateway should have at least one ready endpoint")
				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				By("waiting for gateway to be ready to accept requests")
				healthCheckBackoffLimit := int32(15)
				healthCheckJobName := fmt.Sprintf("gateway-health-check-%s", sanitizedName)
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
echo "Checking gateway readiness at %s:80..."
RESPONSE=$(curl -sf --max-time 10 http://%s:80/v1/models 2>&1)
CURL_EXIT=$?
if [ $CURL_EXIT -ne 0 ]; then
  echo "Gateway not responding (curl exit code: $CURL_EXIT)"
  echo "Response: $RESPONSE"
  exit 1
fi
# Verify response contains actual model data (not empty or 502 error)
if echo "$RESPONSE" | grep -q '"id":'; then
  echo "Gateway is ready with model data!"
  echo "Response: $RESPONSE"
  exit 0
fi
echo "Gateway responded but no model data found in response"
echo "Response: $RESPONSE"
exit 1`,
										model.gatewayService, model.gatewayService)},
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

				_, _ = fmt.Fprintf(GinkgoWriter, "Gateway %s is ready and accepting requests, creating load generation jobs\n", model.gatewayService)

				By("cleaning up any existing load generation jobs")
				_ = k8sClient.BatchV1().Jobs(model.namespace).DeleteCollection(ctx,
					metav1.DeleteOptions{
						PropagationPolicy: &backgroundPropagation,
					},
					metav1.ListOptions{
						LabelSelector: fmt.Sprintf("experiment=%s", jobBaseName),
					})
				time.Sleep(2 * time.Second)

				By(fmt.Sprintf("creating %d parallel load generation jobs targeting gateway", scaledLoadWorkers))
				loadErr := createParallelLoadJobsForModel(ctx, jobBaseName, model.namespace, model.gatewayService, scaledLoadWorkers, model.requestsPerWorker, model.maxTokens)
				Expect(loadErr).NotTo(HaveOccurred(), "Should be able to create load generation jobs")

				By("waiting for job pods to be running")
				Eventually(func(g Gomega) {
					podList, err := k8sClient.CoreV1().Pods(model.namespace).List(ctx, metav1.ListOptions{
						LabelSelector: fmt.Sprintf("experiment=%s", jobBaseName),
					})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
					g.Expect(len(podList.Items)).To(BeNumerically(">=", scaledLoadWorkers), "All job pods should exist")

					runningCount := 0
					for _, pod := range podList.Items {
						if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded {
							runningCount++
						}
					}
					g.Expect(runningCount).To(BeNumerically(">=", scaledLoadWorkers),
						fmt.Sprintf("At least %d job pods should be running, got %d", scaledLoadWorkers, runningCount))
				}, 3*time.Minute, 5*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "All %d load generation jobs are running\n", scaledLoadWorkers)
			})

			It("should detect increased load and trigger scale-up", func() {
				By("waiting for load generation to ramp up (30 seconds)")
				time.Sleep(30 * time.Second)

				By("monitoring VariantAutoscaling and HPA for scale-up")
				zeroArrivalCount := 0
				loadGenLogsShown := false
				Eventually(func(g Gomega) {
					va := &v1alpha1.VariantAutoscaling{}
					err := crClient.Get(ctx, client.ObjectKey{
						Namespace: model.namespace,
						Name:      vaName,
					}, va)
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get VariantAutoscaling")

					scaledOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
					// currentRateStr := va.Status.DesiredOptimizedAlloc.Load.ArrivalRate (Load not in status)
					currentRateStr := "unknown"

					_, _ = fmt.Fprintf(GinkgoWriter, "VA optimized replicas: %d (initial: %d, minReplicas: %d), arrival rate: %s\n",
						scaledOptimized, initialOptimized, hpaMinReplicas, currentRateStr)

					// Log queue metrics for observability
					if podQueues, totalQueue, qErr := utils.GetQueueMetrics(model.namespace); qErr == nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "Queue metrics: total=%.0f, per-pod=%v\n", totalQueue, podQueues)
					}

					// Log load gen job output if arrival rate stays at 0 for too long (debugging)
					if currentRateStr == "0.00" || currentRateStr == "" {
						zeroArrivalCount++
						// After 6 iterations (60s) with zero arrival rate, log load gen jobs
						if zeroArrivalCount >= 6 && !loadGenLogsShown {
							loadGenLogsShown = true
							logLoadGenJobLogs(ctx, jobBaseName, model.namespace, scaledLoadWorkers)
						}
					} else {
						zeroArrivalCount = 0 // Reset counter when we see traffic
					}

					if !lowLoad {
						// Scale-up means we should have MORE replicas than our initial stabilized state
						// (not just more than minReplicas, which could be satisfied by initial startup)
						g.Expect(scaledOptimized).To(BeNumerically(">", initialOptimized),
							fmt.Sprintf("WVA should recommend more replicas than initial under load (current: %d, initial: %d)", scaledOptimized, initialOptimized))
					} else {
						_, _ = fmt.Fprintf(GinkgoWriter, "Low load detected, skipping scale-up recommendation check\n")
					}

					hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(model.namespace).Get(ctx, hpaName, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get HPA")

					_, _ = fmt.Fprintf(GinkgoWriter, "HPA desiredReplicas: %d, currentReplicas: %d\n",
						hpa.Status.DesiredReplicas, hpa.Status.CurrentReplicas)

					if !lowLoad {
						// HPA should also desire more replicas than initial
						g.Expect(hpa.Status.DesiredReplicas).To(BeNumerically(">", initialOptimized),
							fmt.Sprintf("HPA should desire more replicas than initial (desired: %d, initial: %d)", hpa.Status.DesiredReplicas, initialOptimized))
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
				By("verifying deployment stays scaled for at least 30 seconds")
				Consistently(func(g Gomega) {
					deploy, err := k8sClient.AppsV1().Deployments(model.namespace).Get(ctx, model.deployment, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to get deployment")
					g.Expect(deploy.Status.ReadyReplicas).To(BeNumerically(">=", scaledOptimized),
						fmt.Sprintf("Deployment should maintain at least %d replicas while job is running", scaledOptimized))
				}, 30*time.Second, 5*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "Deployment maintained %d replicas under load (target: %d)\n", scaledReplicas, scaledOptimized)
			})

			It("should complete the load generation jobs successfully", func() {
				By("waiting for jobs to complete")
				Eventually(func(g Gomega) {
					succeededCount := 0
					for i := 1; i <= scaledLoadWorkers; i++ {
						jobName := fmt.Sprintf("%s-%d", jobBaseName, i)
						job, err := k8sClient.BatchV1().Jobs(model.namespace).Get(ctx, jobName, metav1.GetOptions{})
						if err != nil {
							continue
						}
						if job.Status.Succeeded >= 1 {
							succeededCount++
						}
					}
					_, _ = fmt.Fprintf(GinkgoWriter, "Jobs completed: %d / %d\n", succeededCount, scaledLoadWorkers)
					g.Expect(succeededCount).To(BeNumerically(">=", scaledLoadWorkers),
						fmt.Sprintf("All %d jobs should have succeeded, got %d", scaledLoadWorkers, succeededCount))
				}, jobCompletionTimeout, 15*time.Second).Should(Succeed())

				_, _ = fmt.Fprintf(GinkgoWriter, "All load generation jobs completed successfully\n")
			})

			AfterAll(func() {
				By("cleaning up load generation jobs")
				deleteParallelLoadJobs(ctx, jobBaseName, model.namespace, scaledLoadWorkers)

				_, _ = fmt.Fprintf(GinkgoWriter, "\n========================================\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "%s test completed - scaled from %d to %d replicas\n", model.name, initialReplicas, scaledReplicas)
				_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")
			})
		})
	}
})

// createLoadGenerationJob creates a lightweight Kubernetes Job that generates load using curl
// The gatewayService parameter should be the Istio gateway service name (port 80)
// modelMaxTokens specifies max tokens per request (use lower values for fast responses, higher for sustained load)
func createLoadGenerationJob(name, namespace, gatewayService, experimentLabel string, workerID, numRequests, modelMaxTokens int) *batchv1.Job {
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
GATEWAY_SERVICE="%s"
MAX_RETRIES=24
RETRY_DELAY=5

# =============================================================================
# Script Start
# =============================================================================
echo "Load generator worker $WORKER_ID starting..."
echo "Sending $TOTAL_REQUESTS requests to gateway $GATEWAY_SERVICE:80"

# Wait for gateway to be ready
echo "Waiting for gateway $GATEWAY_SERVICE to be ready..."
CONNECTED=false
for i in $(seq 1 $MAX_RETRIES); do
  if curl -s -o /dev/null -w "%%{http_code}" http://$GATEWAY_SERVICE:80/v1/models 2>/dev/null | grep -q 200; then
    echo "Connection test passed on attempt $i"
    CONNECTED=true
    break
  fi
  echo "Attempt $i failed, retrying in ${RETRY_DELAY}s..."
  sleep $RETRY_DELAY
done

if [ "$CONNECTED" != "true" ]; then
  echo "ERROR: Cannot connect to gateway $GATEWAY_SERVICE after $MAX_RETRIES attempts"
  exit 1
fi

# Send requests aggressively in parallel batches (ignore individual curl failures)
SENT=0
while [ $SENT -lt $TOTAL_REQUESTS ]; do
  for i in $(seq 1 $BATCH_SIZE); do
    if [ $SENT -ge $TOTAL_REQUESTS ]; then break; fi
    (curl -s -o /dev/null --max-time $CURL_TIMEOUT -X POST http://$GATEWAY_SERVICE:80/v1/completions \
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
`, workerID, numRequests, batchSize, curlTimeoutSeconds, modelMaxTokens, batchSleepDuration, modelID, gatewayService)

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
// Traffic is routed through the gateway service (port 80) to be properly handled by InferencePool/EPP
func createParallelLoadJobsForModel(ctx context.Context, baseName, namespace, gatewayService string, numWorkers, requestsPerWorker, modelMaxTokens int) error {
	for i := 1; i <= numWorkers; i++ {
		jobName := fmt.Sprintf("%s-%d", baseName, i)
		job := createLoadGenerationJob(jobName, namespace, gatewayService, baseName, i, requestsPerWorker, modelMaxTokens)
		_, err := k8sClient.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create job %s: %w", jobName, err)
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Created load generation job: %s (targeting gateway %s, maxTokens=%d)\n", jobName, gatewayService, modelMaxTokens)
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

// logLoadGenJobLogs captures and logs output from load generation pods for debugging
func logLoadGenJobLogs(ctx context.Context, baseName, namespace string, numWorkers int) {
	_, _ = fmt.Fprintf(GinkgoWriter, "\n=== LOAD GENERATION JOB LOGS (debugging zero arrival rate) ===\n")

	podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("experiment=%s", baseName),
	})
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to list load gen pods: %v\n", err)
		return
	}

	if len(podList.Items) == 0 {
		_, _ = fmt.Fprintf(GinkgoWriter, "No load gen pods found with label experiment=%s\n", baseName)
		return
	}

	// Log status and logs for first 3 pods (to avoid too much output)
	maxPods := 3
	if len(podList.Items) < maxPods {
		maxPods = len(podList.Items)
	}

	for i := 0; i < maxPods; i++ {
		pod := podList.Items[i]
		_, _ = fmt.Fprintf(GinkgoWriter, "\n--- Pod: %s (Phase: %s) ---\n", pod.Name, pod.Status.Phase)

		// Get pod logs
		logOpts := &corev1.PodLogOptions{
			TailLines: ptr(int64(50)), // Last 50 lines
		}
		logs, err := k8sClient.CoreV1().Pods(namespace).GetLogs(pod.Name, logOpts).DoRaw(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get logs: %v\n", err)
			continue
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", string(logs))
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "=== END LOAD GENERATION JOB LOGS ===\n\n")
}

// ptr returns a pointer to the given value (helper for int64)
func ptr(i int64) *int64 {
	return &i
}
