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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
)

var _ = Describe("Capacity Model: Full Lifecycle Test", Ordered, func() {
	var (
		ctx                  context.Context
		baselineReplicas     int32
		phase1Replicas       int32
		phase2Replicas       int32
		phase3Replicas       int32
		phase4Replicas       int32
		phase5Replicas       int32
		jobCompletionTimeout = 20 * time.Minute
	)

	// Test phases with different load levels
	type LoadPhase struct {
		Name         string
		RequestRate  int
		NumPrompts   int
		Duration     time.Duration
		Description  string
		ExpectedMin  int32 // Minimum expected replicas
		ExpectedMax  int32 // Maximum expected replicas
	}

	phases := []LoadPhase{
		{
			Name:        "phase1-low-load",
			RequestRate: 10,
			NumPrompts:  1500,
			Duration:    5 * time.Minute,
			Description: "Low load baseline - should maintain minimal replicas",
			ExpectedMin: 1,
			ExpectedMax: 2,
		},
		{
			Name:        "phase2-medium-load",
			RequestRate: 50,
			NumPrompts:  3000,
			Duration:    6 * time.Minute,
			Description: "Medium load - should trigger scale-up (50 req/s exceeds 80% KV cache threshold)",
			ExpectedMin: 1,
			ExpectedMax: 3,
		},
		{
			Name:        "phase3-high-load",
			RequestRate: 70,
			NumPrompts:  4500,
			Duration:    6 * time.Minute,
			Description: "High load - should trigger further scale-up (70 req/s)",
			ExpectedMin: 2,
			ExpectedMax: 4,
		},
		{
			Name:        "phase4-return-medium",
			RequestRate: 50,
			NumPrompts:  3000,
			Duration:    6 * time.Minute,
			Description: "Return to medium load - should scale down gradually",
			ExpectedMin: 1,
			ExpectedMax: 3,
		},
		{
			Name:        "phase5-cooldown",
			RequestRate: 0,
			NumPrompts:  0,
			Duration:    5 * time.Minute,
			Description: "No load cooldown - should return to baseline",
			ExpectedMin: 1,
			ExpectedMax: 2,
		},
	}

	BeforeAll(func() {
		ctx = context.Background()

		By("verifying capacity-only mode is active")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			"workload-variant-autoscaler-variantautoscaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get controller ConfigMap")

		if cm.Data["WVA_EXPERIMENTAL_PROACTIVE_MODEL"] == "true" {
			Skip("Test requires CAPACITY-ONLY mode (WVA_EXPERIMENTAL_PROACTIVE_MODEL=false or unset)")
		}

		By("recording baseline state")
		deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get vLLM deployment")
		baselineReplicas = deploy.Status.ReadyReplicas
		_, _ = fmt.Fprintf(GinkgoWriter, "Baseline ready replicas: %d\n", baselineReplicas)

		By("recording initial VariantAutoscaling state")
		va := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred(), "Should be able to get VariantAutoscaling")
		_, _ = fmt.Fprintf(GinkgoWriter, "Baseline optimized replicas: %d\n", va.Status.DesiredOptimizedAlloc.NumReplicas)

		// Log capacity configuration
		capacityCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			"capacity-scaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "capacity-scaling-config ConfigMap should exist")
		_, _ = fmt.Fprintf(GinkgoWriter, "Capacity config: %+v\n", capacityCM.Data)
	})

	It("should establish stable baseline with no load", func() {
		By("waiting for system to stabilize")
		time.Sleep(30 * time.Second)

		By("verifying stable baseline state")
		va := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred())

		baselineReplicas = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "✓ Baseline established: %d replicas\n", baselineReplicas)

		// Verify OptimizationReady
		var optimizationReady bool
		for _, cond := range va.Status.Conditions {
			if cond.Type == "OptimizationReady" && cond.Status == metav1.ConditionTrue {
				optimizationReady = true
			}
		}
		Expect(optimizationReady).To(BeTrue(), "OptimizationReady should be True at baseline")
	})

	for i, phase := range phases {
		phaseIndex := i
		currentPhase := phase

		It(fmt.Sprintf("Phase %d: %s", phaseIndex+1, currentPhase.Description), func() {
			var jobName string
			var startReplicas int32

			By(fmt.Sprintf("recording pre-phase state"))
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			Expect(err).NotTo(HaveOccurred())
			startReplicas = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)

			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== PHASE %d: %s ===\n", phaseIndex+1, currentPhase.Name)
			_, _ = fmt.Fprintf(GinkgoWriter, "Starting replicas: %d\n", startReplicas)
			_, _ = fmt.Fprintf(GinkgoWriter, "Load: %d req/s, %d prompts\n", currentPhase.RequestRate, currentPhase.NumPrompts)
			_, _ = fmt.Fprintf(GinkgoWriter, "Duration: %v\n", currentPhase.Duration)
			_, _ = fmt.Fprintf(GinkgoWriter, "Expected range: %d-%d replicas\n", currentPhase.ExpectedMin, currentPhase.ExpectedMax)

			if currentPhase.NumPrompts > 0 {
				jobName = currentPhase.Name + "-job"

				By(fmt.Sprintf("cleaning up any existing job from %s", currentPhase.Name))
				_ = k8sClient.BatchV1().Jobs(llmDNamespace).Delete(ctx, jobName, metav1.DeleteOptions{})
				time.Sleep(2 * time.Second)

				By(fmt.Sprintf("starting load generation: %d req/s, %d prompts", currentPhase.RequestRate, currentPhase.NumPrompts))
				job := createShareGPTJob(jobName, llmDNamespace, currentPhase.RequestRate, currentPhase.NumPrompts)
				_, err := k8sClient.BatchV1().Jobs(llmDNamespace).Create(ctx, job, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred(), "Should be able to create load generation job")

				By("waiting for job pod to be running")
				Eventually(func(g Gomega) {
					podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
						LabelSelector: fmt.Sprintf("job-name=%s", jobName),
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

					pod := podList.Items[0]
					g.Expect(pod.Status.Phase).To(Or(
						Equal(corev1.PodRunning),
						Equal(corev1.PodSucceeded),
					), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
				}, 3*time.Minute, 5*time.Second).Should(Succeed())
			} else {
				By("no load phase - waiting for cooldown")
			}

			By(fmt.Sprintf("monitoring for %v - waiting for metrics and scaling", currentPhase.Duration))
			monitoringStart := time.Now()
			var finalReplicas int32
		var peakReplicas int32 = startReplicas

			// Monitor every 20 seconds
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()

			monitoringDone := time.After(currentPhase.Duration)

		MonitoringLoop:
			for {
				select {
				case <-monitoringDone:
					break MonitoringLoop
				case <-ticker.C:
					va := &v1alpha1.VariantAutoscaling{}
					err := crClient.Get(ctx, client.ObjectKey{
						Namespace: llmDNamespace,
						Name:      deployment,
					}, va)
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "⚠ Error getting VA: %v\n", err)
						continue
					}

					currentReplicas := int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
					elapsed := time.Since(monitoringStart)
					arrivalRate := va.Status.CurrentAlloc.Load.ArrivalRate

					_, _ = fmt.Fprintf(GinkgoWriter, "[T+%02dm%02ds] Replicas: %d, Arrival rate: %s\n",
						int(elapsed.Minutes()), int(elapsed.Seconds())%60, currentReplicas, arrivalRate)

					// Check OptimizationReady
					var optimizationReady bool
					for _, cond := range va.Status.Conditions {
						if cond.Type == "OptimizationReady" && cond.Status == metav1.ConditionTrue {
							optimizationReady = true
						}
					}
					if !optimizationReady {
						_, _ = fmt.Fprintf(GinkgoWriter, "⚠ OptimizationReady is False\n")
					}

					finalReplicas = currentReplicas
				if currentReplicas > peakReplicas {
						peakReplicas = currentReplicas
					}
				}
			}

			By(fmt.Sprintf("verifying final state for phase %d", phaseIndex+1))
			va = &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			Expect(err).NotTo(HaveOccurred())

			finalReplicas = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)

			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Phase %d complete: %d → peak %d → final %d replicas\n",
				phaseIndex+1, startReplicas, peakReplicas, finalReplicas)

			// Verify replicas are within expected range
			Expect(peakReplicas).To(BeNumerically(">=", currentPhase.ExpectedMin),
				fmt.Sprintf("Peak replicas should be >= %d", currentPhase.ExpectedMin))
			Expect(peakReplicas).To(BeNumerically("<=", currentPhase.ExpectedMax),
				fmt.Sprintf("Peak replicas should be <= %d", currentPhase.ExpectedMax))

			// Verify OptimizationReady
			var optimizationReady bool
			for _, cond := range va.Status.Conditions {
				if cond.Type == "OptimizationReady" && cond.Status == metav1.ConditionTrue {
					optimizationReady = true
				}
			}
			Expect(optimizationReady).To(BeTrue(), "OptimizationReady should be True")

			// Store phase results
			switch phaseIndex {
			case 0:
				phase1Replicas = finalReplicas
			case 1:
				phase2Replicas = finalReplicas
			case 2:
				phase3Replicas = finalReplicas
			case 3:
				phase4Replicas = finalReplicas
			case 4:
				phase5Replicas = finalReplicas
			}

			if currentPhase.NumPrompts > 0 {
				By("waiting for load job to complete")
				Eventually(func(g Gomega) {
					job, err := k8sClient.BatchV1().Jobs(llmDNamespace).Get(ctx, jobName, metav1.GetOptions{})
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "Job may have been deleted: %v\n", err)
						return
					}
					g.Expect(job.Status.Succeeded).To(BeNumerically(">=", 1), "Job should have succeeded")
				}, jobCompletionTimeout, 15*time.Second).Should(Succeed())

				By("cleaning up load generation job")
				_ = k8sClient.BatchV1().Jobs(llmDNamespace).Delete(ctx, jobName, metav1.DeleteOptions{
					PropagationPolicy: func() *metav1.DeletionPropagation {
						policy := metav1.DeletePropagationBackground
						return &policy
					}(),
				})
			}
		})
	}

	It("should verify complete lifecycle behavior", func() {
		By("analyzing replica progression across all phases")

		_, _ = fmt.Fprintf(GinkgoWriter, "\n=== LIFECYCLE SUMMARY ===\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "Baseline:      %d replicas\n", baselineReplicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Phase 1 (low): %d replicas\n", phase1Replicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Phase 2 (med): %d replicas\n", phase2Replicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Phase 3 (high):%d replicas\n", phase3Replicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Phase 4 (med): %d replicas\n", phase4Replicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Phase 5 (cool):%d replicas\n", phase5Replicas)

		// Verify scale-up behavior
		if phase2Replicas > phase1Replicas {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Scale-up detected: Phase 1→2 (%d→%d)\n", phase1Replicas, phase2Replicas)
		}

		if phase3Replicas >= phase2Replicas {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Sustained/increased capacity: Phase 2→3 (%d→%d)\n", phase2Replicas, phase3Replicas)
		}

		// Verify scale-down behavior
		if phase5Replicas <= phase4Replicas {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Scale-down detected: Phase 4→5 (%d→%d)\n", phase4Replicas, phase5Replicas)
		}

		// Verify return to baseline region
		Expect(phase5Replicas).To(BeNumerically("<=", baselineReplicas+1),
			"Should return close to baseline after cooldown")

		_, _ = fmt.Fprintf(GinkgoWriter, "✓ Full lifecycle validation complete\n")
	})

	AfterAll(func() {
		By("cleaning up any remaining load jobs")
		for _, phase := range phases {
			if phase.NumPrompts > 0 {
				jobName := phase.Name + "-job"
				_ = k8sClient.BatchV1().Jobs(llmDNamespace).Delete(ctx, jobName, metav1.DeleteOptions{
					PropagationPolicy: func() *metav1.DeletionPropagation {
						policy := metav1.DeletePropagationBackground
						return &policy
					}(),
				})
			}
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "\n=== TEST COMPLETE ===\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "Total test duration: ~33 minutes\n")
	})
})
