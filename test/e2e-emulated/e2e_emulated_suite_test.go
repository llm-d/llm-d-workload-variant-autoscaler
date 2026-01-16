package e2eemulated

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestEmulatedE2E runs the end-to-end (e2e) test suite for emulated mode.
func TestEmulatedE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting workload-variant-autoscaler emulated test suite\n")
	RunSpecs(t, "e2e emulated suite")
}

var _ = BeforeSuite(func() {
	setupInfrastructure()

	configureController(KvCacheThreshold, QueueLengthThreshold, kvSpareTrigger, queueSpareTrigger)
})

var _ = AfterSuite(func() {
	teardownInfrastructure()
})
