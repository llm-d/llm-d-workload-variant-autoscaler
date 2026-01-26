package e2esaturation

import (
	"context"
	"fmt"
	"os"
	"time"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils/resources"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Limiter test constants - matching saturation test patterns
const (
	// GPU configurations - each node has 4 GPUs
	gpusPerReplicaLimiter = 2 // 2 GPUs/replica, max 2 replicas on 4-GPU node

	// Min GPUs per specific GPU type (one node with 4 GPUs)
	minRequiredGPUsPerType = 4
)

// getGPUResourceName returns the GPU resource name based on E2E_GPU_TYPE env var.
func getGPUResourceName() corev1.ResourceName {
	gpuType := os.Getenv("E2E_GPU_TYPE")
	if gpuType == "" {
		gpuType = "nvidia"
	}
	return corev1.ResourceName(gpuType + ".com/gpu")
}

// getGPUNodeSelector returns node selector for targeting specific GPU type.
// For nvidia (nvidia-mix cluster): targets H100 nodes
// For amd (amd-mix cluster): targets MI300X nodes
func getGPUNodeSelector() (map[string]string, string) {
	gpuType := os.Getenv("E2E_GPU_TYPE")
	if gpuType == "" {
		gpuType = "nvidia"
	}

	if gpuType == "nvidia" {
		return map[string]string{"gpu-config": "4H100"}, "H100"
	}
	return map[string]string{"gpu-config": "4MI300X"}, "MI300X"
}

var _ = Describe("Test workload-variant-autoscaler - GPU Limiter Feature", Ordered, func() {
	var (
		ctx context.Context

		// Resource names - following saturation test pattern
		name           string
		deployName     string
		serviceName    string
		serviceMonName string
		hpaName        string
		appLabel       string
		namespace      string

		initialReplicas int32
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()

		// Use similar naming to saturation test
		name = "llm-d-sim-limiter"
		deployName = name + "-deployment"
		serviceName = name + "-service"
		serviceMonName = name + "-servicemonitor"
		hpaName = name + "-hpa"
		appLabel = name
		namespace = llmDNamespace

		initialReplicas = 1

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

		gpuResourceName := getGPUResourceName()
		gpuType := os.Getenv("E2E_GPU_TYPE")
		if gpuType == "" {
			gpuType = "nvidia"
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "GPU type for this test run: %s (resource: %s)\n", gpuType, gpuResourceName)

		By(fmt.Sprintf("checking cluster has sufficient %s GPUs", gpuType))
		nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		totalGPUs := int64(0)
		for _, node := range nodes.Items {
			if gpuQty, ok := node.Status.Allocatable[gpuResourceName]; ok {
				totalGPUs += gpuQty.Value()
			}
		}
		if totalGPUs < minRequiredGPUsPerType {
			Skip(fmt.Sprintf("Cluster has only %d %s GPUs, need at least %d. Run: ./deploy/kind-emulator/setup.sh -t %s-mix -g 4",
				totalGPUs, gpuType, minRequiredGPUsPerType, gpuType))
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Cluster has %d %s GPUs (minimum required: %d)\n", totalGPUs, gpuType, minRequiredGPUsPerType)

		By("verifying saturation-scaling ConfigMap exists")
		Eventually(func(g Gomega) {
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("saturation ConfigMap %s should exist in namespace %s", saturationConfigMapName, controllerNamespace))
			g.Expect(cm.Data).To(HaveKey("default"), "saturation ConfigMap should have 'default' configuration")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("enabling limiter in saturation-scaling ConfigMap")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		cm.Data["default"] = `kvCacheThreshold: 0.80
queueLengthThreshold: 5
kvSpareTrigger: 0.1
queueSpareTrigger: 3
enableLimiter: true`
		_, err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Update(ctx, cm, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to update saturation ConfigMap to enable limiter")

		By("ensuring unique app label for deployment")
		utils.ValidateAppLabelUniqueness(namespace, appLabel, k8sClient, crClient)

		// Get node selector and accelerator type for GPU targeting
		nodeSelector, accelerator := getGPUNodeSelector()
		_, _ = fmt.Fprintf(GinkgoWriter, "Targeting nodes with selector: %v (accelerator: %s)\n", nodeSelector, accelerator)

		By("creating deployment with GPU resources and node selector")
		deployment := resources.CreateLlmdSimDeploymentWithGPUAndNodeSelector(
			namespace, deployName, llamaModelId, appLabel, "8000",
			avgTTFT, avgITL, initialReplicas, gpusPerReplicaLimiter, gpuType,
			nodeSelector,
		)
		_, err = k8sClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployName))
		_, _ = fmt.Fprintf(GinkgoWriter, "Deployment created with %d GPUs/replica and node selector: %v\n", gpusPerReplicaLimiter, nodeSelector)

		By("creating service to expose deployment")
		service := resources.CreateLlmdSimService(namespace, serviceName, appLabel, 30020, 8000)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Service: %s", serviceName))

		By("creating ServiceMonitor for metrics collection")
		serviceMonitor := resources.CreateLlmdSimServiceMonitor(serviceMonName, controllerMonitoringNamespace, llmDNamespace, appLabel)
		err = crClient.Create(ctx, serviceMonitor)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create ServiceMonitor: %s", serviceMonName))

		By("waiting for pod to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(podList.Items)).To(BeNumerically(">=", 1))
			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating VariantAutoscaling resource")
		va := utils.CreateVariantAutoscalingResource(namespace, deployName, llamaModelId, accelerator, 30.0)
		err = crClient.Create(ctx, va)
		Expect(err).NotTo(HaveOccurred())
		_, _ = fmt.Fprintf(GinkgoWriter, "VariantAutoscaling created with accelerator: %s\n", accelerator)

		By("creating HPA for deployment")
		hpa := utils.CreateHPAOnDesiredReplicaMetrics(hpaName, namespace, deployName, deployName, 10)
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("waiting for DesiredOptimizedAlloc to be populated")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deployName}, va)
			g.Expect(err).NotTo(HaveOccurred())
			// Accelerator is populated from VA label even without Prometheus metrics
			g.Expect(va.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
				"VariantAutoscaling DesiredOptimizedAlloc should be populated")
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
		_, _ = fmt.Fprintf(GinkgoWriter, "DesiredOptimizedAlloc populated - optimization loop is active\n")
	})

	AfterAll(func() {
		if k8sClient == nil {
			return
		}

		By("cleaning up test resources")
		ctx := context.Background()

		// Delete VA
		va := &v1alpha1.VariantAutoscaling{}
		if err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deployName}, va); err == nil {
			_ = crClient.Delete(ctx, va)
		}

		// Delete HPA
		_ = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaName, metav1.DeleteOptions{})

		// Delete ServiceMonitor
		sm := &promoperator.ServiceMonitor{}
		if err := crClient.Get(ctx, client.ObjectKey{Namespace: controllerMonitoringNamespace, Name: serviceMonName}, sm); err == nil {
			_ = crClient.Delete(ctx, sm)
		}

		// Delete Service
		_ = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})

		// Delete Deployment
		_ = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{})

		_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup completed for GPU Limiter E2E test\n")
	})

	Context("Scenario 1: Limiter enabled and configured", func() {
		It("should have limiter enabled in ConfigMap", func() {
			By("verifying limiter is enabled in ConfigMap")
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data["default"]).To(ContainSubstring("enableLimiter: true"))
			_, _ = fmt.Fprintf(GinkgoWriter, "Limiter is enabled in ConfigMap\n")
		})

		It("should have deployment with GPU resources and node selector", func() {
			By("verifying deployment has GPU resource requests")
			gpuResName := getGPUResourceName()
			nodeSelector, _ := getGPUNodeSelector()

			deploy, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			gpuQty := deploy.Spec.Template.Spec.Containers[0].Resources.Requests[gpuResName]
			Expect(gpuQty.Value()).To(Equal(int64(gpusPerReplicaLimiter)),
				fmt.Sprintf("Deployment should request %d GPUs per replica", gpusPerReplicaLimiter))

			Expect(deploy.Spec.Template.Spec.NodeSelector).To(Equal(nodeSelector),
				"Deployment should have correct node selector")

			_, _ = fmt.Fprintf(GinkgoWriter, "Deployment verified: %d GPUs/replica, node selector: %v\n",
				gpusPerReplicaLimiter, nodeSelector)
		})
	})

	Context("Scenario 2: Scale-up under load with limiter constraint", func() {
		It("should scale up when saturation is detected but be constrained by GPU capacity", func() {
			// Following saturation test pattern exactly
			By("setting up port-forward to Prometheus service")
			prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, prometheusLocalPort, 9090)
			defer func() {
				err := utils.StopCmd(prometheusPortForwardCmd)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
			}()

			By("waiting for Prometheus port-forward to be ready")
			err := utils.VerifyPortForwardReadiness(ctx, prometheusLocalPort, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", prometheusLocalPort))
			Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

			By("waiting for Prometheus to scrape saturation metrics from llm-d-sim pods")
			err = utils.WaitForSaturationMetrics(ctx, prometheusLocalPort, namespace, llamaModelId, 5*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "Prometheus should scrape saturation metrics within timeout")

			By("starting load generation to trigger saturation")
			loadGenJob, err := utils.CreateLoadGeneratorJob(
				namespace,
				fmt.Sprintf("http://%s:%d", gatewayName, 80),
				llamaModelId,
				loadRatePerSecond,
				maxExecutionTimeSec,
				inputTokens,
				outputTokens,
				k8sClient,
				ctx,
			)
			Expect(err).NotTo(HaveOccurred(), "Should be able to start load generator")
			_, _ = fmt.Fprintf(GinkgoWriter, "Load generator job created: %s\n", loadGenJob.Name)

			defer func() {
				By("stopping load generation job")
				err = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop load generator")
			}()

			By("waiting for load generator to be ready (pod running + pip install complete)")
			err = utils.WaitForLoadGeneratorReady(ctx, loadGenJob, k8sClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Load generator should become ready")

			_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running\n")

			// Calculate max replicas based on GPU capacity
			// Node has 4 GPUs, with 2 GPUs/replica, max is 2 replicas
			maxReplicasOnNode := 4 / gpusPerReplicaLimiter
			_, _ = fmt.Fprintf(GinkgoWriter, "With %d GPUs/replica and 4 GPUs on target node, max replicas = %d\n",
				gpusPerReplicaLimiter, maxReplicasOnNode)

			By("waiting for saturation detection and scale-up decision")
			var finalReplicas int
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				finalReplicas = va.Status.DesiredOptimizedAlloc.NumReplicas
				accelerator := va.Status.DesiredOptimizedAlloc.Accelerator

				_, _ = fmt.Fprintf(GinkgoWriter, "DesiredOptimizedAlloc: NumReplicas=%d, Accelerator=%s (max should be %d due to GPU limit)\n",
					finalReplicas, accelerator, maxReplicasOnNode)

				// Verify metrics are flowing
				g.Expect(accelerator).NotTo(BeEmpty(),
					"DesiredOptimizedAlloc.Accelerator should be populated when metrics are flowing")

				// Should scale up from initial 1 replica due to saturation
				g.Expect(finalReplicas).To(BeNumerically(">", int(initialReplicas)),
					fmt.Sprintf("Should scale up from %d under load", initialReplicas))

				// Limiter should cap the scale-up at max replicas for the GPU type
				g.Expect(finalReplicas).To(BeNumerically("<=", maxReplicasOnNode),
					fmt.Sprintf("Limiter should cap replicas at %d (%d GPUs / %d GPUs per replica)",
						maxReplicasOnNode, 4, gpusPerReplicaLimiter))

			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			By("logging VariantAutoscaling status after scale-up")
			err = utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status")

			_, _ = fmt.Fprintf(GinkgoWriter, "Limiter successfully constrained scale-up: final replicas = %d (max = %d)\n",
				finalReplicas, maxReplicasOnNode)
		})
	})
})
