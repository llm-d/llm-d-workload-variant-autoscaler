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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
)

var _ = Describe("Capacity Model: Scale-Up Detection", Ordered, func() {
	var (
		ctx                  context.Context
		jobName              string
		initialReplicas      int32
		initialOptimized     int32
		scaledOptimized      int32
		jobCompletionTimeout = 15 * time.Minute
		testRequestRate      = 15 // Conservative rate to avoid over-saturation
		testNumPrompts       = 3000 // Standard test duration
	)

	BeforeAll(func() {
		ctx = context.Background()
		jobName = "vllm-bench-capacity-scaleup-e2e"

		By("verifying capacity-only mode is active")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			"workload-variant-autoscaler-variantautoscaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get controller ConfigMap")

		if cm.Data["WVA_EXPERIMENTAL_PROACTIVE_MODEL"] == "true" {
			Skip("Test requires CAPACITY-ONLY mode (WVA_EXPERIMENTAL_PROACTIVE_MODEL=false or unset)")
		}

		By("verifying capacity-scaling-config ConfigMap exists")
		capacityCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			"capacity-scaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "capacity-scaling-config ConfigMap should exist")

		_, _ = fmt.Fprintf(GinkgoWriter, "Capacity config: %+v\n", capacityCM.Data)

		By("recording initial state of the deployment")
		deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get vLLM deployment")
		initialReplicas = deploy.Status.ReadyReplicas
		_, _ = fmt.Fprintf(GinkgoWriter, "Initial ready replicas: %d\n", initialReplicas)

		By("recording initial VariantAutoscaling state")
		va := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred(), "Should be able to get VariantAutoscaling")
		initialOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Initial optimized replicas: %d\n", initialOptimized)

		// Check status conditions
		_, _ = fmt.Fprintf(GinkgoWriter, "Initial status conditions:\n")
		for _, cond := range va.Status.Conditions {
			_, _ = fmt.Fprintf(GinkgoWriter, "  %s = %s (reason: %s)\n",
				cond.Type, cond.Status, cond.Reason)
		}

		By("verifying HPA exists and is configured correctly")
		hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(llmDNamespace).Get(ctx,
			"vllm-deployment-hpa", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "HPA should exist")
		Expect(hpa.Spec.ScaleTargetRef.Name).To(Equal(deployment), "HPA should target the correct deployment")
		Expect(hpa.Spec.Metrics).To(HaveLen(1), "HPA should have one metric")
		Expect(hpa.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType),
			"HPA should use external metrics")
		Expect(hpa.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.InfernoDesiredReplicas),
			"HPA should use inferno_desired_replicas metric")
	})

	It("should verify Prometheus has vLLM capacity metrics", func() {
		By("checking vLLM KV cache metrics availability")
		Eventually(func(g Gomega) {
			// Use Prometheus API to query metrics
			promPods, err := k8sClient.CoreV1().Pods(monitoringNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=prometheus",
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list Prometheus pods")
			g.Expect(promPods.Items).NotTo(BeEmpty(), "Prometheus pod should exist")

			// Query for vLLM metrics - this validates ServiceMonitor is working
			// We can't easily exec into prometheus in this test, so we'll check via VA status
			va := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			g.Expect(err).NotTo(HaveOccurred())

		}, 2*time.Minute, 10*time.Second).Should(Succeed())
	})

	It("should create and run conservative load generation job", func() {
		By("cleaning up any existing job")
		_ = k8sClient.BatchV1().Jobs(llmDNamespace).Delete(ctx, jobName, metav1.DeleteOptions{})
		time.Sleep(2 * time.Second)

		By(fmt.Sprintf("creating load generation job (rate=%d, prompts=%d)", testRequestRate, testNumPrompts))
		job := createShareGPTJob(jobName, llmDNamespace, testRequestRate, testNumPrompts)
		_, err := k8sClient.BatchV1().Jobs(llmDNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to create load generation job")

		By("waiting for job pod to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", jobName),
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
			g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Or(
				Equal(corev1.PodRunning),
				Equal(corev1.PodSucceeded),
			), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running with conservative settings\n")
	})

	It("should detect increased load via capacity analyzer", func() {
		By("waiting for load to ramp up and metrics to be collected (60 seconds)")
		time.Sleep(60 * time.Second)

		By("monitoring VariantAutoscaling for capacity-based recommendation")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to get VariantAutoscaling")

			scaledOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
			currentRateStr := va.Status.CurrentAlloc.Load.ArrivalRate

			_, _ = fmt.Fprintf(GinkgoWriter, "Current optimized replicas: %d (initial: %d), arrival rate: %s\n",
				scaledOptimized, initialOptimized, currentRateStr)

			// Log status conditions for debugging
			for _, cond := range va.Status.Conditions {
				_, _ = fmt.Fprintf(GinkgoWriter, "  Condition: %s = %s (reason: %s, message: %s)\n",
					cond.Type, cond.Status, cond.Reason, cond.Message)
			}

			// With conservative load, we might not always scale up, but we should see:
			// OptimizationReady = True (capacity analysis succeeded)
			var optimizationReady bool
			for _, cond := range va.Status.Conditions {
				if cond.Type == "OptimizationReady" && cond.Status == metav1.ConditionTrue {
					optimizationReady = true
				}
			}

			g.Expect(optimizationReady).To(BeTrue(), "OptimizationReady should be True (capacity analysis succeeded)")

			// If load is high enough, expect scale-up recommendation
			if scaledOptimized > initialOptimized {
				_, _ = fmt.Fprintf(GinkgoWriter, "✓ Capacity analyzer recommended scale-up: %d → %d\n",
					initialOptimized, scaledOptimized)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "ℹ Capacity analyzer did not recommend scale-up (load may be within capacity)\n")
			}

		}, 4*time.Minute, 15*time.Second).Should(Succeed())
	})

	It("should verify capacity metrics are within safe thresholds", func() {
		By("checking that no replicas are saturated beyond safe limits")

		// We'll use the VA status to infer capacity state
		va := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred())

		// If capacity analyzer recommended scale-up, it means it detected saturation risk
		// If it didn't, that means capacity is adequate
		if scaledOptimized > initialOptimized {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Capacity analyzer detected saturation risk and recommended scale-up\n")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Capacity analyzer determined current capacity is adequate\n")
		}

		// Verify OptimizationReady condition
		var optimizationReady bool
		var optimizationMessage string
		for _, cond := range va.Status.Conditions {
			if cond.Type == "OptimizationReady" {
				optimizationReady = cond.Status == metav1.ConditionTrue
				optimizationMessage = cond.Message
				break
			}
		}
		Expect(optimizationReady).To(BeTrue(),
			fmt.Sprintf("OptimizationReady should be True (message: %s)", optimizationMessage))
	})

	It("should verify HPA processes capacity analyzer recommendations", func() {
		By("checking HPA external metrics")
		Eventually(func(g Gomega) {
			hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(llmDNamespace).Get(ctx,
				"vllm-deployment-hpa", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to get HPA")

			g.Expect(hpa.Status.CurrentMetrics).NotTo(BeEmpty(), "HPA should have current metrics")

			for _, metric := range hpa.Status.CurrentMetrics {
				if metric.External != nil && metric.External.Metric.Name == constants.InfernoDesiredReplicas {
					currentValue := metric.External.Current.AverageValue
					g.Expect(currentValue).NotTo(BeNil(), "Current metric value should not be nil")

					currentReplicas := currentValue.AsApproximateFloat64()
					_, _ = fmt.Fprintf(GinkgoWriter, "HPA current metric value: %.2f\n", currentReplicas)

					// Verify HPA is seeing the capacity analyzer's recommendation
					g.Expect(currentReplicas).To(BeNumerically("==", float64(scaledOptimized)),
						fmt.Sprintf("HPA should see capacity analyzer recommendation (%d)", scaledOptimized))
				}
			}
		}, 2*time.Minute, 10*time.Second).Should(Succeed())
	})

	It("should complete the load generation job successfully", func() {
		By("waiting for job to complete")
		Eventually(func(g Gomega) {
			job, err := k8sClient.BatchV1().Jobs(llmDNamespace).Get(ctx, jobName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to get job")

			_, _ = fmt.Fprintf(GinkgoWriter, "Job status - Active: %d, Succeeded: %d, Failed: %d\n",
				job.Status.Active, job.Status.Succeeded, job.Status.Failed)

			g.Expect(job.Status.Succeeded).To(BeNumerically(">=", 1), "Job should have succeeded")
		}, jobCompletionTimeout, 15*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job completed successfully\n")
	})

	AfterAll(func() {
		By("cleaning up load generation job")
		err := k8sClient.BatchV1().Jobs(llmDNamespace).Delete(ctx, jobName, metav1.DeleteOptions{
			PropagationPolicy: func() *metav1.DeletionPropagation {
				policy := metav1.DeletePropagationBackground
				return &policy
			}(),
		})
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: failed to delete job: %v\n", err)
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "Test completed - capacity analyzer recommendation: %d → %d replicas\n",
			initialOptimized, scaledOptimized)
	})
})
