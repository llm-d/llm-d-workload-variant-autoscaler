package e2eemulated

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" // nolint:all
	. "github.com/onsi/gomega"    // nolint:all
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(promoperator.AddToScheme(scheme))
}

// Kind cluster configuration constants
const (
	maximumAvailableGPUs = 4
	numNodes             = 3
	gpuTypes             = "mix"
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

var (
	// Optional Environment Variables:
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// These variables are useful if CertManager is already installed, avoiding
	// re-installation and conflicts.
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "ghcr.io/llm-d-incubation/workload-variant-autoscaler:0.0.1-test"

	MinimumReplicas = 1

	k8sClient *kubernetes.Clientset
	crClient  client.Client
	scheme    = runtime.NewScheme()
)

// setupInfrastructure handles the infrastructure setup for e2e tests.
// This includes building the image, deploying to Kind, and configuring the controller.
func setupInfrastructure() {
	var err error

	By("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

	By("exporting environment variables for deployment")
	utils.SetupTestEnvironment(projectImage, numNodes, maximumAvailableGPUs, gpuTypes)

	// Deploy llm-d and workload-variant-autoscaler on the Kind cluster
	By("deploying Workload Variant Autoscaler on Kind")
	launchCmd := exec.Command("make", "deploy-wva-emulated-on-kind", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(launchCmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install llm-d and workload-variant-autoscaler")

	initializeK8sClient()

	waitForControllerReady()
	setupCertManager()
}

// teardownInfrastructure handles the infrastructure teardown for e2e tests.
// This includes uninstalling CertManager and destroying the Kind cluster.
func teardownInfrastructure() {
	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}

	// Destroy the Kind cluster
	cmd := exec.Command("bash", "deploy/kind-emulator/teardown.sh")
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to destroy Kind cluster")
}

// waitForControllerReady waits for the controller-manager pods to be ready and acquire lease.
func waitForControllerReady() {
	// Waiting for the workload-variant-autoscaler pods to be ready and for leader election
	By("waiting for the controller-manager pods to be ready")
	Eventually(func(g Gomega) {
		podList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler"})
		if err != nil {
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list manager pods labelled")
		}
		g.Expect(podList.Items).NotTo(BeEmpty(), "Pod list should not be empty")
		for _, pod := range podList.Items {
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}
	}, 2*time.Minute, 1*time.Second).Should(Succeed())

	By("waiting for the controller-manager to acquire lease")
	Eventually(func(g Gomega) {
		leaseList, err := k8sClient.CoordinationV1().Leases(controllerNamespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to get leases")
		}
		g.Expect(leaseList.Items).NotTo(BeEmpty(), "Lease list should not be empty")
		for _, lease := range leaseList.Items {
			g.Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity should not be nil")
			g.Expect(*lease.Spec.HolderIdentity).To(ContainSubstring("controller-manager"), "Lease holderIdentity is not correct")
		}
	}, 2*time.Minute, 1*time.Second).Should(Succeed())
}

// configureController configures the controller by updating ConfigMaps and restarting pods.
func configureController(KvCacheThreshold, QueueLengthThreshold, kvSpareTrigger, queueSpareTrigger float64) {
	// Verify configuration for saturation-based mode
	By("verifying ConfigMap is accessible")
	cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(context.Background(), WVAConfigMapName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Should be able to get ConfigMap: "+WVAConfigMapName)

	if cm.Data["WVA_SCALE_TO_ZERO"] == "true" {
		MinimumReplicas = 0
	}

	// Update saturation-scaling ConfigMap with relaxed thresholds for easy scale-down testing
	By("updating saturation-scaling ConfigMap with relaxed thresholds")
	saturationCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(context.Background(), saturationConfigMapName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Should be able to get ConfigMap: "+saturationConfigMapName)

	// Relaxed configuration for easy scale-down:
	// - Lower saturation thresholds means more replicas are considered "non-saturated"
	// - Higher spare triggers means more headroom required after scale-down
	saturationCM.Data["default"] = fmt.Sprintf(`kvCacheThreshold: %.2f
queueLengthThreshold: %.2f
kvSpareTrigger: %.2f
queueSpareTrigger: %.2f`, KvCacheThreshold, QueueLengthThreshold, kvSpareTrigger, queueSpareTrigger)

	_, err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Update(context.Background(), saturationCM, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred(), "Should be able to update ConfigMap: "+saturationConfigMapName)

	_, _ = fmt.Fprintf(GinkgoWriter, "Updated saturation-scaling-config with relaxed thresholds: kvCache=%.2f, queue=%.2f, kvSpare=%.2f, queueSpare=%.2f\n", KvCacheThreshold, QueueLengthThreshold, kvSpareTrigger, queueSpareTrigger)

	restartControllerPods()
}

// restartControllerPods restarts the controller-manager pods to pick up new configuration.
func restartControllerPods() {
	By("restarting controller-manager pods to load new saturation configuration")
	podList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
	})
	Expect(err).NotTo(HaveOccurred(), "Should be able to list manager pods")

	for _, pod := range podList.Items {
		err = k8sClient.CoreV1().Pods(controllerNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Should be able to delete pod %s", pod.Name))
	}

	// Wait for new controller pods to be running
	Eventually(func(g Gomega) {
		newPodList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
		})
		g.Expect(err).NotTo(HaveOccurred(), "Should be able to list manager pods")
		g.Expect(newPodList.Items).NotTo(BeEmpty(), "Pod list should not be empty")
		for _, pod := range newPodList.Items {
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}
	}, 2*time.Minute, 1*time.Second).Should(Succeed())

	_, _ = fmt.Fprintf(GinkgoWriter, "Controller pods restarted and running with new saturation configuration\n")
}

// setupCertManager sets up CertManager if not already installed.
func setupCertManager() {
	// The tests-e2e are intended to run on a temporary cluster that is created and destroyed for testing.
	// To prevent errors when tests run in environments with CertManager already installed,
	// we check for its presence before execution.
	// Setup CertManager before the suite if not skipped and if not already installed
	if !skipCertManagerInstall {
		By("checking if cert manager is installed already")
		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
		if !isCertManagerAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing CertManager...\n")
			Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CertManager is already installed. Skipping installation...\n")
		}
	}
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
