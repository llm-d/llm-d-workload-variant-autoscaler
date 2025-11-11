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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
)

// getProjectImage returns the controller image to use for e2e tests.
// It checks the E2E_IMG environment variable first, otherwise defaults to the test image.
func getProjectImage() string {
	if img := os.Getenv("E2E_IMG"); img != "" {
		return img
	}
	return "quay.io/infernoautoscaler/inferno-controller:0.0.1-test"
}

var (
	// Optional Environment Variables:
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// - KEDA_INSTALL_SKIP=true: Skips KEDA installation during test setup.
	// - SKIP_KIND_DEPLOY=true: Skips KIND cluster creation and deployment. Useful when running
	//   in CI/CD where the cluster is already created and controller is already deployed.
	// - SKIP_DOCKER_BUILD=true: Skips building the controller Docker image. Useful when using
	//   a pre-built image from a registry.
	// - E2E_IMG: Override the controller image to use for e2e tests. If not set, defaults to
	//   building and using "quay.io/infernoautoscaler/inferno-controller:0.0.1-test".
	// These variables are useful if CertManager/KEDA is already installed, avoiding
	// re-installation and conflicts.
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	skipKEDAInstall        = os.Getenv("KEDA_INSTALL_SKIP") == "true"
	skipKindDeploy         = os.Getenv("SKIP_KIND_DEPLOY") == "true"
	skipDockerBuild        = os.Getenv("SKIP_DOCKER_BUILD") == "true"
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false
	// isKEDAAlreadyInstalled will be set true when KEDA CRDs be found on the cluster
	isKEDAAlreadyInstalled = false

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	// Can be overridden by setting E2E_IMG environment variable.
	projectImage = getProjectImage()

	MinimumReplicas = 1
)

const (
	maximumAvailableGPUs = 4
	numNodes             = 3
	gpuTypes             = "mix"
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purposed to be used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager and KEDA.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting workload-variant-autoscaler integration test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	if !skipKindDeploy {
		if !skipDockerBuild {
			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "SKIP_DOCKER_BUILD=true: Skipping Docker image build\n")
			_, _ = fmt.Fprintf(GinkgoWriter, "Using pre-built image: %s\n", projectImage)
		}

		By("exporting environment variables for deployment")
		utils.SetupTestEnvironment(projectImage, numNodes, maximumAvailableGPUs, gpuTypes)

		// Deploy llm-d and workload-variant-autoscaler on the Kind cluster
		launchCmd := exec.Command("make", "deploy-llm-d-wva-emulated-on-kind", fmt.Sprintf("IMG=%s", projectImage))
		_, err := utils.Run(launchCmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install llm-d and workload-variant-autoscaler")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "SKIP_KIND_DEPLOY=true: Skipping KIND cluster creation and deployment\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "Assuming cluster is already running with controller deployed\n")
	}
	initializeK8sClient()

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

	// Restart Prometheus Adapter to ensure it discovers the new wva_* metrics
	// Prometheus Adapter caches metric discovery at startup, so it needs to be restarted
	// after the controller starts emitting metrics
	By("restarting Prometheus Adapter to discover new metrics")
	_, _ = fmt.Fprintf(GinkgoWriter, "Restarting Prometheus Adapter pods to refresh metric discovery...\n")
	adapterPods, err := k8sClient.CoreV1().Pods("workload-variant-autoscaler-monitoring").List(
		context.Background(),
		metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=prometheus-adapter"},
	)
	if err == nil && len(adapterPods.Items) > 0 {
		for _, pod := range adapterPods.Items {
			_ = k8sClient.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		}
		// Wait for new pods to be ready
		Eventually(func(g Gomega) {
			pods, err := k8sClient.CoreV1().Pods("workload-variant-autoscaler-monitoring").List(
				context.Background(),
				metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=prometheus-adapter"},
			)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(pods.Items)).To(BeNumerically(">", 0), "At least one Prometheus Adapter pod should exist")
			for _, pod := range pods.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s should be running", pod.Name))
				g.Expect(len(pod.Status.ContainerStatuses)).To(BeNumerically(">", 0), "Pod should have containers")
				g.Expect(pod.Status.ContainerStatuses[0].Ready).To(BeTrue(), fmt.Sprintf("Pod %s should be ready", pod.Name))
			}
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
		_, _ = fmt.Fprintf(GinkgoWriter, "✓ Prometheus Adapter restarted and ready\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "⚠ Prometheus Adapter not found, skipping restart\n")
	}

	// Set MinimumReplicas to 0 if WVA_SCALE_TO_ZERO is true in the ConfigMap
	cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(context.Background(), "workload-variant-autoscaler-variantautoscaling-config", metav1.GetOptions{})
	if err != nil {
		Fail("Failed to get ConfigMap: " + err.Error())
	}
	if cm.Data["WVA_SCALE_TO_ZERO"] == "true" {
		MinimumReplicas = 0
	}

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

	// Setup KEDA before the suite if not skipped and if not already installed
	if !skipKEDAInstall {
		By("checking if KEDA is installed already")
		isKEDAAlreadyInstalled = utils.IsKEDAInstalled()
		if !isKEDAAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing KEDA...\n")
			Expect(utils.InstallKEDA()).To(Succeed(), "Failed to install KEDA")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: KEDA is already installed. Skipping installation...\n")
		}
	}
})

// ReportAfterEach runs after each test spec and captures diagnostics on failure
var _ = ReportAfterEach(func(report SpecReport) {
	if report.Failed() {
		_, _ = fmt.Fprintf(GinkgoWriter, "\n\n========================================\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "🔍 Test Failed - Running Diagnostics\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")

		// Run diagnostic script if it exists
		if _, err := os.Stat("../../test/utils/ci_diagnostics.sh"); err == nil {
			cmd := exec.Command("bash", "../../test/utils/ci_diagnostics.sh")
			output, _ := cmd.CombinedOutput()
			_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", string(output))
		} else {
			// Fallback: Collect critical info directly
			_, _ = fmt.Fprintf(GinkgoWriter, "Diagnostic script not found, collecting basic info...\n\n")

			// Collect controller logs
			_, _ = fmt.Fprintf(GinkgoWriter, "=== Controller Logs (last 100 lines) ===\n")
			logCmd := exec.Command("kubectl", "logs", "-n", controllerNamespace,
				"-l", "app.kubernetes.io/name=workload-variant-autoscaler",
				"--tail=100", "--timestamps")
			if logOutput, err := logCmd.CombinedOutput(); err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", string(logOutput))
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Could not fetch controller logs: %v\n", err)
			}

			// Collect VariantAutoscaling resources
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== VariantAutoscaling Resources ===\n")
			vaCmd := exec.Command("kubectl", "get", "variantautoscaling", "-A", "-o", "yaml")
			if vaOutput, err := vaCmd.CombinedOutput(); err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", string(vaOutput))
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Could not fetch VAs: %v\n", err)
			}
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "\n========================================\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "Diagnostics Complete\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "========================================\n\n")
	}
})

var _ = AfterSuite(func() {
	// Teardown KEDA after the suite if not skipped and if it was not already installed
	if !skipKEDAInstall && !isKEDAAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling KEDA...\n")
		utils.UninstallKEDA()
	}

	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}

	// Destroy the Kind cluster only if we created it
	if !skipKindDeploy {
		cmd := exec.Command("bash", "deploy/kind-emulator/teardown.sh")
		_, err := utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to destroy Kind cluster")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "SKIP_KIND_DEPLOY=true: Skipping KIND cluster deletion (managed by CI/CD)\n")
	}
})
