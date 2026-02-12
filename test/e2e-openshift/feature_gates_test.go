package e2eopenshift

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/test/utils"
)

// isHPAScaleToZeroEnabled is a package-level wrapper for the shared utility function.
func isHPAScaleToZeroEnabled(ctx context.Context) bool {
	return utils.IsHPAScaleToZeroEnabled(ctx, k8sClient, GinkgoWriter)
}
