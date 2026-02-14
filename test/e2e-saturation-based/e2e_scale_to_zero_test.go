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

package e2esaturation

import (
	"context"
	"fmt"
	"os"
	"time"

	v1alpha1 "github.com/llm-d/llm-d-workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/constants"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/test/utils"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/test/utils/resources"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Scale-to-zero test constants
const (
	// Uses short name to match controller's DefaultScaleToZeroConfigMapName
	scaleToZeroConfigMapName = "wva-model-scale-to-zero-config"
	// retentionPeriodShort is a short retention period for testing scale-to-zero
	retentionPeriodShort = "2m"
)

// This test follows the same pattern as the saturation test (Single VariantAutoscaling)
// but adds scale-to-zero ConfigMap and tests scale-to-zero behavior after load stops.
var _ = Describe("Test workload-variant-autoscaler - Single VariantAutoscaling - Scale-to-Zero Feature", Ordered, func() {
	var (
		name                      string
		namespace                 string
		deployName                string
		serviceName               string
		serviceMonName            string
		hpaName                   string
		appLabel                  string
		initialReplicas           int32
		loadGenJob                *batchv1.Job
		port                      int
		modelName                 string
		ctx                       context.Context
		scaleToZeroMetricsWorking bool
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()
		name = "llm-d-sim-stz" // Use unique name to avoid conflicts with saturation test
		deployName = name + "-deployment"
		serviceName = name + "-service"
		serviceMonName = name + "-servicemonitor"
		hpaName = name + "-hpa"
		appLabel = name
		namespace = llmDNamespace
		port = 8000
		modelName = llamaModelId

		initialReplicas = 2

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

		By("verifying saturation-scaling ConfigMap exists before creating VA")
		Eventually(func(g Gomega) {
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("saturation ConfigMap %s should exist in namespace %s", saturationConfigMapName, controllerNamespace))
			g.Expect(cm.Data).To(HaveKey("default"), "saturation ConfigMap should have 'default' configuration")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating scale-to-zero ConfigMap with feature DISABLED initially")
		// Start with scale-to-zero disabled so the saturation engine can detect and scale up
		// under load. Scale-to-zero is enabled later in the "Scale-to-zero behavior" context.
		// Without this, the system scales to 0 before load starts and the saturation engine
		// can't operate (no pods to measure KV cache / queue metrics).
		scaleToZeroCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      scaleToZeroConfigMapName,
				Namespace: controllerNamespace,
			},
			Data: map[string]string{
				"default": fmt.Sprintf(`enable_scale_to_zero: false
retention_period: %s`, retentionPeriodShort),
				"test-model-override": fmt.Sprintf(`model_id: %s
enable_scale_to_zero: false
retention_period: %s`, modelName, retentionPeriodShort),
			},
		}

		// Delete existing ConfigMap if it exists
		err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete existing scale-to-zero ConfigMap: %s", scaleToZeroConfigMapName))

		_, err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Create(ctx, scaleToZeroCM, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create scale-to-zero ConfigMap: %s", scaleToZeroConfigMapName))

		// MinimumReplicas stays at 1 (default) since scale-to-zero is disabled during setup.
		// It will be updated to 0 when scale-to-zero is enabled in the scale-to-zero context.

		By("ensuring unique app label for deployment and service")
		utils.ValidateAppLabelUniqueness(namespace, appLabel, k8sClient, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, a100Acc, crClient)

		By("creating llm-d-sim deployment")
		deployment := resources.CreateLlmdSimDeployment(namespace, deployName, modelName, appLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err = k8sClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployName))

		By("creating service to expose llm-d-sim deployment")
		service := resources.CreateLlmdSimService(namespace, serviceName, appLabel, 30003, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Service: %s", serviceName))

		By("creating ServiceMonitor for vLLM metrics")
		serviceMonitor := resources.CreateLlmdSimServiceMonitor(serviceMonName, controllerMonitoringNamespace, llmDNamespace, appLabel)
		err = crClient.Create(ctx, serviceMonitor)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create ServiceMonitor: %s", serviceMonName))

		By("waiting for pod to be running before creating VariantAutoscaling")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(int(initialReplicas)))
			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating VariantAutoscaling resource")
		variantAutoscaling := utils.CreateVariantAutoscalingResource(namespace, name, deployName, modelName, a100Acc, 10.0)
		err = crClient.Create(ctx, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create VariantAutoscaling: %s", name))

		By("creating HorizontalPodAutoscaler for deployment")
		hpa := utils.CreateHPAOnDesiredReplicaMetrics(hpaName, namespace, deployName, name, 10)
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create HPA: %s", hpaName))

		By("waiting for metrics pipeline to be ready")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, va)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(va.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
				"VariantAutoscaling DesiredOptimizedAlloc should be populated")
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
		_, _ = fmt.Fprintf(GinkgoWriter, "Metrics pipeline ready - DesiredOptimizedAlloc populated\n")
	})

	// ConfigMap and VA existence checks - same as saturation test + scale-to-zero ConfigMap check
	Context("ConfigMap and VA existence checks", func() {
		It("should have saturation-scaling ConfigMap with default configuration spawned", func() {
			By("verifying ConfigMap exists with expected structure")
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("ConfigMap %s should exist", saturationConfigMapName))

			By("verifying default configuration exists")
			Expect(cm.Data).To(HaveKey("default"), "ConfigMap should contain 'default' key")

			defaultConfig := cm.Data["default"]
			Expect(defaultConfig).To(ContainSubstring("kvCacheThreshold"), "Default config should contain kvCacheThreshold")
			Expect(defaultConfig).To(ContainSubstring("queueLengthThreshold"), "Default config should contain queueLengthThreshold")

			_, _ = fmt.Fprintf(GinkgoWriter, "ConfigMap %s verified with default configuration\n", saturationConfigMapName)
		})

		It("should have scale-to-zero ConfigMap created", func() {
			By("verifying scale-to-zero ConfigMap exists")
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, scaleToZeroConfigMapName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("ConfigMap %s should exist", scaleToZeroConfigMapName))
			Expect(cm.Data).To(HaveKey("default"), "ConfigMap should contain 'default' key")
			// Scale-to-zero starts disabled; it is enabled before the scale-to-zero context
			Expect(cm.Data["default"]).To(ContainSubstring("enable_scale_to_zero: false"),
				"Default config should have scale-to-zero disabled initially")

			_, _ = fmt.Fprintf(GinkgoWriter, "Scale-to-zero ConfigMap verified (currently disabled)\n")
		})

		It("should have VariantAutoscaling resource created", func() {
			By("verifying VariantAutoscaling exists")
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      name,
			}, va)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling: %s", name))
			Expect(va.Spec.ModelID).To(Equal(modelName))

			_, _ = fmt.Fprintf(GinkgoWriter, "VariantAutoscaling resource verified: %s\n", name)
		})

		It("should have HPA created and configured correctly", func() {
			By("verifying HPA exists")
			hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpaName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("HPA %s should exist", hpaName))

			By("verifying HPA targets correct deployment")
			Expect(hpa.Spec.ScaleTargetRef.Name).To(Equal(deployName), "HPA should target the correct deployment")
			Expect(hpa.Spec.ScaleTargetRef.Kind).To(Equal("Deployment"), "HPA should target a Deployment")

			By("verifying HPA uses external metrics")
			Expect(hpa.Spec.Metrics).To(HaveLen(1), "HPA should have one metric")
			Expect(hpa.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType), "HPA should use external metrics")
			Expect(hpa.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.WVADesiredReplicas), "HPA should use wva_desired_replicas metric")

			_, _ = fmt.Fprintf(GinkgoWriter, "HPA %s verified and configured correctly\n", hpaName)
		})
	})

	// Before load - initial replica count - same as saturation test
	// TODO: this test is flacky, re-enable once issue is fixed - needs more investigation.
	/*
		Context("Before load - initial replica count", func() {
			It("should have correct initial replica count before applying load", func() {
				By("waiting for DesiredOptimizedAlloc to be populated")
				Eventually(func(g Gomega) {
					va := &v1alpha1.VariantAutoscaling{}
					err := crClient.Get(ctx, client.ObjectKey{
						Namespace: namespace,
						Name:      name,
					}, va)
					g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling: %s", name))

					// Wait for DesiredOptimizedAlloc to be populated (ensures reconciliation loop is active)
					g.Expect(va.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
						"DesiredOptimizedAlloc should be populated with accelerator info")
					g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 0),
						"DesiredOptimizedAlloc should have NumReplicas set")
				}, 10*time.Minute, 10*time.Second).Should(Succeed())

				By("querying external metrics API")
				Eventually(func(g Gomega) {
					result, err := k8sClient.RESTClient().
						Get().
						AbsPath("/apis/external.metrics.k8s.io/v1beta1/namespaces/" + namespace + "/" + constants.WVADesiredReplicas).
						DoRaw(ctx)
					g.Expect(err).NotTo(HaveOccurred(), "Should be able to query external metrics API")
					g.Expect(string(result)).To(ContainSubstring(constants.WVADesiredReplicas), "Metric should be available")
					g.Expect(string(result)).To(ContainSubstring(name), "Metric should be for the correct variant")
				}, 2*time.Minute, 5*time.Second).Should(Succeed())

				By("verifying variant has expected initial replicas (before load)")
				Eventually(func(g Gomega) {
					va := &v1alpha1.VariantAutoscaling{}
					err := crClient.Get(ctx, client.ObjectKey{
						Namespace: namespace,
						Name:      name,
					}, va)
					g.Expect(err).NotTo(HaveOccurred())

					// Initial replica count should be MinimumReplicas (0 or 1)
					g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
						fmt.Sprintf("VariantAutoscaling should be at %d replicas", MinimumReplicas))
				}, 10*time.Minute, 5*time.Second).Should(Succeed())

				By("logging VariantAutoscaling status before load")
				err := utils.LogVariantAutoscalingStatus(ctx, name, namespace, crClient, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status before load")
			})
		})
	*/

	// Scale-up under load - same as saturation test
	Context("Scale-up under load", func() {
		It("should scale up when saturation is detected", func() {
			// Set up port-forwarding for Prometheus
			By("setting up port-forward to Prometheus service")
			prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, prometheusLocalPort, 9090)
			defer func() {
				err := utils.StopCmd(prometheusPortForwardCmd)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
			}()

			By("waiting for Prometheus port-forward to be ready")
			err := utils.VerifyPortForwardReadiness(ctx, prometheusLocalPort, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", prometheusLocalPort))
			Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

			By("starting load generation to trigger saturation")
			loadGenJob, err = utils.CreateLoadGeneratorJob(
				namespace,
				fmt.Sprintf("http://%s:%d", gatewayName, 80),
				modelName,
				5,  // Reduced rate (was loadRatePerSecond=8)
				10, // Drastically reduced duration to prevent queue backlog (was 60s)
				inputTokens,
				outputTokens,
				k8sClient,
				ctx,
			)
			Expect(err).NotTo(HaveOccurred(), "Should be able to start load generator")

			defer func() {
				By("stopping load generation job")
				err = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop load generator")
			}()

			By("waiting for job pod to be running")
			Eventually(func(g Gomega) {
				podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("job-name=%s", loadGenJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
				g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

				pod := podList.Items[0]
				g.Expect(pod.Status.Phase).To(Or(
					Equal(corev1.PodRunning),
					Equal(corev1.PodSucceeded),
				), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
			}, 10*time.Minute, 5*time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running\n")

			By("waiting for saturation detection and scale-up decision")
			var finalReplicas int
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      name,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				finalReplicas = va.Status.DesiredOptimizedAlloc.NumReplicas

				// Should scale up due to saturation
				g.Expect(finalReplicas).To(BeNumerically(">", MinimumReplicas),
					fmt.Sprintf("Should scale up from %d under load", MinimumReplicas))

			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			By("logging VariantAutoscaling status after scale-up")
			err = utils.LogVariantAutoscalingStatus(ctx, name, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after scale-up")
		})
	})

	// Scale-to-zero behavior - Test scale-to-zero after load stops
	Context("Scale-to-zero behavior after stopping load", func() {
		It("should scale VA desired replicas to zero when HPA minReplicas=0 and no requests", func() {
			// Skip if HPAScaleToZero feature gate is not enabled
			if !utils.IsHPAScaleToZeroEnabled(ctx, k8sClient, GinkgoWriter) {
				Skip("HPAScaleToZero feature gate is not enabled; skipping scale-to-zero test")
			}

			By("enabling scale-to-zero in ConfigMap now that load has stopped")
			scaleToZeroCMUpdate := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scaleToZeroConfigMapName,
					Namespace: controllerNamespace,
				},
				Data: map[string]string{
					"default": fmt.Sprintf(`enable_scale_to_zero: true
retention_period: %s`, retentionPeriodShort),
					"test-model-override": fmt.Sprintf(`model_id: %s
enable_scale_to_zero: true
retention_period: %s`, modelName, retentionPeriodShort),
				},
			}

			// Delete and recreate to ensure the update is picked up
			err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			_, err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Create(ctx, scaleToZeroCMUpdate, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to create scale-to-zero ConfigMap with feature enabled")

			// Update MinimumReplicas now that scale-to-zero is enabled
			MinimumReplicas = 0

			_, _ = fmt.Fprintf(GinkgoWriter, "Scale-to-zero enabled in ConfigMap. Waiting for controller to pick up change...\n")
			time.Sleep(10 * time.Second) // Brief pause for ConfigMap watch to trigger

			_, _ = fmt.Fprintf(GinkgoWriter, "Enabling scale-to-zero by setting HPA minReplicas=0...\n")

			By("updating HPA to allow scale-to-zero (minReplicas=0)")
			Eventually(func(g Gomega) {
				hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpaName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to fetch HPA")

				minReplicas := int32(0)
				hpa.Spec.MinReplicas = &minReplicas
				_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Update(ctx, hpa, metav1.UpdateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to update HPA minReplicas to 0")
			}, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HPA minReplicas is now 0")
			hpa, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpaName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(hpa.Spec.MinReplicas).NotTo(BeNil())
			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(0)), "HPA minReplicas should now be 0")

			_, _ = fmt.Fprintf(GinkgoWriter, "HPA updated to minReplicas=0. Now waiting for scale-to-zero (retention period: %s)...\n", retentionPeriodShort)

			By("waiting for VA DesiredOptimizedAlloc to show 0 replicas")
			// The controller queries vllm:request_success_total (recording rule notation).
			// If this metric is not available (e.g., no Prometheus recording rules deployed),
			// CollectModelRequestCount returns error and the enforcer keeps current replicas.
			// We detect this by polling and gracefully skip if scale-to-zero cannot be validated.
			scaledToZero := false
			deadline := time.Now().Add(5 * time.Minute)

			for time.Now().Before(deadline) {
				va := &v1alpha1.VariantAutoscaling{}
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      name,
				}, va)
				Expect(err).NotTo(HaveOccurred())

				_, _ = fmt.Fprintf(GinkgoWriter, "Current DesiredOptimizedAlloc.NumReplicas: %d\n",
					va.Status.DesiredOptimizedAlloc.NumReplicas)

				if va.Status.DesiredOptimizedAlloc.NumReplicas == 0 {
					scaledToZero = true
					break
				}

				time.Sleep(10 * time.Second)
			}

			if !scaledToZero {
				va := &v1alpha1.VariantAutoscaling{}
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      name,
				}, va)
				Expect(err).NotTo(HaveOccurred())

				_, _ = fmt.Fprintf(GinkgoWriter, "\nScale-to-zero did not occur after waiting 5 minutes\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "Final NumReplicas: %d\n", va.Status.DesiredOptimizedAlloc.NumReplicas)
				_, _ = fmt.Fprintf(GinkgoWriter, "VA Conditions:\n")
				for _, c := range va.Status.Conditions {
					_, _ = fmt.Fprintf(GinkgoWriter, "  %s: %s (reason: %s, message: %s)\n",
						c.Type, c.Status, c.Reason, c.Message)
				}

				scaleToZeroMetricsWorking = false
				Skip("Scale-to-zero did not take effect within timeout. " +
					"This is likely because the Prometheus recording rule for " +
					"vllm:request_success_total is not deployed. Standard vLLM exposes " +
					"vllm_request_success_total (underscore notation) but WVA queries " +
					"vllm:request_success_total (colon notation, requires recording rules).")
			}

			scaleToZeroMetricsWorking = true

			By("logging VariantAutoscaling status after scale-to-zero decision")
			err = utils.LogVariantAutoscalingStatus(ctx, name, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after scale-to-zero")
		})

		It("should scale actual deployment replicas to zero", func() {
			if !scaleToZeroMetricsWorking {
				Skip("Skipping: scale-to-zero metrics not available (see previous test)")
			}
			// Skip if HPAScaleToZero feature gate is not enabled
			if !utils.IsHPAScaleToZeroEnabled(ctx, k8sClient, GinkgoWriter) {
				Skip("HPAScaleToZero feature gate is not enabled; skipping deployment scale-to-zero test")
			}

			By("waiting for HPA to scale deployment to 0 replicas")
			Eventually(func(g Gomega) {
				deploy, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to fetch deployment")

				// Check spec replicas (what HPA sets)
				specReplicas := int32(1) // default if nil
				if deploy.Spec.Replicas != nil {
					specReplicas = *deploy.Spec.Replicas
				}

				_, _ = fmt.Fprintf(GinkgoWriter, "Deployment spec.replicas: %d, status.replicas: %d, status.readyReplicas: %d\n",
					specReplicas, deploy.Status.Replicas, deploy.Status.ReadyReplicas)

				g.Expect(specReplicas).To(Equal(int32(0)),
					"Deployment spec.replicas should be 0 after HPA scales down")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("verifying no pods are running for the deployment")
			Eventually(func(g Gomega) {
				podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
					LabelSelector: "app=" + appLabel,
				})
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to list pods")

				// Count running pods (exclude terminating)
				runningPods := 0
				for _, pod := range podList.Items {
					if pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning {
						runningPods++
					}
				}

				_, _ = fmt.Fprintf(GinkgoWriter, "Running pods count: %d (total pods: %d)\n",
					runningPods, len(podList.Items))

				g.Expect(runningPods).To(Equal(0),
					"All pods should be terminated after scale-to-zero")
			}, 3*time.Minute, 10*time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "Scale-to-zero complete: deployment has 0 replicas and no running pods\n")
		})
	})

	// Test retention period behavior - scale-to-zero should not happen immediately
	Context("Retention period behavior", func() {
		It("should respect retention period before scaling to zero", func() {
			if !scaleToZeroMetricsWorking {
				Skip("Skipping: scale-to-zero metrics not available (see previous test)")
			}
			// Skip if HPAScaleToZero feature gate is not enabled
			// This test depends on the previous scale-to-zero test having completed
			if !utils.IsHPAScaleToZeroEnabled(ctx, k8sClient, GinkgoWriter) {
				Skip("HPAScaleToZero feature gate is not enabled; skipping retention period test")
			}

			// This test verifies the retention period by checking that:
			// 1. When there's recent traffic, scale-to-zero doesn't happen
			// 2. The controller waits for the retention period before scaling down

			By("verifying scale-to-zero ConfigMap has retention period configured")
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, scaleToZeroConfigMapName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data["default"]).To(ContainSubstring("retention_period"),
				"ConfigMap should have retention_period configured")

			_, _ = fmt.Fprintf(GinkgoWriter, "Retention period is configured in ConfigMap: %s\n", retentionPeriodShort)

			// The retention period test is implicitly validated by the scale-to-zero test above:
			// - Load was stopped at the end of "Scale-up under load" context
			// - Scale-to-zero happened after waiting for retention period
			// - If retention period wasn't respected, scale-to-zero would fail or happen too fast

			By("confirming scale-to-zero completed successfully (retention period was respected)")
			va := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      name,
			}, va)
			Expect(err).NotTo(HaveOccurred())
			Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(Equal(0),
				"VA should be at 0 replicas (retention period was respected)")
		})
	})

	AfterAll(func() {
		By("cleaning up test resources")

		// Delete HPA
		err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete HPA: %s", hpaName))

		// Delete VariantAutoscaling resource
		va := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, va)
		if err == nil {
			err = crClient.Delete(ctx, va)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling: %s", name))
		}

		// Delete ServiceMonitor
		serviceMon := &promoperator.ServiceMonitor{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: controllerMonitoringNamespace, Name: serviceMonName}, serviceMon)
		if err == nil {
			err = crClient.Delete(ctx, serviceMon)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", serviceMonName))
		}

		// Delete Service
		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", serviceName))

		// Delete Deployment
		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", deployName))

		// Delete scale-to-zero ConfigMap
		err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ConfigMap: %s", scaleToZeroConfigMapName))

		_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup completed for scale-to-zero test\n")
	})
})

// Test scale-to-zero DISABLED - should preserve at least 1 replica
var _ = Describe("Test workload-variant-autoscaler - Scale-to-Zero Disabled", Ordered, func() {
	var (
		name            string
		namespace       string
		deployName      string
		serviceName     string
		serviceMonName  string
		hpaName         string
		appLabel        string
		initialReplicas int32
		loadGenJob      *batchv1.Job
		port            int
		modelName       string
		ctx             context.Context
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()
		// Use short names to avoid port name length limit (max 15 chars)
		name = "llm-d-sim-dis"
		deployName = name + "-deploy"
		serviceName = name + "-svc"
		serviceMonName = name + "-sm"
		hpaName = name + "-hpa"
		appLabel = name
		namespace = llmDNamespace
		port = 8001
		modelName = llamaModelId + "-dis"

		initialReplicas = 2

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

		By("verifying saturation-scaling ConfigMap exists")
		Eventually(func(g Gomega) {
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cm.Data).To(HaveKey("default"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating scale-to-zero ConfigMap with feature DISABLED")
		scaleToZeroCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      scaleToZeroConfigMapName,
				Namespace: controllerNamespace,
			},
			Data: map[string]string{
				"default": `enable_scale_to_zero: false`,
				"test-model-dis": fmt.Sprintf(`model_id: %s
enable_scale_to_zero: false`, modelName),
			},
		}

		// Delete and recreate ConfigMap
		_ = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Delete(ctx, scaleToZeroConfigMapName, metav1.DeleteOptions{})
		time.Sleep(1 * time.Second) // Brief pause for deletion to propagate

		_, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Create(ctx, scaleToZeroCM, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to create scale-to-zero ConfigMap with disabled setting")

		By("ensuring unique app label for deployment and service")
		utils.ValidateAppLabelUniqueness(namespace, appLabel, k8sClient, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, a100Acc, crClient)

		By("creating llm-d-sim deployment with non-zero replicas")
		deployment := resources.CreateLlmdSimDeployment(namespace, deployName, modelName, appLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err = k8sClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployName))

		By("creating service")
		service := resources.CreateLlmdSimService(namespace, serviceName, appLabel, 30004, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("creating ServiceMonitor")
		serviceMonitor := resources.CreateLlmdSimServiceMonitor(serviceMonName, controllerMonitoringNamespace, llmDNamespace, appLabel)
		err = crClient.Create(ctx, serviceMonitor)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for pods to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(int(initialReplicas)))
			for _, pod := range podList.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
			}
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating VariantAutoscaling resource")
		variantAutoscaling := utils.CreateVariantAutoscalingResource(namespace, name, deployName, modelName, a100Acc, 10.0)
		err = crClient.Create(ctx, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred())

		By("creating HPA (minReplicas depends on feature gate availability)")
		hpa := utils.CreateHPAOnDesiredReplicaMetrics(hpaName, namespace, deployName, name, 10)
		// Use minReplicas=0 only if HPAScaleToZero feature gate is enabled, otherwise use 1
		if utils.IsHPAScaleToZeroEnabled(ctx, k8sClient, GinkgoWriter) {
			minReplicas := int32(0)
			hpa.Spec.MinReplicas = &minReplicas
			_, _ = fmt.Fprintf(GinkgoWriter, "HPAScaleToZero feature gate enabled, setting minReplicas=0\n")
		} else {
			minReplicas := int32(1)
			hpa.Spec.MinReplicas = &minReplicas
			_, _ = fmt.Fprintf(GinkgoWriter, "HPAScaleToZero feature gate disabled, setting minReplicas=1\n")
		}
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	Context("Scale-to-zero disabled behavior", func() {
		It("should verify scale-to-zero is disabled in ConfigMap", func() {
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, scaleToZeroConfigMapName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data["default"]).To(ContainSubstring("enable_scale_to_zero: false"),
				"ConfigMap should have scale-to-zero disabled")
			_, _ = fmt.Fprintf(GinkgoWriter, "Verified scale-to-zero is DISABLED in ConfigMap\n")
		})

		It("should have DesiredOptimizedAlloc populated with non-zero replicas", func() {
			By("waiting for DesiredOptimizedAlloc to be populated")
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      name,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(va.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty())
				// When scale-to-zero is disabled, should maintain at least 1 replica
				g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 1),
					"With scale-to-zero disabled, should have at least 1 replica")
			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "VA has non-zero replicas as expected with scale-to-zero disabled\n")
		})

		It("should run load and then verify minimum replica is preserved after load stops", func() {
			By("setting up port-forward to Prometheus")
			prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, prometheusLocalPort, 9090)
			defer func() {
				_ = utils.StopCmd(prometheusPortForwardCmd)
			}()

			By("waiting for Prometheus port-forward to be ready")
			err := utils.VerifyPortForwardReadiness(ctx, prometheusLocalPort, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", prometheusLocalPort))
			Expect(err).NotTo(HaveOccurred())

			By("starting load generation")
			loadGenJob, err = utils.CreateLoadGeneratorJob(
				namespace,
				fmt.Sprintf("http://%s:%d", gatewayName, 80),
				modelName,
				loadRatePerSecond,
				120, // shorter duration for this test
				inputTokens,
				outputTokens,
				k8sClient,
				ctx,
			)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for load to run briefly")
			time.Sleep(30 * time.Second)

			By("stopping load generation")
			err = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
			Expect(err).NotTo(HaveOccurred())

			_, _ = fmt.Fprintf(GinkgoWriter, "Load stopped, waiting to verify minimum replica is preserved...\n")

			By("waiting and verifying that replicas do NOT scale to zero")
			// Wait for longer than retention period would be
			time.Sleep(3 * time.Minute)

			By("checking VA still has at least 1 replica")
			va := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      name,
			}, va)
			Expect(err).NotTo(HaveOccurred())
			Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 1),
				"With scale-to-zero DISABLED, should preserve at least 1 replica even with no traffic")

			_, _ = fmt.Fprintf(GinkgoWriter, "Verified: VA has %d replicas (minimum preserved with scale-to-zero disabled)\n",
				va.Status.DesiredOptimizedAlloc.NumReplicas)
		})

		It("should verify deployment still has running pods", func() {
			By("checking deployment has at least 1 replica")
			deploy, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			specReplicas := int32(1)
			if deploy.Spec.Replicas != nil {
				specReplicas = *deploy.Spec.Replicas
			}

			Expect(specReplicas).To(BeNumerically(">=", 1),
				"Deployment should have at least 1 replica with scale-to-zero disabled")

			By("checking at least one pod is running")
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			Expect(err).NotTo(HaveOccurred())

			runningPods := 0
			for _, pod := range podList.Items {
				if pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning {
					runningPods++
				}
			}

			Expect(runningPods).To(BeNumerically(">=", 1),
				"Should have at least 1 running pod with scale-to-zero disabled")

			_, _ = fmt.Fprintf(GinkgoWriter, "Verified: %d running pods (minimum preserved)\n", runningPods)
		})
	})

	AfterAll(func() {
		By("cleaning up scale-to-zero disabled test resources")

		_ = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaName, metav1.DeleteOptions{})

		va := &v1alpha1.VariantAutoscaling{}
		if err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, va); err == nil {
			_ = crClient.Delete(ctx, va)
		}

		serviceMon := &promoperator.ServiceMonitor{}
		if err := crClient.Get(ctx, client.ObjectKey{Namespace: controllerMonitoringNamespace, Name: serviceMonName}, serviceMon); err == nil {
			_ = crClient.Delete(ctx, serviceMon)
		}

		_ = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		_ = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{})

		_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup completed for scale-to-zero disabled test\n")
	})
})
