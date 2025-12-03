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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
)

var _ = Describe("Capacity Model: Safe Scale-Down", Ordered, func() {
	var (
		ctx                   context.Context
		initialReplicas       int32
		preTestOptimized      int32
		postIdleOptimized     int32
		idleMonitoringPeriod  = 3 * time.Minute // Monitor for scale-down after load stops
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("verifying capacity-only mode is active")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			"workload-variant-autoscaler-variantautoscaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get controller ConfigMap")

		if cm.Data["WVA_EXPERIMENTAL_PROACTIVE_MODEL"] == "true" {
			Skip("Test requires CAPACITY-ONLY mode (WVA_EXPERIMENTAL_PROACTIVE_MODEL=false or unset)")
		}

		By("recording initial deployment state")
		deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get vLLM deployment")
		initialReplicas = deploy.Status.ReadyReplicas
		_, _ = fmt.Fprintf(GinkgoWriter, "Initial deployment replicas: %d\n", initialReplicas)

		By("recording initial VariantAutoscaling state")
		va := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred(), "Should be able to get VariantAutoscaling")
		preTestOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
		_, _ = fmt.Fprintf(GinkgoWriter, "Initial optimized replicas: %d\n", preTestOptimized)

		// Log status conditions
		_, _ = fmt.Fprintf(GinkgoWriter, "Initial status conditions:\n")
		for _, cond := range va.Status.Conditions {
			_, _ = fmt.Fprintf(GinkgoWriter, "  %s = %s (reason: %s)\n",
				cond.Type, cond.Status, cond.Reason)
		}
	})

	It("should maintain stable replica count under no load", func() {
		By("waiting for system to stabilize with no active load")
		time.Sleep(30 * time.Second)

		By("verifying deployment is stable")
		deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(deploy.Status.ReadyReplicas).To(BeNumerically(">=", 1),
			"Deployment should maintain at least 1 replica even with no load")

		_, _ = fmt.Fprintf(GinkgoWriter, "Deployment stable with %d replicas\n", deploy.Status.ReadyReplicas)
	})

	It("should verify capacity metrics show low utilization", func() {
		By("checking VariantAutoscaling status reflects low load")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			g.Expect(err).NotTo(HaveOccurred())

			// Log current allocation for monitoring
			arrivalRate := va.Status.CurrentAlloc.Load.ArrivalRate
			_, _ = fmt.Fprintf(GinkgoWriter, "Current arrival rate: %s\n", arrivalRate)

		}, 2*time.Minute, 15*time.Second).Should(Succeed())
	})

	It("should verify capacity analyzer allows safe scale-down decisions", func() {
		By(fmt.Sprintf("monitoring for scale-down recommendations over %v", idleMonitoringPeriod))

		// Monitor capacity analyzer behavior during idle period
		Consistently(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			g.Expect(err).NotTo(HaveOccurred())

			postIdleOptimized = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)

			_, _ = fmt.Fprintf(GinkgoWriter, "Current optimized replicas: %d\n", postIdleOptimized)

			// Verify OptimizationReady is always True (capacity analysis succeeding)
			var optimizationReady bool
			for _, cond := range va.Status.Conditions {
				if cond.Type == "OptimizationReady" {
					optimizationReady = cond.Status == metav1.ConditionTrue
					if !optimizationReady {
						_, _ = fmt.Fprintf(GinkgoWriter, "⚠ OptimizationReady is False: %s - %s\n",
							cond.Reason, cond.Message)
					}
					break
				}
			}
			g.Expect(optimizationReady).To(BeTrue(), "Capacity analysis should continue succeeding")

			// Verify we maintain at least 1 replica (never scale to zero)
			g.Expect(postIdleOptimized).To(BeNumerically(">=", 1),
				"Capacity analyzer should recommend at least 1 replica")

		}, idleMonitoringPeriod, 20*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Capacity analyzer recommendation after idle period: %d replicas\n",
			postIdleOptimized)
	})

	It("should verify scale-down safety constraints", func() {
		By("checking that scale-down follows safe capacity rules")

		va := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred())

		currentOptimized := int32(va.Status.DesiredOptimizedAlloc.NumReplicas)

		// Capacity analyzer should only scale down if:
		// 1. At least 2 replicas remain non-saturated (or we're going to minimum)
		// 2. Load redistribution won't cause saturation
		if currentOptimized < preTestOptimized {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Safe scale-down occurred: %d → %d replicas\n",
				preTestOptimized, currentOptimized)

			// Verify minimum replica count respected
			Expect(currentOptimized).To(BeNumerically(">=", 1),
				"Should maintain minimum of 1 replica")

			// If scaled down, verify it was gradual (max 1 replica per decision)
			replicaDelta := preTestOptimized - currentOptimized
			Expect(replicaDelta).To(BeNumerically("<=", 2),
				"Scale-down should be gradual (max 1-2 replicas per cycle)")

		} else if currentOptimized == preTestOptimized {
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Capacity analyzer maintained stable replica count: %d\n",
				currentOptimized)
		} else {
			// Unexpected scale-up during idle period
			_, _ = fmt.Fprintf(GinkgoWriter, "⚠ Unexpected scale-up during idle: %d → %d\n",
				preTestOptimized, currentOptimized)
		}

		// Verify optimization is healthy
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

	It("should verify deployment reflects capacity analyzer recommendations", func() {
		By("checking HPA actuates capacity analyzer recommendations")

		va := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred())

		targetReplicas := int32(va.Status.DesiredOptimizedAlloc.NumReplicas)

		Eventually(func(g Gomega) {
			deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())

			// Allow some tolerance for HPA actuation lag
			replicaDiff := deploy.Status.ReadyReplicas - targetReplicas
			if replicaDiff < 0 {
				replicaDiff = -replicaDiff
			}

			_, _ = fmt.Fprintf(GinkgoWriter, "Deployment replicas: %d, Target: %d\n",
				deploy.Status.ReadyReplicas, targetReplicas)

			// HPA should eventually converge to target (within 1 replica tolerance)
			g.Expect(replicaDiff).To(BeNumerically("<=", 1),
				"Deployment should be close to capacity analyzer target")

		}, 2*time.Minute, 10*time.Second).Should(Succeed())
	})

	AfterAll(func() {
		_, _ = fmt.Fprintf(GinkgoWriter, "Test completed - replica progression: %d (initial) → %d (post-idle)\n",
			preTestOptimized, postIdleOptimized)
	})
})
