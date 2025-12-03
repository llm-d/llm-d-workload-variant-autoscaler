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

package e2ecapacity

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
	projectImage = "ghcr.io/llm-d/workload-variant-autoscaler:0.0.1-test"

	MinimumReplicas = 1
)

const (
	maximumAvailableGPUs = 4
	numNodes             = 3
	gpuTypes             = "mix"
)

// TestCapacityE2E runs the end-to-end (e2e) test suite for capacity-based (saturation-based) mode.
// This suite validates reactive capacity-based scaling behavior using KV cache and queue metrics.
func TestCapacityE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting workload-variant-autoscaler capacity-based test suite\n")
	RunSpecs(t, "e2e capacity suite")
}

var _ = BeforeSuite(func() {
	By("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

	By("exporting environment variables for deployment")
	utils.SetupTestEnvironment(projectImage, numNodes, maximumAvailableGPUs, gpuTypes)

	// Deploy llm-d and workload-variant-autoscaler on the Kind cluster
	By("deploying Workload Variant Autoscaler on Kind")
	launchCmd := exec.Command("make", "deploy-wva-emulated-on-kind", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(launchCmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install llm-d and workload-variant-autoscaler")

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

	// Verify configuration for capacity-based mode
	By("verifying ConfigMap has saturation-only mode configured")
	cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(context.Background(), WVAConfigMapName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Should be able to get ConfigMap: "+WVAConfigMapName)

	// Verify saturation-only mode is set
	flagHybridOptimization := cm.Data["EXPERIMENTAL_HYBRID_OPTIMIZATION"]
	Expect(flagHybridOptimization).To(Or(Equal("off"), Equal("")), fmt.Sprintf("EXPERIMENTAL_HYBRID_OPTIMIZATION should be 'off' or unset (default) for capacity-based tests, got: %s", flagHybridOptimization))

	_, _ = fmt.Fprintf(GinkgoWriter, "Capacity-based mode verified: EXPERIMENTAL_HYBRID_OPTIMIZATION=%s\n",
		cm.Data["EXPERIMENTAL_HYBRID_OPTIMIZATION"])

	if cm.Data["WVA_SCALE_TO_ZERO"] == "true" {
		MinimumReplicas = 0
	}

	// Update capacity-scaling ConfigMap with relaxed thresholds for easy scale-down testing
	By("updating capacity-scaling ConfigMap with relaxed thresholds")
	capacityCM, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(context.Background(), CapacityConfigMapName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Should be able to get ConfigMap: "+CapacityConfigMapName)

	// Relaxed configuration for easy scale-down:
	// - Lower saturation thresholds means more replicas are considered "non-saturated"
	// - Higher spare triggers means more headroom required after scale-down
	capacityCM.Data["default"] = fmt.Sprintf(`kvCacheThreshold: %.2f
queueLengthThreshold: %.2f
kvSpareTrigger: %.2f
queueSpareTrigger: %.2f`, KvCacheThreshold, QueueLengthThreshold, kvSpareTrigger, queueSpareTrigger)

	_, err = k8sClient.CoreV1().ConfigMaps(controllerNamespace).Update(context.Background(), capacityCM, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred(), "Should be able to update ConfigMap: "+CapacityConfigMapName)

	_, _ = fmt.Fprintf(GinkgoWriter, "Updated capacity-scaling-config with relaxed thresholds: kvCache=%.2f, queue=%.2f, kvSpare=%.2f, queueSpare=%.2f\n", KvCacheThreshold, QueueLengthThreshold, kvSpareTrigger, queueSpareTrigger)

	// Restart controller pods to pick up new capacity-scaling configuration
	By("restarting controller-manager pods to load new capacity configuration")
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

	_, _ = fmt.Fprintf(GinkgoWriter, "Controller pods restarted and running with new capacity configuration\n")

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
})

var _ = AfterSuite(func() {
	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}

	// Destroy the Kind cluster
	cmd := exec.Command("bash", "deploy/kind-emulator/teardown.sh")
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to destroy Kind cluster")
})
