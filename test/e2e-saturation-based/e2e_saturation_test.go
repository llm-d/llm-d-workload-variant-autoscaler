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
	"strconv"
	"time"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils/resources"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Saturation-based mode workload test constants
const (
	loadRatePerSecond   = 8   // requests per second
	avgITL              = 10  // ms
	avgTTFT             = 150 // ms
	inputTokens         = 128
	outputTokens        = 128
	maxExecutionTimeSec = 600

	KvCacheThreshold     = 0.7  // 70% of KV-cache blocks currently in use
	QueueLengthThreshold = 10.0 // 10 requests in queue
	kvSpareTrigger       = 0.1
	queueSpareTrigger    = 2.0
)

// Kubernetes resource constants
const (
	controllerNamespace           = "workload-variant-autoscaler-system"
	controllerMonitoringNamespace = "workload-variant-autoscaler-monitoring"
	llmDNamespace                 = "llm-d-sim"
	gatewayName                   = "infra-sim-inference-gateway-istio"
	WVAConfigMapName              = "workload-variant-autoscaler-variantautoscaling-config"
	saturationConfigMapName       = "workload-variant-autoscaler-saturation-scaling-config"
)

// Variant and Model constants
const (
	llamaModelId = "unsloth/Meta-Llama-3.1-8B"
	a100Acc      = "A100"
	h100Acc      = "H100"

	// Second variant with different accelerator (for cost-based testing)
	h100Cost = 50.0
	a100Cost = 30.0

	prometheusLocalPort = 19090
)

var (
	k8sClient *kubernetes.Clientset
	crClient  client.Client
	scheme    = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(promoperator.AddToScheme(scheme))
}

// initializeK8sClient initializes the Kubernetes client for testing
func initializeK8sClient() {
	cfg, err := func() (*rest.Config, error) {
		if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
			return clientcmd.BuildConfigFromFlags("", kubeconfig)
		}
		return rest.InClusterConfig()
	}()
	if err != nil {
		Skip("failed to load kubeconfig: " + err.Error())
	}

	// Suppress warnings to avoid spam in test output
	cfg.WarningHandler = rest.NoWarnings{}

	k8sClient, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		Skip("failed to create kubernetes client: " + err.Error())
	}

	// Initialize controller-runtime client for custom resources
	crClient, err = client.New(cfg, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		Skip("failed to create controller-runtime client: " + err.Error())
	}
}

var _ = Describe("Test workload-variant-autoscaler - Saturation Mode - Single VariantAutoscaling", Ordered, func() {
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
		name = "llm-d-sim"
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

		By("ensuring unique app label for deployment and service")
		utils.ValidateAppLabelUniqueness(namespace, appLabel, k8sClient, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, a100Acc, crClient)

		By("creating llm-d-sim deployment")
		deployment := resources.CreateLlmdSimDeployment(namespace, deployName, modelName, appLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err := k8sClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployName))

		By("creating service to expose llm-d-sim deployment")
		service := resources.CreateLlmdSimService(namespace, serviceName, appLabel, 30003, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Service: %s", serviceName))

		By("creating ServiceMonitor for vLLM metrics")
		serviceMonitor := resources.CreateLlmdSimServiceMonitor(serviceMonName, controllerMonitoringNamespace, llmDNamespace, appLabel)
		err = crClient.Create(ctx, serviceMonitor)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create ServiceMonitor: %s", serviceMonName))

		By("waiting for pods to be running before creating VariantAutoscaling")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(int(initialReplicas)))
			for _, pod := range podList.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
			}
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating VariantAutoscaling resource")
		variantAutoscaling := utils.CreateVariantAutoscalingResource(namespace, deployName, modelName, a100Acc, 10.0)
		err = crClient.Create(ctx, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create VariantAutoscaling for: %s", deployName))

		By("creating HorizontalPodAutoscaler for deployment")
		hpa := utils.CreateHPAOnDesiredReplicaMetrics(hpaName, namespace, deployName, deployName, 10)
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create HPA: %s", hpaName))
	})

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
			Expect(defaultConfig).To(ContainSubstring("kvSpareTrigger"), "Default config should contain kvSpareTrigger")
			Expect(defaultConfig).To(ContainSubstring("queueSpareTrigger"), "Default config should contain queueSpareTrigger")

			_, _ = fmt.Fprintf(GinkgoWriter, "ConfigMap %s verified with default configuration\n", saturationConfigMapName)
		})

		It("should have VariantAutoscaling resource created", func() {
			By("verifying VariantAutoscaling exists")
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, va)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling: %s", deployName))
			Expect(va.Spec.ModelID).To(Equal(modelName))

			_, _ = fmt.Fprintf(GinkgoWriter, "VariantAutoscaling resource verified: %s\n", deployName)
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
			Expect(hpa.Spec.Metrics[0].External.Metric.Selector.MatchLabels["variant_name"]).To(Equal(deployName), "HPA metric should filter by variant_name")

			_, _ = fmt.Fprintf(GinkgoWriter, "HPA %s verified and configured correctly\n", hpaName)
		})
	})

	Context("Before load - initial replica count", func() {
		It("should have correct initial replica count before applying load", func() {
			By("waiting for DesiredOptimizedAlloc to be populated")
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))

				// Wait for DesiredOptimizedAlloc to be populated (ensures reconciliation loop is active)
				// Accelerator is populated from VA label even without Prometheus metrics
				g.Expect(va.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
					"DesiredOptimizedAlloc should be populated with accelerator info")
				g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 0),
					"DesiredOptimizedAlloc should have NumReplicas set")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("querying external metrics API")
			Eventually(func(g Gomega) {
				result, err := k8sClient.RESTClient().
					Get().
					AbsPath("/apis/external.metrics.k8s.io/v1beta1/namespaces/" + namespace + "/" + constants.WVADesiredReplicas).
					DoRaw(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to query external metrics API")
				g.Expect(string(result)).To(ContainSubstring(constants.WVADesiredReplicas), "Metric should be available")
				g.Expect(string(result)).To(ContainSubstring(deployName), "Metric should be for the correct variant")
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying variant has expected initial replicas or scales down (before load)")
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				// Initial replica count should be MinimumReplicas (0 or 1)
				// Initial replica count should be MinimumReplicas (0 or 1)
				g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
					fmt.Sprintf("VariantAutoscaling should be at %d replicas", MinimumReplicas))
			}, 10*time.Minute, 5*time.Second).Should(Succeed())

			By("logging VariantAutoscaling status before load")
			err := utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status before load")
		})
	})

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
				loadRatePerSecond,
				maxExecutionTimeSec,
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

			By("waiting for load generator to be ready (pod running + pip install complete)")
			err = utils.WaitForLoadGeneratorReady(ctx, loadGenJob, k8sClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Load generator should become ready")

			_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running\n")

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

				// Should scale up due to saturation
				g.Expect(finalReplicas).To(BeNumerically(">", MinimumReplicas),
					fmt.Sprintf("Should scale up from %d under load", MinimumReplicas))

			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			By("logging VariantAutoscaling status after scale-up")
			err = utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after scale-up")
		})
	})

	Context("Replica stability under constant load", func() {
		It("should maintain stable replica count under constant load", func() {
			By("starting constant load generation")
			loadGenJob, err := utils.CreateLoadGeneratorJob(
				namespace,
				fmt.Sprintf("http://%s:%d", serviceName, port),
				modelName,
				loadRatePerSecond,
				maxExecutionTimeSec,
				inputTokens,
				outputTokens,
				k8sClient,
				ctx,
			)
			Expect(err).NotTo(HaveOccurred(), "Should be able to start constant load generator")

			defer func() {
				By("stopping load generation job")
				err = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop load generator")
			}()

			By("waiting for load generator to be ready (pod running + pip install complete)")
			err = utils.WaitForLoadGeneratorReady(ctx, loadGenJob, k8sClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Load generator should become ready")

			By("recording initial replica count after load stabilizes")
			var initialReplicas int
			Eventually(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				initialReplicas = va.Status.DesiredOptimizedAlloc.NumReplicas

				// Ensure we have some replicas running under load
				g.Expect(initialReplicas).To(BeNumerically(">", 0),
					"Should have replicas running under load")
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "Initial stable state: %d replicas\n", initialReplicas)

			By("verifying replica count remains stable for 3 minutes")
			Consistently(func(g Gomega) {
				va := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployName,
				}, va)
				g.Expect(err).NotTo(HaveOccurred())

				currentReplicas := va.Status.DesiredOptimizedAlloc.NumReplicas

				// Allow small fluctuations (±1 replica) due to metric variance
				g.Expect(currentReplicas).To(BeNumerically("~", initialReplicas, 1),
					fmt.Sprintf("Replicas should remain stable around %d, but got %d", initialReplicas, currentReplicas))

			}, 3*time.Minute, 15*time.Second).Should(Succeed())

			By("logging VariantAutoscaling status after stability check")
			err = utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after stability check")
		})
	})

	AfterAll(func() {
		By("cleaning up test resources")

		// Delete HPA
		err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete HPA: %s", hpaName))

		// Delete VariantAutoscaling resource
		va := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deployName}, va)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling: %s", deployName))
		err = crClient.Delete(ctx, va)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling: %s", deployName))

		// Delete ServiceMonitor
		serviceMon := &promoperator.ServiceMonitor{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: controllerMonitoringNamespace, Name: serviceMonName}, serviceMon)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get ServiceMonitor: %s", serviceMonName))
		err = crClient.Delete(ctx, serviceMon)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", serviceMonName))

		// Delete Service
		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", serviceName))

		// Delete vLLM-sim Deployment
		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", deployName))

		_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup completed for single VA Saturation-based E2E test\n")
	})
})

var _ = Describe("Test workload-variant-autoscaler - Saturation Mode - Multiple VariantAutoscalings", Ordered, func() {
	var (
		nameA100           string
		nameH100           string
		namespace          string
		deployNameA100     string
		deployNameH100     string
		serviceNameA100    string
		serviceNameH100    string
		serviceMonNameA100 string
		serviceMonNameH100 string
		hpaNameA100        string
		hpaNameH100        string
		appLabelA100       string
		appLabelH100       string
		initialReplicas    int32
		loadGenJob         *batchv1.Job
		port               int
		modelName          string
		ctx                context.Context
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()

		// First VariantAutoscaling with A100 accelerator
		nameA100 = "llm-d-sim-a100"
		deployNameA100 = nameA100 + "-deployment"
		serviceNameA100 = nameA100 + "-service"
		serviceMonNameA100 = nameA100 + "-servicemonitor"
		hpaNameA100 = nameA100 + "-hpa"
		appLabelA100 = nameA100

		// Second VariantAutoscaling with H100 accelerator
		nameH100 = "llm-d-sim-h100"
		deployNameH100 = nameH100 + "-deployment"
		serviceNameH100 = nameH100 + "-service"
		serviceMonNameH100 = nameH100 + "-servicemonitor"
		hpaNameH100 = nameH100 + "-hpa"
		appLabelH100 = nameH100

		namespace = llmDNamespace
		port = 8000
		modelName = llamaModelId

		initialReplicas = 2

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

		By("verifying saturation-scaling ConfigMap exists before creating VAs")
		Eventually(func(g Gomega) {
			cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, saturationConfigMapName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("saturation ConfigMap %s should exist in namespace %s", saturationConfigMapName, controllerNamespace))
			g.Expect(cm.Data).To(HaveKey("default"), "saturation ConfigMap should have 'default' configuration")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("ensuring unique app labels for deployments and services")
		utils.ValidateAppLabelUniqueness(namespace, appLabelA100, k8sClient, crClient)
		utils.ValidateAppLabelUniqueness(namespace, appLabelH100, k8sClient, crClient)

		By("ensuring unique VariantAutoscaling configurations")
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, a100Acc, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, h100Acc, crClient)

		// Create first VariantAutoscaling (A100 - cheaper)
		By("creating llm-d-sim deployment for A100 variant")
		deploymentA100 := resources.CreateLlmdSimDeployment(namespace, deployNameA100, modelName, appLabelA100, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err := k8sClient.AppsV1().Deployments(namespace).Create(ctx, deploymentA100, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployNameA100))

		By("creating service to expose A100 variant")
		serviceA100 := resources.CreateLlmdSimService(namespace, serviceNameA100, appLabelA100, 30001, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, serviceA100, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Service: %s", serviceNameA100))

		By("creating ServiceMonitor for A100 variant metrics")
		serviceMonitorA100 := resources.CreateLlmdSimServiceMonitor(serviceMonNameA100, controllerMonitoringNamespace, llmDNamespace, appLabelA100)
		err = crClient.Create(ctx, serviceMonitorA100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create ServiceMonitor: %s", serviceMonNameA100))

		// Create second VariantAutoscaling (H100 - more expensive)
		By("creating llm-d-sim deployment for H100 variant")
		deploymentH100 := resources.CreateLlmdSimDeployment(namespace, deployNameH100, modelName, appLabelH100, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err = k8sClient.AppsV1().Deployments(namespace).Create(ctx, deploymentH100, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployNameH100))

		By("creating service to expose H100 variant")
		serviceH100 := resources.CreateLlmdSimService(namespace, serviceNameH100, appLabelH100, 30002, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, serviceH100, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Service: %s", serviceNameH100))

		By("creating ServiceMonitor for H100 variant metrics")
		serviceMonitorH100 := resources.CreateLlmdSimServiceMonitor(serviceMonNameH100, controllerMonitoringNamespace, llmDNamespace, appLabelH100)
		err = crClient.Create(ctx, serviceMonitorH100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create ServiceMonitor: %s", serviceMonNameH100))

		By("waiting for A100 pod to be running before creating VariantAutoscaling")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabelA100,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(int(initialReplicas)))
			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("waiting for H100 pod to be running before creating VariantAutoscaling")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabelH100,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(int(initialReplicas)))
			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating VariantAutoscaling resource for A100 variant")
		variantAutoscalingA100 := utils.CreateVariantAutoscalingResource(namespace, deployNameA100, modelName, a100Acc, a100Cost)
		err = crClient.Create(ctx, variantAutoscalingA100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create VariantAutoscaling for: %s", deployNameA100))

		By("creating VariantAutoscaling resource for H100 variant")
		variantAutoscalingH100 := utils.CreateVariantAutoscalingResource(namespace, deployNameH100, modelName, h100Acc, h100Cost)
		err = crClient.Create(ctx, variantAutoscalingH100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create VariantAutoscaling for: %s", deployNameH100))

		By("creating HorizontalPodAutoscaler for A100 deployment")
		hpaA100 := utils.CreateHPAOnDesiredReplicaMetrics(hpaNameA100, namespace, deployNameA100, deployNameA100, 10)
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpaA100, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create HPA: %s", hpaNameA100))

		By("creating HorizontalPodAutoscaler for H100 deployment")
		hpaH100 := utils.CreateHPAOnDesiredReplicaMetrics(hpaNameH100, namespace, deployNameH100, deployNameH100, 10)
		_, err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpaH100, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create HPA: %s", hpaNameH100))
	})

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
			Expect(defaultConfig).To(ContainSubstring("kvSpareTrigger"), "Default config should contain kvSpareTrigger")
			Expect(defaultConfig).To(ContainSubstring("queueSpareTrigger"), "Default config should contain queueSpareTrigger")

			_, _ = fmt.Fprintf(GinkgoWriter, "ConfigMap %s verified with default configuration\n", saturationConfigMapName)
		})

		It("should have VariantAutoscaling resources created for both variants", func() {
			By("verifying A100 VariantAutoscaling exists")
			vaA100 := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployNameA100,
			}, vaA100)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling: %s", deployNameA100))
			Expect(vaA100.Spec.ModelID).To(Equal(modelName))

			By("verifying H100 VariantAutoscaling exists")
			vaH100 := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployNameH100,
			}, vaH100)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling: %s", deployNameH100))
			Expect(vaH100.Spec.ModelID).To(Equal(modelName))
		})

		It("should have HPAs created and configured correctly for both variants", func() {
			By("verifying A100 HPA exists and is configured")
			hpaA100, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpaNameA100, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("HPA %s should exist", hpaNameA100))
			Expect(hpaA100.Spec.ScaleTargetRef.Name).To(Equal(deployNameA100), "A100 HPA should target correct deployment")
			Expect(hpaA100.Spec.Metrics).To(HaveLen(1), "A100 HPA should have one metric")
			Expect(hpaA100.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType), "A100 HPA should use external metrics")
			Expect(hpaA100.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.WVADesiredReplicas), "A100 HPA should use wva_desired_replicas")

			By("verifying H100 HPA exists and is configured")
			hpaH100, err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpaNameH100, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("HPA %s should exist", hpaNameH100))
			Expect(hpaH100.Spec.ScaleTargetRef.Name).To(Equal(deployNameH100), "H100 HPA should target correct deployment")
			Expect(hpaH100.Spec.Metrics).To(HaveLen(1), "H100 HPA should have one metric")
			Expect(hpaH100.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType), "H100 HPA should use external metrics")
			Expect(hpaH100.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.WVADesiredReplicas), "H100 HPA should use wva_desired_replicas")

			_, _ = fmt.Fprintf(GinkgoWriter, "Both HPAs verified and configured correctly\n")
		})
	})

	Context("Before load - initial replica count", func() {
		It("should have correct initial replica counts before applying load", func() {
			By("waiting for A100 VariantAutoscaling DesiredOptimizedAlloc to be populated")
			Eventually(func(g Gomega) {
				vaA100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameA100,
				}, vaA100)
				g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployNameA100))

				// Wait for DesiredOptimizedAlloc to be populated (ensures reconciliation loop is active)
				// Accelerator is populated from VA label even without Prometheus metrics
				g.Expect(vaA100.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
					"DesiredOptimizedAlloc should be populated with accelerator info")
				g.Expect(vaA100.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 0),
					"DesiredOptimizedAlloc should have NumReplicas set")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("waiting for H100 VariantAutoscaling DesiredOptimizedAlloc to be populated")
			Eventually(func(g Gomega) {
				vaH100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameH100,
				}, vaH100)
				g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployNameH100))

				// Wait for DesiredOptimizedAlloc to be populated (ensures reconciliation loop is active)
				// Accelerator is populated from VA label even without Prometheus metrics
				g.Expect(vaH100.Status.DesiredOptimizedAlloc.Accelerator).NotTo(BeEmpty(),
					"DesiredOptimizedAlloc should be populated with accelerator info")
				g.Expect(vaH100.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 0),
					"DesiredOptimizedAlloc should have NumReplicas set")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("verifying A100 variant has expected initial replicas or scales down (before load)")
			Eventually(func(g Gomega) {
				vaA100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameA100,
				}, vaA100)
				g.Expect(err).NotTo(HaveOccurred())

				// Initial replica count should be MinimumReplicas (typically 0 or 1)
				// Initial replica count should be MinimumReplicas (typically 0 or 1)
				g.Expect(vaA100.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
					fmt.Sprintf("A100 VariantAutoscaling DesiredReplicas should be at %d replicas", MinimumReplicas))
			}, 10*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying H100 variant has expected initial replicas or scales down (before load)")
			Eventually(func(g Gomega) {
				vaH100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameH100,
				}, vaH100)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(vaH100.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
					fmt.Sprintf("H100 VariantAutoscaling DesiredReplicas should be at %d replicas", MinimumReplicas))
			}, 10*time.Minute, 5*time.Second).Should(Succeed())

			By("logging initial VariantAutoscaling statuses")
			err := utils.LogVariantAutoscalingStatus(ctx, deployNameA100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status before load")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameH100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status before load")
		})
	})

	Context("Scale-up behavior under load", func() {
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
				loadRatePerSecond,
				maxExecutionTimeSec,
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

			By("waiting for load generator to be ready (pod running + pip install complete)")
			err = utils.WaitForLoadGeneratorReady(ctx, loadGenJob, k8sClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Load generator should become ready")

			_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running\n")

			By("waiting for saturation detection and scale-up decision")
			Eventually(func(g Gomega) {
				// Check A100 variant
				vaA100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameA100,
				}, vaA100)
				g.Expect(err).NotTo(HaveOccurred())

				// Check H100 variant
				vaH100 := &v1alpha1.VariantAutoscaling{}
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameH100,
				}, vaH100)
				g.Expect(err).NotTo(HaveOccurred())

				// At least one variant should scale up due to saturation
				totalDesiredReplicas := vaA100.Status.DesiredOptimizedAlloc.NumReplicas +
					vaH100.Status.DesiredOptimizedAlloc.NumReplicas

				g.Expect(totalDesiredReplicas).To(BeNumerically(">", 2),
					fmt.Sprintf("Total desired replicas should increase under load - A100: %d, H100: %d",
						vaA100.Status.DesiredOptimizedAlloc.NumReplicas,
						vaH100.Status.DesiredOptimizedAlloc.NumReplicas))

				// Verify metrics are being collected
				// Verify metrics are being collected
				// arrivalRateA100, err := strconv.ParseFloat(vaA100.Status.CurrentAlloc.Load.ArrivalRate, 64)
				// g.Expect(err).NotTo(HaveOccurred(), "Should parse A100 arrival rate")

				// arrivalRateH100, err := strconv.ParseFloat(vaH100.Status.CurrentAlloc.Load.ArrivalRate, 64)
				// g.Expect(err).NotTo(HaveOccurred(), "Should parse H100 arrival rate")

				// totalArrivalRate := arrivalRateA100 + arrivalRateH100
				// g.Expect(totalArrivalRate).To(BeNumerically(">", 0),
				// 	"Total arrival rate should be positive under load")

			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			By("logging VariantAutoscaling statuses after scale-up")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameA100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after scale-up")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameH100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after scale-up")
		})
	})

	// TODO: This test is flaky because A100 and H100 variants share the same model
	// and get grouped together for cost-based optimization, causing replica shifts
	// between variants even under constant load. Needs redesign with independent variants.
	Context("Replica stability under constant load", func() {
		PIt("should maintain stable replica count under constant load", func() {
			By("starting constant load generation")
			loadGenJob, err := utils.CreateLoadGeneratorJob(
				namespace,
				fmt.Sprintf("http://%s:%d", gatewayName, 80),
				modelName,
				loadRatePerSecond,
				maxExecutionTimeSec,
				inputTokens,
				outputTokens,
				k8sClient,
				ctx,
			)
			Expect(err).NotTo(HaveOccurred(), "Should be able to start constant load generator")

			defer func() {
				By("stopping load generation job")
				err = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
				Expect(err).NotTo(HaveOccurred(), "Should be able to stop load generator")
			}()

			By("waiting for load generator to be ready (pod running + pip install complete)")
			err = utils.WaitForLoadGeneratorReady(ctx, loadGenJob, k8sClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Load generator should become ready")

			By("waiting for autoscaler to respond to load (2 minutes)")
			time.Sleep(2 * time.Minute)

			By("recording initial replica counts")
			var initialA100Replicas, initialH100Replicas int
			Eventually(func(g Gomega) {
				vaA100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameA100,
				}, vaA100)
				g.Expect(err).NotTo(HaveOccurred())

				vaH100 := &v1alpha1.VariantAutoscaling{}
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameH100,
				}, vaH100)
				g.Expect(err).NotTo(HaveOccurred())

				initialA100Replicas = vaA100.Status.DesiredOptimizedAlloc.NumReplicas
				initialH100Replicas = vaH100.Status.DesiredOptimizedAlloc.NumReplicas

				// Ensure we have some replicas running
				g.Expect(initialA100Replicas+initialH100Replicas).To(BeNumerically(">", 0),
					"Should have replicas running under load")
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "Initial stable state: A100=%d, H100=%d replicas\n",
				initialA100Replicas, initialH100Replicas)

			By("verifying replica counts remain stable for 3 minutes")
			Consistently(func(g Gomega) {
				vaA100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameA100,
				}, vaA100)
				g.Expect(err).NotTo(HaveOccurred())

				vaH100 := &v1alpha1.VariantAutoscaling{}
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameH100,
				}, vaH100)
				g.Expect(err).NotTo(HaveOccurred())

				// Allow small fluctuations (±1 replica) due to metric variance
				g.Expect(vaA100.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("~", initialA100Replicas, 1),
					fmt.Sprintf("A100 replicas should remain stable around %d", initialA100Replicas))

				g.Expect(vaH100.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("~", initialH100Replicas, 1),
					fmt.Sprintf("H100 replicas should remain stable around %d", initialH100Replicas))

			}, 3*time.Minute, 15*time.Second).Should(Succeed())

			By("logging VariantAutoscaling statuses after stability check")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameA100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after stability check")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameH100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after stability check")
		})
	})

	Context("Cost-based variant selection", func() {
		It("should prefer cheaper A100 variant for scale-up when both variants have same base model", func() {
			By("starting load that requires scale-up")
			loadGenJob, err := utils.CreateLoadGeneratorJob(

				namespace,
				fmt.Sprintf("http://%s:%d", gatewayName, 80),
				modelName,
				loadRatePerSecond, // High load to trigger scale-up
				maxExecutionTimeSec,
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

			By("waiting for scale-up decisions")
			var finalA100Replicas, finalH100Replicas int
			Eventually(func(g Gomega) {
				vaA100 := &v1alpha1.VariantAutoscaling{}
				err := crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameA100,
				}, vaA100)
				g.Expect(err).NotTo(HaveOccurred())

				vaH100 := &v1alpha1.VariantAutoscaling{}
				err = crClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      deployNameH100,
				}, vaH100)
				g.Expect(err).NotTo(HaveOccurred())

				finalA100Replicas = vaA100.Status.DesiredOptimizedAlloc.NumReplicas
				finalH100Replicas = vaH100.Status.DesiredOptimizedAlloc.NumReplicas

				// Verify total replicas increased
				totalReplicas := finalA100Replicas + finalH100Replicas
				g.Expect(totalReplicas).To(BeNumerically(">", 2),
					"Total replicas should increase under high load")

				// Verify cost information is available
				a100Cost, err := strconv.ParseFloat(vaA100.Spec.VariantCost, 64)
				g.Expect(err).NotTo(HaveOccurred(), "A100 cost should be parseable")

				h100Cost, err := strconv.ParseFloat(vaH100.Spec.VariantCost, 64)
				g.Expect(err).NotTo(HaveOccurred(), "H100 cost should be parseable")

				// In Saturation-based mode with cost-awareness, cheaper variant should get more replicas
				// if both can handle the load equally well
				_, _ = fmt.Fprintf(GinkgoWriter, "Costs: A100=%.2f, H100=%.2f, Replicas: A100=%d, H100=%d\n",
					a100Cost, h100Cost, finalA100Replicas, finalH100Replicas)

				// The cheaper variant (A100) should have at least as many replicas as the expensive one
				// This verifies cost-aware scaling in saturation mode
				if a100Cost < h100Cost {
					g.Expect(finalA100Replicas).To(BeNumerically(">=", finalH100Replicas),
						"Cheaper A100 variant should have at least as many replicas as expensive H100")
				}

			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("logging VariantAutoscaling statuses after cost-based selection check")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameA100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after cost-based selection check")
			err = utils.LogVariantAutoscalingStatus(ctx, deployNameH100, namespace, crClient, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "Should be able to log VariantAutoscaling status after cost-based selection check")
		})
	})

	AfterAll(func() {
		By("cleaning up test resources")

		// Delete HPAs
		err := k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaNameA100, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete HPA: %s", hpaNameA100))

		err = k8sClient.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaNameH100, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete HPA: %s", hpaNameH100))

		// Delete VariantAutoscaling resources
		vaA100 := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deployNameA100}, vaA100)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		err = crClient.Delete(ctx, vaA100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling: %s", deployNameA100))

		vaH100 := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deployNameH100}, vaH100)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		err = crClient.Delete(ctx, vaH100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling: %s", deployNameH100))

		// Delete ServiceMonitors
		serviceMonitorA100 := &promoperator.ServiceMonitor{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: controllerMonitoringNamespace, Name: serviceMonNameA100}, serviceMonitorA100)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get ServiceMonitor: %s", serviceMonNameA100))
		err = crClient.Delete(ctx, serviceMonitorA100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", serviceMonNameA100))

		serviceMonitorH100 := &promoperator.ServiceMonitor{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: controllerMonitoringNamespace, Name: serviceMonNameH100}, serviceMonitorH100)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get ServiceMonitor: %s", serviceMonNameH100))
		err = crClient.Delete(ctx, serviceMonitorH100)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", serviceMonNameH100))

		// Delete Services
		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceNameA100, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", serviceNameA100))

		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceNameH100, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", serviceNameH100))

		// Delete vLLM-sim Deployments
		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployNameA100, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", deployNameA100))

		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployNameH100, metav1.DeleteOptions{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", deployNameH100))

		_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup completed for multiple VAs Saturation-based E2E tests\n")
	})

	// PodScrapingSource tests using real EPP pods from llm-d-sim deployment
	var _ = Describe("PodScrapingSource - Real EPP Pods", Ordered, func() {
		var (
			testInferencePoolName string
			testNamespace         = llmDNamespace
			ctx                   context.Context
		)

		BeforeAll(func() {
			if os.Getenv("KUBECONFIG") == "" {
				Skip("KUBECONFIG is not set; skipping PodScrapingSource test")
			}

			initializeK8sClient()
			ctx = context.Background()

			// Discover existing EPP pods by finding services with "-epp" suffix
			// Prefer the primary EPP (gaie-sim-epp) over secondary EPPs (gaie-sim-2-epp)
			By("discovering existing EPP service")
			serviceList, err := k8sClient.CoreV1().Services(testNamespace).List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to list services")

			// Collect all EPP services and prefer primary (no numeric suffix before -epp)
			var primaryEPP, fallbackEPP string
			for _, svc := range serviceList.Items {
				if len(svc.Name) > 4 && svc.Name[len(svc.Name)-4:] == "-epp" {
					poolName := svc.Name[:len(svc.Name)-4]
					// Check if this is the primary EPP (gaie-sim-epp) vs secondary (gaie-sim-2-epp)
					// Primary EPP doesn't have a digit before "-epp"
					if len(poolName) > 0 && poolName[len(poolName)-1] >= '0' && poolName[len(poolName)-1] <= '9' {
						// This is a secondary EPP (ends with digit like gaie-sim-2)
						if fallbackEPP == "" {
							fallbackEPP = poolName
						}
					} else {
						// This is the primary EPP
						primaryEPP = poolName
					}
					_, _ = fmt.Fprintf(GinkgoWriter, "Found EPP service: %s, InferencePool: %s\n", svc.Name, poolName)
				}
			}
			// Use primary EPP if available, otherwise use fallback
			if primaryEPP != "" {
				testInferencePoolName = primaryEPP
				_, _ = fmt.Fprintf(GinkgoWriter, "Using primary EPP: %s-epp\n", testInferencePoolName)
			} else if fallbackEPP != "" {
				testInferencePoolName = fallbackEPP
				_, _ = fmt.Fprintf(GinkgoWriter, "Using fallback EPP: %s-epp\n", testInferencePoolName)
			}

			if testInferencePoolName == "" {
				Skip("No EPP service found in namespace; skipping PodScrapingSource test. Ensure llm-d-sim is deployed with EPP.")
			}

			// Verify EPP pods exist and are Ready
			By("verifying EPP pods are Ready")
			Eventually(func(g Gomega) {
				eppServiceName := fmt.Sprintf("%s-epp", testInferencePoolName)
				pods, err := utils.FindExistingEPPPods(ctx, k8sClient, testNamespace, eppServiceName)
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to find EPP pods")

				readyCount := 0
				for _, pod := range pods {
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
							readyCount++
							break
						}
					}
				}
				g.Expect(readyCount).To(BeNumerically(">=", 1), "Should have at least one Ready EPP pod")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		// Run shared PodScrapingSource tests with Kind/real EPP configuration
		// Use a closure to capture k8sClient and crClient at runtime
		Context("with real EPP pods", func() {
			var metricsSecretName string

			BeforeAll(func() {
				// Discover or create metrics reader secret
				By("discovering metrics reader secret")
				eppServiceName := fmt.Sprintf("%s-epp", testInferencePoolName)
				var err error
				metricsSecretName, err = utils.DiscoverMetricsReaderSecret(ctx, k8sClient, crClient, testNamespace, eppServiceName)
				Expect(err).NotTo(HaveOccurred(), "Should be able to discover or create metrics secret")
				_, _ = fmt.Fprintf(GinkgoWriter, "Using metrics secret: %s\n", metricsSecretName)
			})

			utils.DescribePodScrapingSourceTests(func() utils.PodScrapingTestConfig {
				return utils.PodScrapingTestConfig{
					Environment:             "kind",
					ServiceName:             fmt.Sprintf("%s-epp", testInferencePoolName),
					ServiceNamespace:        testNamespace,
					MetricsPort:             9090,
					MetricsPath:             "/metrics",
					MetricsScheme:           "http",
					MetricsReaderSecretName: metricsSecretName,
					MetricsReaderSecretKey:  "token",
					K8sClient:               k8sClient,
					CRClient:                crClient,
					Ctx:                     ctx,
				}
			})
		})
	})
})

var _ = Describe("VariantAutoscaling Target Condition", Ordered, func() {
	var (
		namespace   string
		validCtx    context.Context
		invalidCtx  context.Context
		modelName   string
		variantCost float64
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		namespace = "default" // Use default namespace for simplicity
		modelName = "target-condition-model"
		variantCost = 10.0
		validCtx = context.Background()
		invalidCtx = context.Background()
	})

	It("should set TargetResolved=True when target deployment exists", func() {
		name := "valid-target-va"
		deployName := name
		appLabel := name
		port := 8000

		By("creating deployment")
		deployment := resources.CreateLlmdSimDeployment(namespace, deployName, modelName, appLabel, fmt.Sprintf("%d", port), 200, 20, 1)
		_, err := k8sClient.AppsV1().Deployments(namespace).Create(validCtx, deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("creating VariantAutoscaling")
		va := utils.CreateVariantAutoscalingResource(namespace, deployName, modelName, "A100", variantCost)
		err = crClient.Create(validCtx, va)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for TargetResolved=True")
		Eventually(func(g Gomega) {
			fetchedVA := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(validCtx, client.ObjectKey{Namespace: namespace, Name: deployName}, fetchedVA)
			g.Expect(err).NotTo(HaveOccurred())

			condition := v1alpha1.GetCondition(fetchedVA, v1alpha1.TypeTargetResolved)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(condition.Reason).To(Equal(v1alpha1.ReasonTargetFound))
		}, 1*time.Minute, 1*time.Second).Should(Succeed())

		// Cleanup
		_ = crClient.Delete(validCtx, va)
		_ = k8sClient.AppsV1().Deployments(namespace).Delete(validCtx, deployName, metav1.DeleteOptions{})
	})

	It("should set TargetResolved=False when target deployment does not exist", func() {
		name := "invalid-target-va"
		// No deployment created

		By("creating VariantAutoscaling")
		va := utils.CreateVariantAutoscalingResource(namespace, name, modelName, "A100", variantCost)
		err := crClient.Create(invalidCtx, va)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for TargetResolved=False")
		Eventually(func(g Gomega) {
			fetchedVA := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(invalidCtx, client.ObjectKey{Namespace: namespace, Name: name}, fetchedVA)
			g.Expect(err).NotTo(HaveOccurred())

			condition := v1alpha1.GetCondition(fetchedVA, v1alpha1.TypeTargetResolved)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(condition.Reason).To(Equal(v1alpha1.ReasonTargetNotFound))
		}, 1*time.Minute, 1*time.Second).Should(Succeed())

		// Cleanup
		_ = crClient.Delete(invalidCtx, va)
	})
})
