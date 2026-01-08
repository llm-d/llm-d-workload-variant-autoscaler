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

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
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

const (
	controllerNamespace           = "workload-variant-autoscaler-system"
	controllerMonitoringNamespace = "workload-variant-autoscaler-monitoring"
	llmDNamespace                 = "llm-d-sim"
	gatewayName                   = "infra-sim-inference-gateway-istio"
)

const (
	llamaModelId        = "unsloth/Meta-Llama-3.1-8B"
	a100Acc             = "A100"
	h100Acc             = "H100"
	inputTokens         = 1500
	outputTokens        = 1500
	maxExecutionTimeSec = 600
	loadRateTolerance   = 10
	avgITL              = 20
	avgTTFT             = 200
)

var (
	k8sClient *kubernetes.Clientset
	crClient  client.Client
	scheme    = runtime.NewScheme()

	GuidellmImage = "ghcr.io/vllm-project/guidellm:latest"
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

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", controllerNamespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", controllerNamespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})
		// +kubebuilder:scaffold:e2e-webhooks-checks
	})
})

var _ = Describe("Test workload-variant-autoscaler in emulated environment - single VariantAutoscaling", Ordered, func() {
	var (
		name            string
		namespace       string
		deployName      string
		serviceName     string
		serviceMonName  string
		appLabel        string
		loadGenJob      *batchv1.Job
		port            int
		loadRate        int
		modelName       string
		ctx             context.Context
		initialReplicas int32
		variantCost     float64
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()
		name = "llm-d-sim"
		namespace = llmDNamespace
		deployName = name + "-deployment"
		serviceName = name + "-service"
		serviceMonName = name + "-servicemonitor"
		appLabel = name
		port = 8000
		loadRate = 5 // requests per second
		modelName = llamaModelId

		initialReplicas = 2
		variantCost = 10.0

		By("ensuring unique app label for deployment and service")
		utils.ValidateAppLabelUniqueness(namespace, appLabel, k8sClient, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, llamaModelId, a100Acc, crClient)

		By("creating llm-d-sim deployment")
		deployment := utils.CreateLlmdSimDeployment(namespace, deployName, modelName, appLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err := k8sClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Deployment: %s", deployName))

		By("creating service to expose llm-d-sim deployment")
		service := utils.CreateLlmdSimService(namespace, serviceName, appLabel, 30000, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create Service: %s", serviceName))

		By("creating ServiceMonitor for vLLM metrics")
		serviceMonitor := utils.CreateLlmdSimServiceMonitor(serviceMonName, controllerMonitoringNamespace, llmDNamespace, appLabel)
		err = crClient.Create(ctx, serviceMonitor)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create ServiceMonitor: %s", serviceMonName))

		By("pod should be running before creating VariantAutoscaling resource")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(1))

			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))

		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating VariantAutoscaling resource")
		variantAutoscaling := utils.CreateVariantAutoscalingResource(namespace, deployName, modelName, a100Acc, variantCost)
		err = crClient.Create(ctx, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create VariantAutoscaling for: %s", deployName))

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	})

	It("deployment should be running", func() {
		Eventually(func() (appsv1.DeploymentStatus, error) {
			deployment, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
			if err != nil {
				return appsv1.DeploymentStatus{}, err
			}
			return deployment.Status, nil
		}, 4*time.Minute, 10*time.Second).Should(And(
			HaveField("ReadyReplicas", BeNumerically("==", 1)),
			HaveField("Replicas", BeNumerically("==", 1)),
		))
	})

	It("deployment should have correct deployment labels", func() {
		deployment, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get Deployment: %s", deployName))

		By("verifying deployment selector labels")
		selector := deployment.Spec.Selector.MatchLabels
		Expect(selector).To(HaveKeyWithValue("app", appLabel))
		Expect(selector).To(HaveKeyWithValue("llm-d.ai/inferenceServing", "true"))
		Expect(selector).To(HaveKeyWithValue("llm-d.ai/model", "ms-sim-llm-d-modelservice"))

		By("verifying pod template labels")
		podLabels := deployment.Spec.Template.Labels
		Expect(podLabels).To(HaveKeyWithValue("app", appLabel))
		Expect(podLabels).To(HaveKeyWithValue("llm-d.ai/inferenceServing", "true"))
		Expect(podLabels).To(HaveKeyWithValue("llm-d.ai/model", "ms-sim-llm-d-modelservice"))
	})

	It("deployment should have corresponding service with correct selector", func() {
		service, err := k8sClient.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get Service: %s", serviceName))

		By("verifying service selector")
		Expect(service.Spec.Selector).To(HaveKeyWithValue("app", appLabel))
	})

	It("deployment should create pods with correct labels", func() {
		Eventually(func() ([]corev1.Pod, error) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + appLabel,
			})
			if err != nil {
				return nil, err
			}
			return podList.Items, nil
		}, 2*time.Minute, 5*time.Second).Should(HaveLen(1))

		podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=" + appLabel,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(podList.Items).To(HaveLen(1))

		pod := podList.Items[0]
		By("verifying pod labels")
		Expect(pod.Labels).To(HaveKeyWithValue("app", appLabel))
		Expect(pod.Labels).To(HaveKeyWithValue("llm-d.ai/inferenceServing", "true"))
		Expect(pod.Labels).To(HaveKeyWithValue("llm-d.ai/model", "ms-sim-llm-d-modelservice"))
	})

	It("should have VariantAutoscaling resource created", func() {
		By("verifying VariantAutoscaling resource exists")
		variantAutoscaling := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      deployName,
		}, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling: %s", deployName))

		By("verifying VariantAutoscaling spec")
		Expect(variantAutoscaling.Spec.ModelID).To(Equal(modelName))
		Expect(variantAutoscaling.Name).To(Equal(deployName))
	})

	It("should have VariantAutoscaling with correct ownerReference to Deployment", func() {
		By("first ensuring both Deployment and VariantAutoscaling exist")
		Eventually(func(g Gomega) {
			_, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch Deployment for: %s", deployName))
			// NOTE: Replica checks excluded - using Prometheus metrics validation instead
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("ensuring VariantAutoscaling resource exists")
		Eventually(func(g Gomega) {
			variantAutoscaling := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, variantAutoscaling)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("VariantAutoscaling should exist for: %s", deployName))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("waiting for VariantAutoscaling to have ownerReference set by controller")
		Eventually(func(g Gomega) {
			deployment, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch Deployment for: %s", deployName))

			variantAutoscaling := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, variantAutoscaling)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))

			g.Expect(variantAutoscaling.OwnerReferences).To(HaveLen(1), fmt.Sprintf("VariantAutoscaling should have exactly one ownerReference related to: %s", deployName))

			ownerRef := variantAutoscaling.OwnerReferences[0]
			g.Expect(ownerRef.APIVersion).To(Equal("apps/v1"), fmt.Sprintf("ownerReference should have correct APIVersion for: %s", deployName))
			g.Expect(ownerRef.Kind).To(Equal("Deployment"), fmt.Sprintf("ownerReference should point to a Deployment for: %s", deployName))
			g.Expect(ownerRef.Name).To(Equal(deployment.Name), fmt.Sprintf("ownerReference should point to the correct Deployment name for: %s", deployName))
			g.Expect(ownerRef.UID).To(Equal(deployment.UID), fmt.Sprintf("ownerReference should have the correct UID for: %s", deployName))
			g.Expect(ownerRef.Controller).NotTo(BeNil(), fmt.Sprintf("ownerReference should have Controller field set for: %s", deployName))
			g.Expect(*ownerRef.Controller).To(BeTrue(), fmt.Sprintf("ownerReference Controller should be true for: %s", deployName))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
	})

	It("should scale out when load increases", func() {
		// Set up port-forwarding for Prometheus to enable metrics queries
		By("setting up port-forward to Prometheus service")
		prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, 9090, 9090)
		defer func() {
			err := utils.StopCmd(prometheusPortForwardCmd)
			Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
		}()

		By("waiting for Prometheus port-forward to be ready")
		err := utils.VerifyPortForwardReadiness(ctx, 9090, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", 9090))
		Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

		By("waiting for variantAutoscaling resource to have MetricsAvailable condition set to True")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, va)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))

			// Check that MetricsAvailable condition exists and is True
			metricsCondition := v1alpha1.GetCondition(va, v1alpha1.TypeMetricsAvailable)
			g.Expect(metricsCondition).NotTo(BeNil(),
				fmt.Sprintf("VariantAutoscaling %s should have MetricsAvailable condition", va.Name))
			g.Expect(metricsCondition.Status).To(Equal(metav1.ConditionTrue),
				fmt.Sprintf("VariantAutoscaling %s MetricsAvailable condition should be True", va.Name))
		}, 4*time.Minute, 10*time.Second).Should(Succeed())

		By("starting load generation to create traffic")
		loadGenJob, err = utils.CreateLoadGeneratorJob(namespace, fmt.Sprintf("http://%s:%d", gatewayName, 80), modelName, loadRate, maxExecutionTimeSec, inputTokens, outputTokens, k8sClient, ctx)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to start load generator sending requests to: %s", deployName))
		defer func() {
			By("stopping load generation job")
			err = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to stop load generator sending requests to: %s", deployName))
		}()

		By("waiting for job pod to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", loadGenJob.Name),
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
			g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

			pod := podList.Items[0]
			// Check if pod is running or has completed initialization
			g.Expect(pod.Status.Phase).To(Or(
				Equal(corev1.PodRunning),
				Equal(corev1.PodSucceeded),
			), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
		}, 10*time.Minute, 5*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running\n")

		var currentReplicasProm, desiredReplicasProm float64
		By("waiting for load to be processed and scaling decision to be made")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, va)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))

			// Verify that the optimized allocation has been computed
			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">", 0),
				fmt.Sprintf("DesiredOptimizedAlloc should have calculated optimized replicas for: %s - actual replicas: %d", va.Name, va.Status.DesiredOptimizedAlloc.NumReplicas))

			// Verify that the number of replicas has scaled up
			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">", 1),
				fmt.Sprintf("High load should trigger scale-up recommendation for VA: %s - actual replicas: %d", va.Name, va.Status.DesiredOptimizedAlloc.NumReplicas))

			// Verify Prometheus replica metrics
			currentReplicasProm, desiredReplicasProm, _, err = utils.GetInfernoReplicaMetrics(va.Name, namespace, va.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va.Name, err))

			g.Expect(desiredReplicasProm).To(BeNumerically(">", 1),
				fmt.Sprintf("Prometheus `inferno_desired_replicas` query should show scale-up for VA: %s - actual: %.2f", va.Name, desiredReplicasProm))
			g.Expect(currentReplicasProm).To(BeNumerically(">=", 1),
				fmt.Sprintf("Prometheus `inferno_current_replicas` query should show at least 1 replica for VA: %s - actual: %.2f", va.Name, currentReplicasProm))

			// Verify that the current and desired number of replicas have the same value as Prometheus results
			g.Expect(va.Status.CurrentAlloc.NumReplicas).To(BeNumerically("==", currentReplicasProm),
				fmt.Sprintf("Current replicas %d for VA %s should be the same as Prometheus result: %.2f", va.Status.CurrentAlloc.NumReplicas, deployName, currentReplicasProm))

			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicasProm),
				fmt.Sprintf("Desired replicas %d for VA %s should be the same as Prometheus result: %.2f", va.Status.DesiredOptimizedAlloc.NumReplicas, deployName, desiredReplicasProm))

			observedLoad, err := strconv.ParseFloat(va.Status.CurrentAlloc.Load.ArrivalRate, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc Load ArrivalRate to float for VA: %s", va.Name))

			// Calculate expected arrival rate based on latencies and GuideLLM constant rate behavior
			expectedArrivalRate := utils.CalculateExpectedArrivalRate(loadRate, avgTTFT, avgITL, outputTokens)

			// Verify that the observed load approximately matches the expected load
			g.Expect(observedLoad).To(BeNumerically("~", expectedArrivalRate, loadRateTolerance),
				fmt.Sprintf("Current load arrival rate for VA %s should approximately match the expected load: %.2f - observed: %.2f", va.Name, expectedArrivalRate, observedLoad))

			itlAvg, err := strconv.ParseFloat(va.Status.CurrentAlloc.ITLAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc ITLAverage to float for VA: %s", va.Name))

			ttftAvg, err := strconv.ParseFloat(va.Status.CurrentAlloc.TTFTAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc TTFTAverage to float for VA: %s", va.Name))

			g.Expect(itlAvg).To(BeNumerically(">", 0),
				fmt.Sprintf("Current ITL Average for VA %s should be greater than 0 under load", va.Name))

			g.Expect(ttftAvg).To(BeNumerically(">", 0),
				fmt.Sprintf("Current TTFT Average for VA %s should be greater than 0 under load", va.Name))

		}, 10*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying that the controller has updated the status")
		err = utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", deployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			deployName,
			int(desiredReplicasProm))

		By("verifying that the number of replicas remains constant with constant load")
		initialDesiredReplicas := int(desiredReplicasProm)
		Consistently(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, va)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))

			// Verify that the desired allocation remains stable with constant load
			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(Equal(initialDesiredReplicas),
				fmt.Sprintf("DesiredOptimizedAlloc for VA %s should stay at %d replicas with constant load equal to %s", deployName, initialDesiredReplicas, va.Status.CurrentAlloc.Load.ArrivalRate))

			// Verify Prometheus replica metrics
			_, desiredReplicasProm, _, err = utils.GetInfernoReplicaMetrics(va.Name, namespace, va.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicasProm),
				fmt.Sprintf("Desired replicas %d for VA %s should be the same as Prometheus result: %.2f", va.Status.DesiredOptimizedAlloc.NumReplicas, deployName, desiredReplicasProm))

			// Calculate expected arrival rate based on latencies and GuideLLM constant rate behavior
			expectedArrivalRate := utils.CalculateExpectedArrivalRate(loadRate, avgTTFT, avgITL, outputTokens)

			// Verify that the observed load approximately matches the expected load
			observedLoad, err := strconv.ParseFloat(va.Status.CurrentAlloc.Load.ArrivalRate, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc Load ArrivalRate to float for VA: %s", va.Name))

			g.Expect(observedLoad).To(BeNumerically("~", expectedArrivalRate, loadRateTolerance),
				fmt.Sprintf("Current load arrival rate for VA %s should approximately match the expected load: %.2f - observed: %.2f", va.Name, expectedArrivalRate, observedLoad))

			itlAvg, err := strconv.ParseFloat(va.Status.CurrentAlloc.ITLAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc ITLAverage to float for VA: %s", va.Name))

			ttftAvg, err := strconv.ParseFloat(va.Status.CurrentAlloc.TTFTAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc TTFTAverage to float for VA: %s", va.Name))

			g.Expect(itlAvg).To(BeNumerically(">", 0),
				fmt.Sprintf("Current ITL Average for VA %s should be greater than 0 under load", va.Name))

			g.Expect(ttftAvg).To(BeNumerically(">", 0),
				fmt.Sprintf("Current TTFT Average for VA %s should be greater than 0 under load", va.Name))

		}, 1*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying that the controller has updated the status")
		err = utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", deployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			deployName,
			int(desiredReplicasProm))
	})

	It("should scale in with no load", func() {
		// Set up port-forwarding for Prometheus to enable metrics queries
		By("setting up port-forward to Prometheus service")
		prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, 9090, 9090)
		defer func() {
			err := utils.StopCmd(prometheusPortForwardCmd)
			Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
		}()

		By("waiting for Prometheus port-forward to be ready")
		err := utils.VerifyPortForwardReadiness(ctx, 9090, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", 9090))
		Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

		var desiredReplicasProm float64
		By("waiting for scaling down decision to be made")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, va)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))

			// Verify that the number of replicas has scaled down to MinimumReplicas
			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
				fmt.Sprintf("No load should trigger scale-down to %d recommendation for: %s", MinimumReplicas, va.Name))

			// Verify Prometheus replica metrics
			_, desiredReplicasProm, _, err = utils.GetInfernoReplicaMetrics(va.Name, namespace, va.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicasProm),
				fmt.Sprintf("Current replicas for VA %s should stay at %.2f with no load", deployName, desiredReplicasProm))

		}, 4*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying that the controller has updated the status")
		err = utils.LogVariantAutoscalingStatus(ctx, deployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", deployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			deployName,
			int(desiredReplicasProm))
	})

	It("should connect to Prometheus using HTTPS with TLS", func() {
		By("verifying Prometheus is accessible via HTTPS")
		Eventually(func(g Gomega) {
			// Check if Prometheus service is running with TLS
			service, err := k8sClient.CoreV1().Services(controllerMonitoringNamespace).Get(ctx, "kube-prometheus-stack-prometheus", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Prometheus service should exist")
			g.Expect(service.Spec.Ports).To(ContainElement(HaveField("Port", int32(9090))), "Prometheus should be listening on port 9090")

			// Verify TLS secret exists
			secret, err := k8sClient.CoreV1().Secrets(controllerMonitoringNamespace).Get(ctx, "prometheus-web-tls", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "TLS secret should exist")
			g.Expect(secret.Data).To(HaveKey("tls.crt"), "TLS secret should contain certificate")
			g.Expect(secret.Data).To(HaveKey("tls.key"), "TLS secret should contain private key")

		}, 2*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying controller can connect to Prometheus with TLS")
		Eventually(func(g Gomega) {
			pods, err := k8sClient.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list controller pods")
			g.Expect(pods.Items).NotTo(BeEmpty(), "Controller pods should exist")

			// Check logs for TLS-related messages
			pod := pods.Items[0]
			logs, err := k8sClient.CoreV1().Pods(controllerNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				// Get all logs instead of just tail lines to find the TLS message from startup
			}).DoRaw(ctx)
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to get controller logs")

			logString := string(logs)
			g.Expect(logString).To(ContainSubstring("TLS configuration applied to Prometheus HTTPS transport"),
				"Controller should log TLS configuration")
			g.Expect(logString).NotTo(ContainSubstring("http: server gave HTTP response to HTTPS client"),
				"Controller should not have HTTP/HTTPS mismatch errors")

		}, 4*time.Minute, 15*time.Second).Should(Succeed())
	})

	It("should handle TLS certificate verification correctly", func() {
		By("verifying TLS configuration in controller ConfigMap")
		configMap, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx, "workload-variant-autoscaler-variantautoscaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "ConfigMap should exist")

		// Verify HTTPS URL is configured
		Expect(configMap.Data["PROMETHEUS_BASE_URL"]).To(ContainSubstring("https://"),
			"Prometheus URL should use HTTPS")

		// Verify TLS settings are configured
		Expect(configMap.Data["PROMETHEUS_TLS_INSECURE_SKIP_VERIFY"]).To(Equal("true"),
			"TLS insecure skip verify should be enabled for e2e tests")

		By("verifying controller startup with TLS configuration")
		Eventually(func(g Gomega) {
			pods, err := k8sClient.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list controller pods")

			for _, pod := range pods.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning),
					fmt.Sprintf("Pod %s should be running", pod.Name))
			}
		}, 2*time.Minute, 10*time.Second).Should(Succeed())
	})

	It("should have VariantAutoscaling deleted when Deployment is deleted", func() {
		By("deleting the Deployment")
		err := k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", deployName))

		By("verifying VariantAutoscaling is deleted due to owner reference")
		variantAutoscaling := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      deployName,
		}, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", deployName))
		Eventually(func() error {
			return crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      deployName,
			}, variantAutoscaling)
		}, 4*time.Minute, 2*time.Second).Should(HaveOccurred(), fmt.Sprintf("VariantAutoscaling for: %s should be deleted", deployName))
	})

	AfterAll(func() {
		By("deleting VariantAutoscaling resource")
		variantAutoscaling := &v1alpha1.VariantAutoscaling{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deployName,
				Namespace: namespace,
			},
		}
		err := crClient.Delete(ctx, variantAutoscaling)
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling for: %s", deployName))

		By("deleting ServiceMonitor")
		serviceMonitor := &promoperator.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceMonName,
				Namespace: controllerMonitoringNamespace,
			},
		}
		err = crClient.Delete(ctx, serviceMonitor)
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", serviceMonName))

		By("deleting llm-d-sim service")
		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", serviceName))

		By("deleting llm-d-sim deployment")
		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{})
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", deployName))

		By("waiting for all pods to be deleted")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + appLabel})
			if err != nil {
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to list Pods")
			}
			g.Expect(podList.Items).To(BeEmpty(), fmt.Sprintf("All Pods labelled: %s should be deleted", appLabel))
		}, 1*time.Minute, 1*time.Second).Should(Succeed())
	})
})

var _ = Describe("Test workload-variant-autoscaler in emulated environment - multiple VariantAutoscalings", Ordered, func() {
	var (
		firstName                string
		secondName               string
		namespace                string
		firstDeployName          string
		secondDeployName         string
		firstAppLabel            string
		secondAppLabel           string
		firstServiceName         string
		firstServiceMonitorName  string
		secondServiceName        string
		secondServiceMonitorName string
		firstModelName           string
		secondModelName          string
		firstVariantCost         float64
		secondVariantCost        float64
		port                     int
		loadRate                 int
		initialReplicas          int32
		ctx                      context.Context
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()
		port = 8000
		firstName = "llm-d-sim-1"
		namespace = llmDNamespace
		firstDeployName = firstName + "-deployment"
		firstAppLabel = firstName
		firstServiceName = firstName + "-service"
		firstServiceMonitorName = firstName + "-servicemonitor"
		secondName = "llm-d-sim-2"
		secondDeployName = secondName + "-deployment"
		secondServiceName = secondName + "-service"
		secondServiceMonitorName = secondName + "-servicemonitor"
		secondAppLabel = secondName
		firstModelName = llamaModelId
		secondModelName = llamaModelId
		loadRate = 3 // requests per second

		initialReplicas = 2
		firstVariantCost = 10.0
		secondVariantCost = 10.0

		By("ensuring unique app labels for deployment and service")
		utils.ValidateAppLabelUniqueness(namespace, firstAppLabel, k8sClient, crClient)
		utils.ValidateAppLabelUniqueness(namespace, secondAppLabel, k8sClient, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, llamaModelId, a100Acc, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, llamaModelId, h100Acc, crClient)

		By("creating resources for the first deployment")
		firstDeployment := utils.CreateLlmdSimDeployment(namespace, firstDeployName, firstModelName, firstAppLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err := k8sClient.AppsV1().Deployments(namespace).Create(ctx, firstDeployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create first Deployment: %s", firstDeployName))

		firstService := utils.CreateLlmdSimService(namespace, firstServiceName, firstAppLabel, 30000, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, firstService, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create first Service: %s", firstServiceName))

		firstServiceMonitor := utils.CreateLlmdSimServiceMonitor(firstServiceMonitorName, controllerMonitoringNamespace, llmDNamespace, firstAppLabel)
		err = crClient.Create(ctx, firstServiceMonitor)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create first ServiceMonitor: %s", firstServiceMonitorName))

		By("pod should be running before creating VariantAutoscaling resource")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + firstAppLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(1))

			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))

		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		variantAutoscaling := utils.CreateVariantAutoscalingResource(namespace, firstDeployName, firstModelName, a100Acc, firstVariantCost)
		err = crClient.Create(ctx, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create first VariantAutoscaling for: %s", firstDeployName))

		By("creating resources for the second deployment")
		secondDeployment := utils.CreateLlmdSimDeployment(namespace, secondDeployName, secondModelName, secondAppLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err = k8sClient.AppsV1().Deployments(namespace).Create(ctx, secondDeployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create second Deployment: %s", secondDeployName))

		By("pod should be running before creating VariantAutoscaling resource")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + secondAppLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(1))

			pod := podList.Items[0]
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))

		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		secondVariantAutoscaling := utils.CreateVariantAutoscalingResource(namespace, secondDeployName, secondModelName, h100Acc, secondVariantCost)
		err = crClient.Create(ctx, secondVariantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create second VariantAutoscaling for: %s", secondDeployName))

		secondService := utils.CreateLlmdSimService(namespace, secondServiceName, secondAppLabel, 30001, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, secondService, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create second Service: %s", secondServiceName))

		secondServiceMonitor := utils.CreateLlmdSimServiceMonitor(secondServiceMonitorName, controllerMonitoringNamespace, llmDNamespace, secondAppLabel)
		err = crClient.Create(ctx, secondServiceMonitor)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create second ServiceMonitor: %s", secondServiceMonitorName))

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	})

	It("deployments should be running", func() {
		Eventually(func() (appsv1.DeploymentStatus, error) {
			deployment, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, firstDeployName, metav1.GetOptions{})
			if err != nil {
				return appsv1.DeploymentStatus{}, err
			}
			return deployment.Status, nil
		}, 4*time.Minute, 10*time.Second).Should(And(
			HaveField("ReadyReplicas", BeNumerically("==", 1)),
			HaveField("Replicas", BeNumerically("==", 1)),
		))

		Eventually(func() (appsv1.DeploymentStatus, error) {
			deployment, err := k8sClient.AppsV1().Deployments(namespace).Get(ctx, secondDeployName, metav1.GetOptions{})
			if err != nil {
				return appsv1.DeploymentStatus{}, err
			}
			return deployment.Status, nil
		}, 4*time.Minute, 10*time.Second).Should(And(
			HaveField("ReadyReplicas", BeNumerically("==", 1)),
			HaveField("Replicas", BeNumerically("==", 1)),
		))
	})

	It("should have VariantAutoscaling resource created", func() {
		By("verifying VariantAutoscaling resources exist")
		variantAutoscaling := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      firstDeployName,
		}, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling for: %s", firstDeployName))

		variantAutoscaling = &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      secondDeployName,
		}, variantAutoscaling)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling for: %s", secondDeployName))
	})

	It("should scale out when load increases", func() {
		By("verifying initial state of VariantAutoscaling")
		initialVA := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      firstDeployName,
		}, initialVA)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling for: %s", firstDeployName))

		// Set up port-forwarding for Prometheus to enable metrics queries
		By("setting up port-forward to Prometheus service")
		prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, 9090, 9090)
		defer func() {
			err = utils.StopCmd(prometheusPortForwardCmd)
			Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
		}()

		By("waiting for Prometheus port-forward to be ready")
		err = utils.VerifyPortForwardReadiness(ctx, 9090, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", 9090))
		Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

		By("waiting for variantAutoscaling resource to have MetricsAvailable condition set to True")
		Eventually(func(g Gomega) {
			va1 := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      firstDeployName,
			}, va1)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", firstDeployName))

			// Check that MetricsAvailable condition exists and is True
			metricsCondition := v1alpha1.GetCondition(va1, v1alpha1.TypeMetricsAvailable)
			g.Expect(metricsCondition).NotTo(BeNil(),
				fmt.Sprintf("VariantAutoscaling %s should have MetricsAvailable condition", va1.Name))
			g.Expect(metricsCondition.Status).To(Equal(metav1.ConditionTrue),
				fmt.Sprintf("VariantAutoscaling %s MetricsAvailable condition should be True", va1.Name))

			va2 := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secondDeployName,
			}, va2)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", secondDeployName))
			// Check that MetricsAvailable condition exists and is True
			metricsCondition = v1alpha1.GetCondition(va2, v1alpha1.TypeMetricsAvailable)
			g.Expect(metricsCondition).NotTo(BeNil(),
				fmt.Sprintf("VariantAutoscaling %s should have MetricsAvailable condition", va2.Name))
			g.Expect(metricsCondition.Status).To(Equal(metav1.ConditionTrue),
				fmt.Sprintf("VariantAutoscaling %s MetricsAvailable condition should be True", va2.Name))
		}, 4*time.Minute, 10*time.Second).Should(Succeed())

		By("starting load generation to create traffic for both deployments")
		loadGenJob1, err := utils.CreateLoadGeneratorJob(namespace, fmt.Sprintf("http://%s:%d", gatewayName, 80), firstModelName, loadRate, maxExecutionTimeSec, inputTokens, outputTokens, k8sClient, ctx)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to start load generator sending requests to: %s", firstDeployName))
		defer func() {
			err = utils.StopJob(namespace, loadGenJob1, k8sClient, ctx)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to stop load generator sending requests to: %s", firstDeployName))
		}()

		By("waiting for job pod to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", loadGenJob1.Name),
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
			g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

			pod := podList.Items[0]
			// Check if pod is running or has completed initialization
			g.Expect(pod.Status.Phase).To(Or(
				Equal(corev1.PodRunning),
				Equal(corev1.PodSucceeded),
			), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
		}, 10*time.Minute, 5*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Load generation job is running\n")

		var desiredReplicas1, desiredReplicas2 float64
		By("waiting for load to be processed and scaling decision to be made")
		Eventually(func(g Gomega) {
			va1 := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      firstDeployName,
			}, va1)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", firstDeployName))

			// Verify that the number of replicas has scaled up
			g.Expect(va1.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">", 1),
				fmt.Sprintf("High load should trigger scale-up recommendation for VA: %s", va1.Name))

			// Verify Prometheus replica metrics
			_, desiredReplicas1, _, err = utils.GetInfernoReplicaMetrics(va1.Name, namespace, va1.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va1.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va1.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicas1),
				fmt.Sprintf("Current desired replicas for VA status %s should be equal to %d", va1.Name, int(desiredReplicas1)))

			itlAvg1, err := strconv.ParseFloat(va1.Status.CurrentAlloc.ITLAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc ITLAverage to float for VA: %s", va1.Name))

			ttftAvg1, err := strconv.ParseFloat(va1.Status.CurrentAlloc.TTFTAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc TTFTAverage to float for VA: %s", va1.Name))

			g.Expect(itlAvg1).To(BeNumerically(">", 0),
				fmt.Sprintf("Current ITL Average for VA %s should be greater than 0 under load", va1.Name))

			g.Expect(ttftAvg1).To(BeNumerically(">", 0),
				fmt.Sprintf("Current TTFT Average for VA %s should be greater than 0 under load", va1.Name))

			va2 := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secondDeployName,
			}, va2)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", secondDeployName))

			// Verify that the number of replicas has scaled up
			g.Expect(va2.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">", 1),
				fmt.Sprintf("High load should trigger scale-up recommendation for VA: %s", va2.Name))

			// Verify Prometheus replica metrics
			_, desiredReplicas2, _, err = utils.GetInfernoReplicaMetrics(va2.Name, namespace, va2.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va2.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va2.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicas2),
				fmt.Sprintf("Current desired replicas for VA status %s should be equal to %d", va2.Name, int(desiredReplicas2)))

			itlAvg2, err := strconv.ParseFloat(va2.Status.CurrentAlloc.ITLAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc ITLAverage to float for VA: %s", va2.Name))

			ttftAvg2, err := strconv.ParseFloat(va2.Status.CurrentAlloc.TTFTAverage, 64)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to convert CurrentAlloc TTFTAverage to float for VA: %s", va2.Name))
			g.Expect(itlAvg2).To(BeNumerically(">", 0),
				fmt.Sprintf("Current ITL Average for VA %s should be greater than 0 under load", va2.Name))

			g.Expect(ttftAvg2).To(BeNumerically(">", 0),
				fmt.Sprintf("Current TTFT Average for VA %s should be greater than 0 under load", va2.Name))
		}, 10*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying that the controller has updated the status")
		err = utils.LogVariantAutoscalingStatus(ctx, firstDeployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", firstDeployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			firstDeployName,
			int(desiredReplicas1))

		err = utils.LogVariantAutoscalingStatus(ctx, secondDeployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", secondDeployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			secondDeployName,
			int(desiredReplicas2))
	})

	It("should further scale out if load further increases", func() {
		By("verifying initial state of VariantAutoscaling")
		initialVA1 := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      firstDeployName,
		}, initialVA1)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling for: %s", firstDeployName))

		initialVA2 := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      secondDeployName,
		}, initialVA2)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to get VariantAutoscaling for: %s", secondDeployName))

		initialOptimizedReplicas1 := initialVA1.Status.DesiredOptimizedAlloc.NumReplicas
		initialOptimizedReplicas2 := initialVA2.Status.DesiredOptimizedAlloc.NumReplicas

		// Set up port-forwarding for Prometheus to enable metrics queries
		By("setting up port-forward to Prometheus service")
		prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, 9090, 9090)
		defer func() {
			err = utils.StopCmd(prometheusPortForwardCmd)
			Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
		}()

		By("waiting for Prometheus port-forward to be ready")
		err = utils.VerifyPortForwardReadiness(ctx, 9090, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", 9090))
		Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

		By("starting load generation to create traffic for both deployments")
		loadRate = 10 // significantly increased requests per second to trigger further scale-up from 2 to 3+ replicas
		// Target services directly to ensure each deployment gets dedicated load
		// Using service short DNS name: <service-name>.<namespace>
		firstServiceURL := fmt.Sprintf("http://%s.%s:%d", firstServiceName, namespace, port)
		loadGenJob1, err := utils.CreateLoadGeneratorJob(namespace, firstServiceURL, firstModelName, loadRate, maxExecutionTimeSec, inputTokens, outputTokens, k8sClient, ctx)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to start load generator sending requests to: %s", firstDeployName))
		defer func() {
			err = utils.StopJob(namespace, loadGenJob1, k8sClient, ctx)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to stop load generator sending requests to: %s", firstDeployName))
		}()

		secondServiceURL := fmt.Sprintf("http://%s.%s:%d", secondServiceName, namespace, port)
		loadGenJob2, err := utils.CreateLoadGeneratorJob(namespace, secondServiceURL, secondModelName, loadRate, maxExecutionTimeSec, inputTokens, outputTokens, k8sClient, ctx)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to start load generator sending requests to: %s", secondDeployName))
		defer func() {
			err = utils.StopJob(namespace, loadGenJob2, k8sClient, ctx)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to stop load generator sending requests to: %s", secondDeployName))
		}()

		By("waiting for job pods to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", loadGenJob1.Name),
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
			g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

			pod := podList.Items[0]
			// Check if pod is running or has completed initialization
			g.Expect(pod.Status.Phase).To(Or(
				Equal(corev1.PodRunning),
				Equal(corev1.PodSucceeded),
			), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(llmDNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", loadGenJob2.Name),
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list job pods")
			g.Expect(podList.Items).NotTo(BeEmpty(), "Job pod should exist")

			pod := podList.Items[0]
			// Check if pod is running or has completed initialization
			g.Expect(pod.Status.Phase).To(Or(
				Equal(corev1.PodRunning),
				Equal(corev1.PodSucceeded),
			), fmt.Sprintf("Job pod should be running or succeeded, but is in phase: %s", pod.Status.Phase))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		_, _ = fmt.Fprintf(GinkgoWriter, "Load generation jobs are running\n")

		By("waiting for load generation to ramp up again (30 seconds)")
		time.Sleep(30 * time.Second)

		var desiredReplicas1, desiredReplicas2 float64
		By("waiting for load to be processed and scaling decision to be made")
		Eventually(func(g Gomega) {
			va1 := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      firstDeployName,
			}, va1)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", firstDeployName))

			// Verify that the number of replicas has scaled up
			g.Expect(va1.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", initialOptimizedReplicas1),
				fmt.Sprintf("High load should trigger scale-up recommendation for VA: %s - actual replicas: %d", firstDeployName, va1.Status.DesiredOptimizedAlloc.NumReplicas))

			// Verify Prometheus replica metrics
			_, desiredReplicas1, _, err = utils.GetInfernoReplicaMetrics(va1.Name, namespace, va1.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va1.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va1.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicas1),
				fmt.Sprintf("Current replicas for VA %s should stay at %.2f with no load", va1.Name, desiredReplicas1))

			va2 := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secondDeployName,
			}, va2)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", secondDeployName))

			// Verify that the number of replicas has scaled up
			g.Expect(va2.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">", initialOptimizedReplicas2),
				fmt.Sprintf("High load should trigger scale-up recommendation for VA: %s - actual replicas: %d", secondDeployName, va2.Status.DesiredOptimizedAlloc.NumReplicas))

			// Verify Prometheus replica metrics
			_, desiredReplicas2, _, err = utils.GetInfernoReplicaMetrics(va2.Name, namespace, va2.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va2.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va2.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicas2),
				fmt.Sprintf("Current replicas for VA %s should stay at %.2f with no load", va2.Name, desiredReplicas2))

		}, 10*time.Minute, 10*time.Second).Should(Succeed())

		By("showing the status of VAs")
		err = utils.LogVariantAutoscalingStatus(ctx, firstDeployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", firstDeployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			firstDeployName,
			int(desiredReplicas1))

		err = utils.LogVariantAutoscalingStatus(ctx, secondDeployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", secondDeployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			secondDeployName,
			int(desiredReplicas2))
	})

	It("should scale in with no load", func() {
		// Set up port-forwarding for Prometheus to enable metrics queries
		By("setting up port-forward to Prometheus service")
		prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, 9090, 9090)
		defer func() {
			err := utils.StopCmd(prometheusPortForwardCmd)
			Expect(err).NotTo(HaveOccurred(), "Should be able to stop Prometheus port-forwarding")
		}()

		By("waiting for Prometheus port-forward to be ready")
		err := utils.VerifyPortForwardReadiness(ctx, 9090, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", 9090))
		Expect(err).NotTo(HaveOccurred(), "Prometheus port-forward should be ready within timeout")

		var desiredReplicas1, desiredReplicas2 float64
		By("waiting for scaling down decision to be made")
		Eventually(func(g Gomega) {
			va1 := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      firstDeployName,
			}, va1)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", firstDeployName))

			// Verify that the number of replicas has scaled down to MinimumReplicas
			g.Expect(va1.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
				fmt.Sprintf("No load should trigger scale-down recommendation to %d for VA: %s - actual replicas: %d", MinimumReplicas, firstDeployName, va1.Status.CurrentAlloc.NumReplicas))

			// Verify Prometheus replica metrics
			_, desiredReplicas1, _, err = utils.GetInfernoReplicaMetrics(va1.Name, namespace, va1.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va1.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va1.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicas1),
				fmt.Sprintf("Current replicas for VA %s should stay at %.2f with no load", va1.Name, desiredReplicas1))

			va2 := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secondDeployName,
			}, va2)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to fetch VariantAutoscaling for: %s", secondDeployName))

			// Verify that the number of replicas has scaled down to MinimumReplicas
			g.Expect(va2.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
				fmt.Sprintf("High load should trigger scale-up recommendation to %d for VA: %s - actual replicas: %d", MinimumReplicas, secondDeployName, va2.Status.CurrentAlloc.NumReplicas))

			// Verify Prometheus replica metrics
			_, desiredReplicas2, _, err = utils.GetInfernoReplicaMetrics(va2.Name, namespace, va2.Status.CurrentAlloc.Accelerator)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to query Prometheus metrics for: %s - got error: %v", va2.Name, err))

			// Verify that the desired number of replicas has same value as Prometheus result
			g.Expect(va2.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", desiredReplicas2),
				fmt.Sprintf("Current replicas for VA %s should stay at %.2f with no load", va2.Name, desiredReplicas2))

		}, 4*time.Minute, 10*time.Second).Should(Succeed())

		By("verifying that the controller has updated the status")

		err = utils.LogVariantAutoscalingStatus(ctx, firstDeployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", firstDeployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			firstDeployName,
			int(desiredReplicas1))

		err = utils.LogVariantAutoscalingStatus(ctx, secondDeployName, namespace, crClient, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to log VariantAutoscaling status for: %s", secondDeployName))

		fmt.Printf("Prometheus metrics for VA %s - desired replicas: %d\n",
			secondDeployName,
			int(desiredReplicas2))
	})

	AfterAll(func() {
		By("deleting resources for first deployment")
		variantAutoscaling := &v1alpha1.VariantAutoscaling{
			ObjectMeta: metav1.ObjectMeta{
				Name:      firstDeployName,
				Namespace: namespace,
			},
		}
		err := crClient.Delete(ctx, variantAutoscaling)
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling for: %s", firstDeployName))

		serviceMonitor := &promoperator.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      firstServiceMonitorName,
				Namespace: controllerMonitoringNamespace,
			},
		}
		err = crClient.Delete(ctx, serviceMonitor)
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", firstServiceMonitorName))

		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, firstServiceName, metav1.DeleteOptions{})
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", firstServiceName))

		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, firstDeployName, metav1.DeleteOptions{})
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", firstDeployName))

		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + firstAppLabel})
			if err != nil {
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to list Pods")
			}
			g.Expect(podList.Items).To(BeEmpty(), fmt.Sprintf("All Pods labelled: %s should be deleted", firstAppLabel))
		}, 1*time.Minute, 1*time.Second).Should(Succeed())

		By("deleting resources for second deployment")
		variantAutoscaling = &v1alpha1.VariantAutoscaling{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secondDeployName,
				Namespace: namespace,
			},
		}
		err = crClient.Delete(ctx, variantAutoscaling)
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete VariantAutoscaling for: %s", secondDeployName))

		serviceMonitor = &promoperator.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secondServiceMonitorName,
				Namespace: controllerMonitoringNamespace,
			},
		}
		err = crClient.Delete(ctx, serviceMonitor)
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete ServiceMonitor: %s", secondServiceMonitorName))

		err = k8sClient.CoreV1().Services(namespace).Delete(ctx, secondServiceName, metav1.DeleteOptions{})
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Service: %s", secondServiceName))

		err = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, secondDeployName, metav1.DeleteOptions{})
		err = client.IgnoreNotFound(err)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete Deployment: %s", secondDeployName))

		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + secondAppLabel})
			if err != nil {
				g.Expect(err).NotTo(HaveOccurred(), "Should be able to list Pods")
			}
			g.Expect(podList.Items).To(BeEmpty(), fmt.Sprintf("All Pods labelled: %s should be deleted", secondAppLabel))
		}, 1*time.Minute, 1*time.Second).Should(Succeed())
	})
})

// Test accelerator-based VA grouping isolation (issue #454)
// This test validates that VAs with the same model but different accelerators
// are treated as separate scaling domains and don't affect each other's scaling decisions.
var _ = Describe("Test accelerator-based VA grouping isolation", Ordered, func() {
	var (
		// Two VAs with same model but different accelerators
		a100Name           string
		h100Name           string
		namespace          string
		a100DeployName     string
		h100DeployName     string
		a100AppLabel       string
		h100AppLabel       string
		a100ServiceName    string
		h100ServiceName    string
		a100ServiceMonName string
		h100ServiceMonName string
		modelName          string
		a100VariantCost    float64
		h100VariantCost    float64
		port               int
		loadRate           int
		initialReplicas    int32
		ctx                context.Context
	)

	BeforeAll(func() {
		if os.Getenv("KUBECONFIG") == "" {
			Skip("KUBECONFIG is not set; skipping e2e test")
		}

		initializeK8sClient()

		ctx = context.Background()
		port = 8000
		namespace = llmDNamespace
		modelName = llamaModelId // Same model for both

		// A100 variant
		a100Name = "llm-d-sim-a100"
		a100DeployName = a100Name + "-deployment"
		a100AppLabel = a100Name
		a100ServiceName = a100Name + "-service"
		a100ServiceMonName = a100Name + "-servicemonitor"

		// H100 variant - same model, different accelerator
		h100Name = "llm-d-sim-h100"
		h100DeployName = h100Name + "-deployment"
		h100AppLabel = h100Name
		h100ServiceName = h100Name + "-service"
		h100ServiceMonName = h100Name + "-servicemonitor"

		loadRate = 5
		initialReplicas = 1
		a100VariantCost = 10.0
		h100VariantCost = 15.0

		By("ensuring unique app labels for deployments")
		utils.ValidateAppLabelUniqueness(namespace, a100AppLabel, k8sClient, crClient)
		utils.ValidateAppLabelUniqueness(namespace, h100AppLabel, k8sClient, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, a100Acc, crClient)
		utils.ValidateVariantAutoscalingUniqueness(namespace, modelName, h100Acc, crClient)

		By("creating A100 deployment and VA")
		a100Deployment := utils.CreateLlmdSimDeployment(namespace, a100DeployName, modelName, a100AppLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err := k8sClient.AppsV1().Deployments(namespace).Create(ctx, a100Deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create A100 Deployment: %s", a100DeployName))

		a100Service := utils.CreateLlmdSimService(namespace, a100ServiceName, a100AppLabel, 30010, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, a100Service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create A100 Service: %s", a100ServiceName))

		a100ServiceMon := utils.CreateLlmdSimServiceMonitor(a100ServiceMonName, controllerMonitoringNamespace, llmDNamespace, a100AppLabel)
		err = crClient.Create(ctx, a100ServiceMon)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create A100 ServiceMonitor: %s", a100ServiceMonName))

		By("waiting for A100 pod to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + a100AppLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(1))
			g.Expect(podList.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		a100VA := utils.CreateVariantAutoscalingResource(namespace, a100DeployName, modelName, a100Acc, a100VariantCost)
		err = crClient.Create(ctx, a100VA)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create A100 VariantAutoscaling: %s", a100DeployName))

		By("creating H100 deployment and VA with same model")
		h100Deployment := utils.CreateLlmdSimDeployment(namespace, h100DeployName, modelName, h100AppLabel, fmt.Sprintf("%d", port), avgTTFT, avgITL, initialReplicas)
		_, err = k8sClient.AppsV1().Deployments(namespace).Create(ctx, h100Deployment, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create H100 Deployment: %s", h100DeployName))

		h100Service := utils.CreateLlmdSimService(namespace, h100ServiceName, h100AppLabel, 30011, port)
		_, err = k8sClient.CoreV1().Services(namespace).Create(ctx, h100Service, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create H100 Service: %s", h100ServiceName))

		h100ServiceMon := utils.CreateLlmdSimServiceMonitor(h100ServiceMonName, controllerMonitoringNamespace, llmDNamespace, h100AppLabel)
		err = crClient.Create(ctx, h100ServiceMon)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create H100 ServiceMonitor: %s", h100ServiceMonName))

		By("waiting for H100 pod to be running")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + h100AppLabel,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).To(HaveLen(1))
			g.Expect(podList.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		h100VA := utils.CreateVariantAutoscalingResource(namespace, h100DeployName, modelName, h100Acc, h100VariantCost)
		err = crClient.Create(ctx, h100VA)
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to create H100 VariantAutoscaling: %s", h100DeployName))

		logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	})

	It("should have both VAs with same model but different accelerators", func() {
		By("verifying both VAs exist with same model ID")
		a100VA := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: a100DeployName}, a100VA)
		Expect(err).NotTo(HaveOccurred())
		Expect(a100VA.Spec.ModelID).To(Equal(modelName))

		h100VA := &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: h100DeployName}, h100VA)
		Expect(err).NotTo(HaveOccurred())
		Expect(h100VA.Spec.ModelID).To(Equal(modelName))

		By("verifying they have different accelerators")
		Expect(a100VA.Spec.ModelProfile.Accelerators[0].Acc).To(Equal(a100Acc))
		Expect(h100VA.Spec.ModelProfile.Accelerators[0].Acc).To(Equal(h100Acc))
	})

	It("should isolate scaling decisions between VAs with different accelerators", func() {
		By("setting up Prometheus port-forward")
		prometheusPortForwardCmd := utils.SetUpPortForward(k8sClient, ctx, "kube-prometheus-stack-prometheus", controllerMonitoringNamespace, 9090, 9090)
		defer func() {
			_ = utils.StopCmd(prometheusPortForwardCmd)
		}()

		err := utils.VerifyPortForwardReadiness(ctx, 9090, fmt.Sprintf("https://localhost:%d/api/v1/query?query=up", 9090))
		Expect(err).NotTo(HaveOccurred())

		By("waiting for both VAs to have MetricsAvailable condition")
		Eventually(func(g Gomega) {
			a100VA := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: a100DeployName}, a100VA)
			g.Expect(err).NotTo(HaveOccurred())
			cond := v1alpha1.GetCondition(a100VA, v1alpha1.TypeMetricsAvailable)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))

			h100VA := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: h100DeployName}, h100VA)
			g.Expect(err).NotTo(HaveOccurred())
			cond = v1alpha1.GetCondition(h100VA, v1alpha1.TypeMetricsAvailable)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}, 4*time.Minute, 10*time.Second).Should(Succeed())

		By("applying load ONLY to A100 deployment")
		a100ServiceURL := fmt.Sprintf("http://%s.%s:%d", a100ServiceName, namespace, port)
		loadGenJob, err := utils.CreateLoadGeneratorJob(namespace, a100ServiceURL, modelName, loadRate, maxExecutionTimeSec, inputTokens, outputTokens, k8sClient, ctx)
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			_ = utils.StopJob(namespace, loadGenJob, k8sClient, ctx)
		}()

		By("waiting for load generator to start")
		Eventually(func(g Gomega) {
			podList, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", loadGenJob.Name),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podList.Items).NotTo(BeEmpty())
			g.Expect(podList.Items[0].Status.Phase).To(Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying A100 VA scales up while H100 VA remains at minimum")
		Eventually(func(g Gomega) {
			// A100 should scale up due to load
			a100VA := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: a100DeployName}, a100VA)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(a100VA.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">", 1),
				fmt.Sprintf("A100 VA should scale up under load, got %d replicas", a100VA.Status.DesiredOptimizedAlloc.NumReplicas))

			// H100 should remain at minimum (no load)
			h100VA := &v1alpha1.VariantAutoscaling{}
			err = crClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: h100DeployName}, h100VA)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(h100VA.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically("==", MinimumReplicas),
				fmt.Sprintf("H100 VA should remain at minimum replicas (%d) with no load, got %d replicas - "+
					"this validates accelerator-based isolation (issue #454)", MinimumReplicas, h100VA.Status.DesiredOptimizedAlloc.NumReplicas))
		}, 10*time.Minute, 10*time.Second).Should(Succeed())

		By("logging VA status to confirm isolation")
		_ = utils.LogVariantAutoscalingStatus(ctx, a100DeployName, namespace, crClient, GinkgoWriter)
		_ = utils.LogVariantAutoscalingStatus(ctx, h100DeployName, namespace, crClient, GinkgoWriter)
	})

	AfterAll(func() {
		By("cleaning up A100 resources")
		_ = crClient.Delete(ctx, &v1alpha1.VariantAutoscaling{ObjectMeta: metav1.ObjectMeta{Name: a100DeployName, Namespace: namespace}})
		_ = crClient.Delete(ctx, &promoperator.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: a100ServiceMonName, Namespace: controllerMonitoringNamespace}})
		_ = k8sClient.CoreV1().Services(namespace).Delete(ctx, a100ServiceName, metav1.DeleteOptions{})
		_ = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, a100DeployName, metav1.DeleteOptions{})

		By("cleaning up H100 resources")
		_ = crClient.Delete(ctx, &v1alpha1.VariantAutoscaling{ObjectMeta: metav1.ObjectMeta{Name: h100DeployName, Namespace: namespace}})
		_ = crClient.Delete(ctx, &promoperator.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: h100ServiceMonName, Namespace: controllerMonitoringNamespace}})
		_ = k8sClient.CoreV1().Services(namespace).Delete(ctx, h100ServiceName, metav1.DeleteOptions{})
		_ = k8sClient.AppsV1().Deployments(namespace).Delete(ctx, h100DeployName, metav1.DeleteOptions{})

		By("waiting for pods to be deleted")
		Eventually(func(g Gomega) {
			podList, _ := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app in (%s, %s)", a100AppLabel, h100AppLabel),
			})
			g.Expect(podList.Items).To(BeEmpty())
		}, 1*time.Minute, 1*time.Second).Should(Succeed())
	})
})
