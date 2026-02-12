package e2esaturation

import (
	"context"
	"fmt"
	"os"
	"time"

	v1alpha1 "github.com/llm-d/llm-d-workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/test/utils"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/test/utils/resources"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Limiter test constants
const (
	// GPU configurations - use 2 GPUs/replica so max 2 replicas on 4-GPU node
	gpusPerReplicaLimiter = 2

	// Min GPUs per specific GPU type (one node with 4 GPUs)
	minRequiredGPUsPerType = 4

	// Higher load rate to ensure saturation requests scale beyond GPU limits
	limiterLoadRatePerSecond = 30 // 2x higher to ensure saturation triggers scale-up
	limiterInputTokens       = 128
	limiterOutputTokens      = 128
	limiterMaxExecutionTime  = 600

	// Model identifier
	limiterModel1 = "unsloth/Meta-Llama-3.1-8B"
)

// Note: controllerNamespace, controllerMonitoringNamespace, and saturationConfigMapName
// are defined in e2e_saturation_test.go and are accessible in this package.

// getGPUResourceName returns the GPU resource name based on E2E_GPU_TYPE env var.
func getGPUResourceName() corev1.ResourceName {
	gpuType := os.Getenv("E2E_GPU_TYPE")
	if gpuType == "" {
		gpuType = "nvidia"
	}
	return corev1.ResourceName(gpuType + ".com/gpu")
}

// getGPUType returns the GPU type string based on E2E_GPU_TYPE env var.
func getGPUType() string {
	gpuType := os.Getenv("E2E_GPU_TYPE")
	if gpuType == "" {
		gpuType = "nvidia"
	}
	return gpuType
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

// countGPUsOnNodeWithSelector counts available GPUs on nodes matching the selector.
func countGPUsOnNodeWithSelector(ctx context.Context, selector map[string]string) (int64, error) {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}

	gpuResourceName := getGPUResourceName()
	var totalGPUs int64

	for _, node := range nodes.Items {
		// Check if node matches selector
		matches := true
		for k, v := range selector {
			if node.Labels[k] != v {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}

		if gpuQty, ok := node.Status.Allocatable[gpuResourceName]; ok {
			totalGPUs += gpuQty.Value()
		}
	}

	return totalGPUs, nil
}

var _ = Describe("Test workload-variant-autoscaler - GPU Limiter Feature", Ordered, func() {
	var (
		ctx context.Context

		// Resource names
		name           string
		deployName     string
		serviceName    string
		serviceMonName string
		hpaName        string
		appLabel       string
		namespace      string

		initialReplicas int32

		// GPU capacity on target node
		gpusOnTargetNode  int64
		maxReplicasOnNode int
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

		gpuType := getGPUType()
		gpuResourceName := getGPUResourceName()
		_, _ = fmt.Fprintf(GinkgoWriter, "GPU type for this test run: %s (resource: %s)\n", gpuType, gpuResourceName)

		// Get node selector and count GPUs on target node
		nodeSelector, accelerator := getGPUNodeSelector()
		var err error
		gpusOnTargetNode, err = countGPUsOnNodeWithSelector(ctx, nodeSelector)
		Expect(err).NotTo(HaveOccurred())

		if gpusOnTargetNode < int64(minRequiredGPUsPerType) {
			Skip(fmt.Sprintf("Target node with selector %v has only %d GPUs, need at least %d. Run: ./deploy/kind-emulator/setup.sh -t %s-mix -g 4",
				nodeSelector, gpusOnTargetNode, minRequiredGPUsPerType, gpuType))
		}

		// Calculate max replicas based on actual GPU capacity
		maxReplicasOnNode = int(gpusOnTargetNode) / gpusPerReplicaLimiter
		_, _ = fmt.Fprintf(GinkgoWriter, "Target node (%v) has %d %s GPUs, accelerator=%s\n",
			nodeSelector, gpusOnTargetNode, gpuType, accelerator)
		_, _ = fmt.Fprintf(GinkgoWriter, "With %d GPUs/replica, max replicas on target node = %d\n",
			gpusPerReplicaLimiter, maxReplicasOnNode)

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

		By("restarting controller-manager pods to load limiter configuration")
		podList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
		})
		Expect(err).NotTo(HaveOccurred(), "Should be able to list manager pods")

		for _, pod := range podList.Items {
			err = k8sClient.CoreV1().Pods(controllerNamespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete pod %s", pod.Name))
		}

		// Wait for new controller pods to be running
		Eventually(func(g Gomega) {
			newPodList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list manager pods")
			g.Expect(newPodList.Items).NotTo(BeEmpty(), "Pod list should not be empty")
			for _, pod := range newPodList.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
			}
		}, 2*time.Minute, 1*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Controller pods restarted with limiter enabled\n")

		By("ensuring unique app label for deployment")
		utils.ValidateAppLabelUniqueness(namespace, appLabel, k8sClient, crClient)

		By("creating deployment with GPU resources and node selector")
		deployment := resources.CreateLlmdSimDeploymentWithGPUAndNodeSelector(
			namespace, deployName, limiterModel1, appLabel, "8000",
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
		va := utils.CreateVariantAutoscalingResource(namespace, name, deployName, llamaModelId, accelerator, 30.0)
		err = crClient.Create(ctx, va)
		Expect(err).NotTo(HaveOccurred())
		_, _ = fmt.Fprintf(GinkgoWriter, "VariantAutoscaling created with accelerator: %s\n", accelerator)

		By("creating HPA for deployment")
		hpa := utils.CreateHPAOnDesiredReplicaMetrics(hpaName, namespace, deployName, name, 10)
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

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

	AfterAll(func() {
		if k8sClient == nil {
			return
		}

		By("cleaning up test resources")
		ctx := context.Background()

		// Delete VA
		va := &v1alpha1.VariantAutoscaling{}
		if err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, va); err == nil {
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

	Context("Scenario 1: Limiter configuration verification", func() {
		It("should have limiter enabled in ConfigMap", func() {
			By("verifying limiter is enabled in ConfigMap")
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data["default"]).To(ContainSubstring("enableLimiter: true"))
			_, _ = fmt.Fprintf(GinkgoWriter, "Limiter is enabled in ConfigMap\n")
		})

		It("should have deployment with correct GPU resources and node selector", func() {
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

		It("should have detected correct GPU capacity on target node", func() {
			By("verifying GPU capacity calculation")
			nodeSelector, accelerator := getGPUNodeSelector()

			// Verify the capacity we detected matches expectations
			_, _ = fmt.Fprintf(GinkgoWriter, "GPU capacity verification:\n")
			_, _ = fmt.Fprintf(GinkgoWriter, "  - Target node selector: %v\n", nodeSelector)
			_, _ = fmt.Fprintf(GinkgoWriter, "  - Accelerator type: %s\n", accelerator)
			_, _ = fmt.Fprintf(GinkgoWriter, "  - GPUs on target node: %d\n", gpusOnTargetNode)
			_, _ = fmt.Fprintf(GinkgoWriter, "  - GPUs per replica: %d\n", gpusPerReplicaLimiter)
			_, _ = fmt.Fprintf(GinkgoWriter, "  - Max replicas (GPU limited): %d\n", maxReplicasOnNode)

			Expect(gpusOnTargetNode).To(BeNumerically(">=", int64(minRequiredGPUsPerType)),
				"Target node should have sufficient GPUs")
			Expect(maxReplicasOnNode).To(BeNumerically(">=", 1),
				"Should be able to run at least 1 replica")
		})
	})

	Context("Scenario 2: Scale-up under load with limiter constraint", func() {
		It("should scale up when saturation is detected but be constrained by GPU capacity", func() {
			By("setting up port-forward to Prometheus service")
			prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, prometheusLocalPort, 9090)
			defer func() {
				err := utils.StopCmd(prometheusPortForwardCmd)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
			}()

			By("waiting for Prometheus port-forward to be ready")
			err := utils.VerifyPortForwardReadiness(ctx, prometheusLocalPort, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", prometheusLocalPort))
			Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

			By("starting HIGH load generation to trigger scale-up beyond GPU capacity")
			// Use higher load rate to ensure saturation analysis wants more replicas than available
			loadGenJob, err := utils.CreateLoadGeneratorJob(
				namespace,
				fmt.Sprintf("http://%s:%d", gatewayName, 80),
				limiterModel1,
				limiterLoadRatePerSecond, // Higher load rate
				limiterMaxExecutionTime,
				limiterInputTokens,
				limiterOutputTokens,
				k8sClient,
				ctx,
			)
			Expect(err).NotTo(HaveOccurred(), "Should be able to start load generator")
			_, _ = fmt.Fprintf(GinkgoWriter, "Load generator job created: %s (rate: %d req/s)\n",
				loadGenJob.Name, limiterLoadRatePerSecond)

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

			By("waiting for saturation detection and verifying limiter constraint")
			var desiredReplicas int

			// First, wait for scale-up to occur (proves saturation was detected)
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      name,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				desiredReplicas = va.Status.DesiredOptimizedAlloc.NumReplicas
				accelerator := va.Status.DesiredOptimizedAlloc.Accelerator

				_, _ = fmt.Fprintf(GinkgoWriter, "DesiredOptimizedAlloc: NumReplicas=%d, Accelerator=%s\n",
					desiredReplicas, accelerator)

				// Verify metrics are flowing
				g.Expect(accelerator).NotTo(BeEmpty(),
					"DesiredOptimizedAlloc.Accelerator should be populated when metrics are flowing")

				// Should scale up from initial 1 replica due to saturation
				g.Expect(desiredReplicas).To(BeNumerically(">", int(initialReplicas)),
					fmt.Sprintf("Should scale up from %d under load", initialReplicas))

			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			By("verifying scale-up is constrained by GPU capacity via limiter")
			Consistently(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      name,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				desiredReplicas = va.Status.DesiredOptimizedAlloc.NumReplicas
				_, _ = fmt.Fprintf(GinkgoWriter, "Checking DesiredOptimizedAlloc.NumReplicas=%d against max=%d\n",
					desiredReplicas, maxReplicasOnNode)

				// Final replicas should not exceed max allowed by GPU capacity
				g.Expect(desiredReplicas).To(BeNumerically("<=", maxReplicasOnNode),
					fmt.Sprintf("Final replicas %d should be less than or equal to max %d due to GPU limiter",
						desiredReplicas, maxReplicasOnNode))

			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			By("logging VariantAutoscaling status after scale-up")
			err = utils.LogVariantAutoscalingStatus(ctx, name, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status")

			_, _ = fmt.Fprintf(GinkgoWriter, "Limiter successfully constrained scale-up: desiredReplicas=%d (GPU max=%d)\n",
				desiredReplicas, maxReplicasOnNode)
		})
	})

	Context("Scenario 3: Limiter respects GPU type boundaries", func() {
		It("should only use GPUs from the correct accelerator type", func() {
			By("verifying deployment pods are scheduled on nodes with correct GPU type")
			nodeSelector, accelerator := getGPUNodeSelector()

			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(podList.Items)).To(BeNumerically(">=", 1), "Should have at least one pod")

			for _, pod := range podList.Items {
				if pod.Status.Phase != corev1.PodRunning {
					continue
				}

				nodeName := pod.Spec.NodeName
				node, err := k8sClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())

				// Verify pod is on a node matching the selector
				for k, v := range nodeSelector {
					Expect(node.Labels[k]).To(Equal(v),
						fmt.Sprintf("Pod %s should be on node with label %s=%s, but node %s has %s=%s",
							pod.Name, k, v, nodeName, k, node.Labels[k]))
				}

				_, _ = fmt.Fprintf(GinkgoWriter, "Pod %s is correctly scheduled on node %s (accelerator: %s)\n",
					pod.Name, nodeName, accelerator)
			}
		})
	})
})
