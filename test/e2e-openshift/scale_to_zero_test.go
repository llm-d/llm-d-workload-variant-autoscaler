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
	"os"
	"time"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/llm-d/llm-d-workload-variant-autoscaler/api/v1alpha1"
)

// Scale-to-zero test configuration
const (
	// retentionPeriod for scale-to-zero tests
	// Using a short period for faster test execution
	retentionPeriod = "3m"
)

var _ = Describe("Scale-to-Zero Test", Ordered, func() {
	var (
		ctx                   context.Context
		scaleToZeroEnabled    bool
		hpaScaleToZeroEnabled bool
		originalConfigExists  bool
		originalConfigData    map[string]string
	)

	BeforeAll(func() {
		ctx = context.Background()

		// Verify controller namespace matches what controller expects
		// The controller uses config.SystemNamespace() which checks POD_NAMESPACE env var
		// or defaults to "workload-variant-autoscaler-system"
		expectedSystemNamespace := config.SystemNamespace()
		if controllerNamespace != expectedSystemNamespace {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CONTROLLER_NAMESPACE (%s) does not match controller's expected namespace (%s)\n",
				controllerNamespace, expectedSystemNamespace)
			_, _ = fmt.Fprintf(GinkgoWriter, "  Controller uses POD_NAMESPACE env var if set, otherwise defaults to %s\n",
				config.DefaultNamespace)
			_, _ = fmt.Fprintf(GinkgoWriter, "  Ensure CONTROLLER_NAMESPACE matches the actual controller deployment namespace\n")
		}

		// Check if scale-to-zero is enabled via environment variable
		scaleToZeroEnabled = os.Getenv("WVA_SCALE_TO_ZERO") == "true"

		// Check if HPAScaleToZero feature gate is enabled on the cluster
		hpaScaleToZeroEnabled = isHPAScaleToZeroEnabled(ctx)

		_, _ = fmt.Fprintf(GinkgoWriter, "\n========================================\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "Starting Scale-to-Zero Tests\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "  WVA Scale-to-Zero Enabled: %v\n", scaleToZeroEnabled)
		_, _ = fmt.Fprintf(GinkgoWriter, "  HPA Scale-to-Zero Feature Gate: %v\n", hpaScaleToZeroEnabled)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Controller Namespace: %s\n", controllerNamespace)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Expected System Namespace: %s\n", expectedSystemNamespace)
		_, _ = fmt.Fprintf(GinkgoWriter, "  llm-d Namespace: %s\n", llmDNamespace)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Deployment: %s\n", deployment)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Scale-to-Zero ConfigMap: %s\n", scaleToZeroConfigMapName)
		_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")

		// Backup existing scale-to-zero ConfigMap if it exists
		By("checking for existing scale-to-zero ConfigMap")
		existingCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, scaleToZeroConfigMapName, metav1.GetOptions{})
		if err == nil {
			originalConfigExists = true
			originalConfigData = existingCM.Data
			_, _ = fmt.Fprintf(GinkgoWriter, "Found existing scale-to-zero ConfigMap, will restore after tests\n")
		} else {
			originalConfigExists = false
			_, _ = fmt.Fprintf(GinkgoWriter, "No existing scale-to-zero ConfigMap found\n")
		}
	})

	Context("Scale-to-zero enabled - verify scaling behavior", Ordered, func() {
		var (
			initialReplicas int32
			vaName          string
		)

		BeforeAll(func() {
			By("configuring scale-to-zero as enabled")
			scaleToZeroCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scaleToZeroConfigMapName,
					Namespace: controllerNamespace,
				},
				Data: map[string]string{
					"default": fmt.Sprintf(`enable_scale_to_zero: true
retention_period: %s`, retentionPeriod),
				},
			}

			// Delete existing ConfigMap if it exists
			_ = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})

			_, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Create(ctx, scaleToZeroCM, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to create scale-to-zero ConfigMap")

			By("recording initial state of deployment")
			deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to get vLLM deployment")
			initialReplicas = deploy.Status.ReadyReplicas

			_, _ = fmt.Fprintf(GinkgoWriter, "Initial ready replicas: %d\n", initialReplicas)

			By("finding VariantAutoscaling for the deployment")
			vaList := &v1alpha1.VariantAutoscalingList{}
			err = crClient.List(ctx, vaList, client.InNamespace(llmDNamespace))
			Expect(err).NotTo(HaveOccurred(), "Should be able to list VariantAutoscalings")

			// Find VA that targets our deployment
			for _, va := range vaList.Items {
				if va.GetScaleTargetName() == deployment {
					vaName = va.Name
					break
				}
			}

			if vaName == "" {
				Skip("No VariantAutoscaling found for deployment " + deployment)
			}

			_, _ = fmt.Fprintf(GinkgoWriter, "Found VariantAutoscaling: %s\n", vaName)
		})

		It("should recommend zero replicas in VA status when idle", func() {
			By("waiting for scale-to-zero to take effect (no load)")
			// Note: This test assumes no load is being generated
			// In a real test, you would stop all load generators first

			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: llmDNamespace,
					Name:      vaName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				_, _ = fmt.Fprintf(GinkgoWriter, "Current DesiredOptimizedAlloc.NumReplicas: %d\n",
					va.Status.DesiredOptimizedAlloc.NumReplicas)

				// Should scale to 0 when scale-to-zero is enabled and no requests
				g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(Equal(0),
					"VariantAutoscaling should recommend 0 replicas when idle with scale-to-zero enabled")
			}, 10*time.Minute, 30*time.Second).Should(Succeed())
		})

		It("should scale deployment to zero when idle", func() {
			if !hpaScaleToZeroEnabled {
				Skip("HPAScaleToZero feature gate is not enabled on this cluster - see docs/integrations/hpa-integration.md for setup instructions")
			}

			By("verifying deployment has scaled to zero")
			Eventually(func(g Gomega) {
				deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				_, _ = fmt.Fprintf(GinkgoWriter, "Current deployment replicas: %d\n", deploy.Status.Replicas)

				g.Expect(deploy.Status.Replicas).To(Equal(int32(0)),
					"Deployment should have scaled to 0 replicas")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		AfterAll(func() {
			// Cleanup is handled in the outer AfterAll
		})
	})

	Context("Scale-to-zero disabled - verify minimum replica preservation", Ordered, func() {
		var vaName string

		BeforeAll(func() {
			By("configuring scale-to-zero as disabled")
			scaleToZeroCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scaleToZeroConfigMapName,
					Namespace: controllerNamespace,
				},
				Data: map[string]string{
					"default": `enable_scale_to_zero: false`,
				},
			}

			// Delete existing ConfigMap if it exists
			_ = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})

			_, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Create(ctx, scaleToZeroCM, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to create scale-to-zero ConfigMap")

			By("finding VariantAutoscaling for the deployment")
			vaList := &v1alpha1.VariantAutoscalingList{}
			err = crClient.List(ctx, vaList, client.InNamespace(llmDNamespace))
			Expect(err).NotTo(HaveOccurred(), "Should be able to list VariantAutoscalings")

			// Find VA that targets our deployment
			for _, va := range vaList.Items {
				if va.GetScaleTargetName() == deployment {
					vaName = va.Name
					break
				}
			}

			if vaName == "" {
				Skip("No VariantAutoscaling found for deployment " + deployment)
			}

			_, _ = fmt.Fprintf(GinkgoWriter, "Found VariantAutoscaling: %s\n", vaName)
		})

		It("should preserve at least 1 replica when scale-to-zero is disabled", func() {
			By("verifying DesiredOptimizedAlloc is populated")
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: llmDNamespace,
					Name:      vaName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(va.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
					"DesiredOptimizedAlloc should be populated")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("waiting for controller to reconcile with scale-to-zero disabled")
			// First wait for replicas to become >= 1 after ConfigMap change
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: llmDNamespace,
					Name:      vaName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				_, _ = fmt.Fprintf(GinkgoWriter, "Waiting for NumReplicas >= 1, current: %d\n",
					va.Status.DesiredOptimizedAlloc.NumReplicas)

				g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 1),
					"VariantAutoscaling should recommend at least 1 replica after scale-to-zero is disabled")
			}, 5*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying minimum replica is preserved consistently")
			// Then verify it stays at >= 1
			Consistently(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: llmDNamespace,
					Name:      vaName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				_, _ = fmt.Fprintf(GinkgoWriter, "Current DesiredOptimizedAlloc.NumReplicas: %d\n",
					va.Status.DesiredOptimizedAlloc.NumReplicas)

				// Should maintain at least 1 replica when scale-to-zero is disabled
				g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 1),
					"VariantAutoscaling should preserve at least 1 replica when scale-to-zero is disabled")
			}, 2*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying deployment has at least 1 replica")
			deploy, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deployment, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy.Status.Replicas).To(BeNumerically(">=", 1),
				"Deployment should have at least 1 replica when scale-to-zero is disabled")
		})
	})

	Context("Verify scale-to-zero ConfigMap structure", func() {
		It("should accept valid scale-to-zero configuration", func() {
			By("creating a valid scale-to-zero ConfigMap")
			testCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-to-zero-config-test",
					Namespace: controllerNamespace,
				},
				Data: map[string]string{
					"default": `enable_scale_to_zero: true
retention_period: 10m`,
					"model-override": `model_id: test-model
enable_scale_to_zero: false
retention_period: 5m`,
				},
			}

			// Delete if exists
			_ = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, testCM.Name, metav1.DeleteOptions{})

			_, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Create(ctx, testCM, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to create test ConfigMap")

			// Verify the ConfigMap was created correctly
			createdCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, testCM.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdCM.Data).To(HaveKey("default"))
			Expect(createdCM.Data).To(HaveKey("model-override"))
			Expect(createdCM.Data["default"]).To(ContainSubstring("enable_scale_to_zero: true"))
			Expect(createdCM.Data["model-override"]).To(ContainSubstring("model_id: test-model"))

			// Cleanup
			err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, testCM.Name, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	AfterAll(func() {
		By("restoring original scale-to-zero ConfigMap state")

		if originalConfigExists {
			// Restore original ConfigMap
			existingCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, scaleToZeroConfigMapName, metav1.GetOptions{})
			if err == nil {
				existingCM.Data = originalConfigData
				_, err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Update(ctx, existingCM, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred(), "Should be able to restore original ConfigMap")
				_, _ = fmt.Fprintf(GinkgoWriter, "Restored original scale-to-zero ConfigMap\n")
			}
		} else {
			// Delete the ConfigMap we created
			err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})
			if err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Note: Could not delete scale-to-zero ConfigMap: %v\n", err)
			}
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "Scale-to-Zero tests completed\n")
	})
})
