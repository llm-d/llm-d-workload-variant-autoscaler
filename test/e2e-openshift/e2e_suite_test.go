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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/llm-d/llm-d-workload-variant-autoscaler/api/v1alpha1"
)

// Shared test configuration constants
const (
	// Scale-to-zero ConfigMap name - used by both scale-to-zero and scale-from-zero tests.
	// Scale-from-zero tests require this because they need the deployment to first scale TO zero
	// (when idle) before testing scaling FROM zero (when requests arrive).
	// Use config package constant to ensure consistency with controller expectations.
	scaleToZeroConfigMapName = config.DefaultScaleToZeroConfigMapName
)

var (
	controllerNamespace = getEnvString("CONTROLLER_NAMESPACE", "workload-variant-autoscaler-system")
	monitoringNamespace = getEnvString("MONITORING_NAMESPACE", "openshift-user-workload-monitoring")
	llmDNamespace       = getEnvString("LLMD_NAMESPACE", "llm-d-inference-scheduler")
	// Secondary llm-d namespace for Model B (multi-model testing)
	llmDNamespaceB = getEnvString("LLMD_NAMESPACE_B", "")
	gatewayName    = getEnvString("GATEWAY_NAME", "") // Auto-derived from namespace if empty
	modelID        = getEnvString("MODEL_ID", "unsloth/Meta-Llama-3.1-8B")
	deployment     = getEnvString("DEPLOYMENT", "") // Auto-derived from namespace if empty
	requestRate    = getEnvInt("REQUEST_RATE", 20)
	numPrompts     = getEnvInt("NUM_PROMPTS", 3000)
	multiModelMode = llmDNamespaceB != ""
)

// discoverPathNameFromDeployments discovers the path name from existing deployments in the namespace.
// It looks for deployments with pattern: ms-{path-name}-llm-d-modelservice-decode
// and extracts the path name from the deployment name.
// Returns empty string if discovery fails (k8sClient not available or no deployments found).
func discoverPathNameFromDeployments(namespace string) string {
	if k8sClient == nil {
		return ""
	}

	ctx := context.Background()
	// List all deployments in the namespace and find ones matching the pattern
	deployments, err := k8sClient.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil || len(deployments.Items) == 0 {
		return ""
	}

	// Look for deployment matching pattern: ms-{path-name}-llm-d-modelservice-decode
	for _, deployment := range deployments.Items {
		deploymentName := deployment.Name
		if strings.HasPrefix(deploymentName, "ms-") && strings.HasSuffix(deploymentName, "-llm-d-modelservice-decode") {
			pathNameFromDeployment := deploymentName[len("ms-") : len(deploymentName)-len("-llm-d-modelservice-decode")]
			_, _ = fmt.Fprintf(GinkgoWriter, "Discovered path name '%s' from deployment '%s' in namespace '%s'\n",
				pathNameFromDeployment, deploymentName, namespace)
			return pathNameFromDeployment
		}
	}

	return ""
}

// deriveDeploymentName discovers the deployment name from existing deployments in the namespace.
// Falls back to namespace suffix if discovery fails.
func deriveDeploymentName(namespace string) string {
	// Try to discover from existing deployments
	pathName := discoverPathNameFromDeployments(namespace)
	if pathName != "" {
		return fmt.Sprintf("ms-%s-llm-d-modelservice-decode", pathName)
	}

	// Fallback: use namespace suffix
	const prefix = "llm-d-"
	if strings.HasPrefix(namespace, prefix) {
		pathName = namespace[len(prefix):]
	} else {
		pathName = namespace
	}
	return fmt.Sprintf("ms-%s-llm-d-modelservice-decode", pathName)
}

// deriveGatewayName discovers the gateway name from existing deployments in the namespace.
// Falls back to namespace suffix if discovery fails.
func deriveGatewayName(namespace string) string {
	// Try to discover from existing deployments
	pathName := discoverPathNameFromDeployments(namespace)
	if pathName != "" {
		return fmt.Sprintf("infra-%s-inference-gateway-istio", pathName)
	}

	// Fallback: use namespace suffix
	const prefix = "llm-d-"
	if strings.HasPrefix(namespace, prefix) {
		pathName = namespace[len(prefix):]
	} else {
		pathName = namespace
	}
	return fmt.Sprintf("infra-%s-inference-gateway-istio", pathName)
}

// getDeploymentName returns the deployment name, auto-deriving from namespace if not set
func getDeploymentName() string {
	if deployment != "" {
		return deployment
	}
	return deriveDeploymentName(llmDNamespace)
}

// getGatewayName returns the gateway name, auto-deriving from namespace if not set
func getGatewayName() string {
	if gatewayName != "" {
		return gatewayName
	}
	return deriveGatewayName(llmDNamespace)
}

var (
	k8sClient *kubernetes.Clientset
	crClient  client.Client
	scheme    = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func getEnvString(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Failed to parse env variable, using fallback")
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to parse env variable as int, using fallback")
		return fallback
	}
	return fallback
}

// TestE2EOpenShift runs the end-to-end (e2e) test suite for OpenShift deployments.
// These tests assume that the infrastructure (WVA, llm-d, Prometheus, etc.) is already deployed.
func TestE2EOpenShift(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting workload-variant-autoscaler OpenShift integration test suite\n")
	RunSpecs(t, "e2e-openshift suite")
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

var _ = BeforeSuite(func() {
	// Verify cluster connectivity before proceeding
	initializeK8sClient()

	ctx := context.Background()
	_, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		Fail(fmt.Sprintf("Cannot connect to cluster: %v. Ensure KUBECONFIG is set or the test is running with in-cluster authentication.", err))
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "Using the following configuration:\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "CONTROLLER_NAMESPACE=%s\n", controllerNamespace)

	// Verify namespace matches controller expectations
	// Note: Controller uses config.SystemNamespace() which checks POD_NAMESPACE env var
	// or defaults to "workload-variant-autoscaler-system"
	// Ensure CONTROLLER_NAMESPACE matches the actual controller deployment namespace
	expectedSystemNamespace := config.SystemNamespace()
	if controllerNamespace != expectedSystemNamespace {
		_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CONTROLLER_NAMESPACE (%s) does not match controller's expected namespace (%s)\n",
			controllerNamespace, expectedSystemNamespace)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Controller uses POD_NAMESPACE env var if set, otherwise defaults to %s\n",
			config.DefaultNamespace)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Ensure CONTROLLER_NAMESPACE matches the actual controller deployment namespace\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "EXPECTED_SYSTEM_NAMESPACE=%s (matches CONTROLLER_NAMESPACE)\n", expectedSystemNamespace)
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "MONITORING_NAMESPACE=%s\n", monitoringNamespace)
	_, _ = fmt.Fprintf(GinkgoWriter, "LLMD_NAMESPACE=%s\n", llmDNamespace)
	if multiModelMode {
		_, _ = fmt.Fprintf(GinkgoWriter, "LLMD_NAMESPACE_B=%s (multi-model mode enabled)\n", llmDNamespaceB)
	}
	// Auto-derive gateway and deployment names from namespace if not explicitly set
	// Note: This is called after k8sClient is initialized, so discovery will work
	actualGatewayName := getGatewayName()
	actualDeploymentName := getDeploymentName()

	_, _ = fmt.Fprintf(GinkgoWriter, "GATEWAY_NAME=%s (derived from namespace: %s)\n", actualGatewayName, llmDNamespace)
	_, _ = fmt.Fprintf(GinkgoWriter, "MODEL_ID=%s\n", modelID)
	_, _ = fmt.Fprintf(GinkgoWriter, "DEPLOYMENT=%s (derived from namespace: %s)\n", actualDeploymentName, llmDNamespace)
	_, _ = fmt.Fprintf(GinkgoWriter, "REQUEST_RATE=%d\n", requestRate)
	_, _ = fmt.Fprintf(GinkgoWriter, "NUM_PROMPTS=%d\n", numPrompts)

	By("verifying that the controller-manager pods are running")
	Eventually(func(g Gomega) {
		podList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
		})
		if err != nil {
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list manager pods")
		}
		g.Expect(podList.Items).NotTo(BeEmpty(), "Pod list should not be empty")
		for _, pod := range podList.Items {
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s is not running", pod.Name))
		}
	}, 2*time.Minute, 1*time.Second).Should(Succeed())

	By("verifying that llm-d infrastructure (Model A1) is running")
	Eventually(func(g Gomega) {
		// Check Gateway
		deploymentList, err := k8sClient.AppsV1().Deployments(llmDNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list deployments in llm-d namespace")
		}
		g.Expect(deploymentList.Items).NotTo(BeEmpty(), "llm-d deployments should exist")

		// Check that vLLM deployment exists
		deploymentName := getDeploymentName()
		vllmDeployment, err := k8sClient.AppsV1().Deployments(llmDNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("vLLM deployment %s should exist in namespace %s", deploymentName, llmDNamespace))
		}
		g.Expect(vllmDeployment.Status.ReadyReplicas).To(BeNumerically(">", 0), "At least one vLLM replica should be ready")
	}, 5*time.Minute, 5*time.Second).Should(Succeed())

	// Verify multi-model infrastructure if enabled
	if multiModelMode {
		By("verifying that Model B infrastructure is running")
		Eventually(func(g Gomega) {
			// Check that Model B namespace has deployments
			deploymentList, err := k8sClient.AppsV1().Deployments(llmDNamespaceB).List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list deployments in Model B namespace")
			g.Expect(deploymentList.Items).NotTo(BeEmpty(), "Model B deployments should exist")

			// Check that Model B vLLM deployment exists
			// For Model B, derive deployment name from its namespace
			modelBDeploymentName := deriveDeploymentName(llmDNamespaceB)
			vllmDeployment, err := k8sClient.AppsV1().Deployments(llmDNamespaceB).Get(ctx, modelBDeploymentName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Model B vLLM deployment %s should exist in namespace %s", modelBDeploymentName, llmDNamespaceB))
			g.Expect(vllmDeployment.Status.ReadyReplicas).To(BeNumerically(">", 0), "At least one Model B replica should be ready")
		}, 5*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying WVA resources for all models")
		Eventually(func(g Gomega) {
			// Check that VariantAutoscaling resources exist for all models
			vaList := &v1alpha1.VariantAutoscalingList{}
			err := crClient.List(ctx, vaList, client.MatchingLabels{
				"app.kubernetes.io/name": "workload-variant-autoscaler",
			})
			g.Expect(err).NotTo(HaveOccurred(), "Should be able to list VariantAutoscalings")
			// Expect at least 2 VAs: Model A1 and Model B
			_, _ = fmt.Fprintf(GinkgoWriter, "Found %d VariantAutoscaling resources\n", len(vaList.Items))
			g.Expect(len(vaList.Items)).To(BeNumerically(">=", 2), "Should have at least 2 VariantAutoscaling resources for multi-model mode")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
	}

	By("verifying that Prometheus Adapter is running")
	// Note: Prometheus Adapter is required for HPA-based tests but not for PodScrapingSource tests
	// We check it but don't fail the entire suite if it's not ready (PodScrapingSource tests can still run)
	Eventually(func(g Gomega) {
		podList, err := k8sClient.CoreV1().Pods(monitoringNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=prometheus-adapter",
		})
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: Could not list Prometheus Adapter pods: %v\n", err)
			return // Don't fail, just log warning
		}
		if len(podList.Items) == 0 {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: No Prometheus Adapter pods found (required for HPA tests, not for PodScrapingSource tests)\n")
			return // Don't fail, just log warning
		}
		allRunning := true
		for _, pod := range podList.Items {
			if pod.Status.Phase != corev1.PodRunning {
				allRunning = false
				_, _ = fmt.Fprintf(GinkgoWriter, "Warning: Prometheus Adapter pod %s is not running (status: %s)\n", pod.Name, pod.Status.Phase)
			}
		}
		if allRunning {
			_, _ = fmt.Fprintf(GinkgoWriter, "Prometheus Adapter is running\n")
		}
	}, 2*time.Minute, 1*time.Second).Should(Succeed())

	_, _ = fmt.Fprintf(GinkgoWriter, "Infrastructure verification complete\n")
})

var _ = AfterSuite(func() {
	_, _ = fmt.Fprintf(GinkgoWriter, "OpenShift e2e test suite completed\n")
})
