package pipeline

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/config"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
)

// boolPtr is a helper to create a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}

var _ = Describe("Enforcer", func() {
	var (
		ctx             context.Context
		enforcer        *Enforcer
		targets         map[string]int
		variantAnalyses []interfaces.VariantSaturationAnalysis
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("EnforcePolicy", func() {
		Context("when scale-to-zero is enabled", func() {
			Context("and there are no requests", func() {
				BeforeEach(func() {
					enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
						return 0, nil
					})
					targets = map[string]int{
						"variant-a": 2,
						"variant-b": 1,
					}
					variantAnalyses = []interfaces.VariantSaturationAnalysis{
						{VariantName: "variant-a", Cost: 1.0},
						{VariantName: "variant-b", Cost: 2.0},
					}
				})

				It("should scale all variants to zero", func() {
					scaleToZeroConfig := config.ScaleToZeroConfigData{
						"test-model": {
							EnableScaleToZero: boolPtr(true),
							RetentionPeriod:   "10m",
						},
					}

					result, applied := enforcer.EnforcePolicy(
						ctx,
						"test-model",
						"test-ns",
						targets,
						variantAnalyses,
						scaleToZeroConfig,
					)

					Expect(applied).To(BeTrue())
					Expect(result["variant-a"]).To(Equal(0))
					Expect(result["variant-b"]).To(Equal(0))
				})
			})

			Context("and there are requests", func() {
				BeforeEach(func() {
					enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
						return 10, nil
					})
					targets = map[string]int{
						"variant-a": 2,
						"variant-b": 1,
					}
					variantAnalyses = []interfaces.VariantSaturationAnalysis{
						{VariantName: "variant-a", Cost: 1.0},
						{VariantName: "variant-b", Cost: 2.0},
					}
				})

				It("should keep targets unchanged", func() {
					scaleToZeroConfig := config.ScaleToZeroConfigData{
						"test-model": {
							EnableScaleToZero: boolPtr(true),
							RetentionPeriod:   "10m",
						},
					}

					result, applied := enforcer.EnforcePolicy(
						ctx,
						"test-model",
						"test-ns",
						targets,
						variantAnalyses,
						scaleToZeroConfig,
					)

					Expect(applied).To(BeFalse())
					Expect(result["variant-a"]).To(Equal(2))
					Expect(result["variant-b"]).To(Equal(1))
				})
			})

			Context("and request count query fails", func() {
				BeforeEach(func() {
					enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
						return 0, errors.New("prometheus unavailable")
					})
					targets = map[string]int{
						"variant-a": 2,
						"variant-b": 1,
					}
					variantAnalyses = []interfaces.VariantSaturationAnalysis{
						{VariantName: "variant-a", Cost: 1.0},
						{VariantName: "variant-b", Cost: 2.0},
					}
				})

				It("should keep targets unchanged", func() {
					scaleToZeroConfig := config.ScaleToZeroConfigData{
						"test-model": {
							EnableScaleToZero: boolPtr(true),
							RetentionPeriod:   "10m",
						},
					}

					result, applied := enforcer.EnforcePolicy(
						ctx,
						"test-model",
						"test-ns",
						targets,
						variantAnalyses,
						scaleToZeroConfig,
					)

					Expect(applied).To(BeFalse())
					Expect(result["variant-a"]).To(Equal(2))
					Expect(result["variant-b"]).To(Equal(1))
				})
			})
		})

		Context("when scale-to-zero is disabled", func() {
			BeforeEach(func() {
				enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
					return 0, nil
				})
			})

			Context("and all targets are zero", func() {
				BeforeEach(func() {
					targets = map[string]int{
						"variant-a": 0,
						"variant-b": 0,
					}
					variantAnalyses = []interfaces.VariantSaturationAnalysis{
						{VariantName: "variant-a", Cost: 2.0},
						{VariantName: "variant-b", Cost: 1.0}, // Cheaper
					}
				})

				It("should preserve minimum replica on the cheapest variant", func() {
					scaleToZeroConfig := config.ScaleToZeroConfigData{
						"test-model": {
							EnableScaleToZero: boolPtr(false),
						},
					}

					result, applied := enforcer.EnforcePolicy(
						ctx,
						"test-model",
						"test-ns",
						targets,
						variantAnalyses,
						scaleToZeroConfig,
					)

					Expect(applied).To(BeTrue())
					Expect(result["variant-a"]).To(Equal(0))
					Expect(result["variant-b"]).To(Equal(1))
				})
			})

			Context("and some targets have replicas", func() {
				BeforeEach(func() {
					targets = map[string]int{
						"variant-a": 2,
						"variant-b": 0,
					}
					variantAnalyses = []interfaces.VariantSaturationAnalysis{
						{VariantName: "variant-a", Cost: 2.0},
						{VariantName: "variant-b", Cost: 1.0},
					}
				})

				It("should keep targets unchanged", func() {
					scaleToZeroConfig := config.ScaleToZeroConfigData{
						"test-model": {
							EnableScaleToZero: boolPtr(false),
						},
					}

					result, applied := enforcer.EnforcePolicy(
						ctx,
						"test-model",
						"test-ns",
						targets,
						variantAnalyses,
						scaleToZeroConfig,
					)

					Expect(applied).To(BeFalse())
					Expect(result["variant-a"]).To(Equal(2))
					Expect(result["variant-b"]).To(Equal(0))
				})
			})
		})

		Context("when model is not in config", func() {
			BeforeEach(func() {
				enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
					return 0, nil
				})
				targets = map[string]int{
					"variant-a": 0,
					"variant-b": 0,
				}
				variantAnalyses = []interfaces.VariantSaturationAnalysis{
					{VariantName: "variant-a", Cost: 2.0},
					{VariantName: "variant-b", Cost: 1.0},
				}
			})

			It("should default to scale-to-zero disabled and preserve minimum", func() {
				scaleToZeroConfig := config.ScaleToZeroConfigData{
					"other-model": {
						EnableScaleToZero: boolPtr(true),
					},
				}

				result, applied := enforcer.EnforcePolicy(
					ctx,
					"test-model",
					"test-ns",
					targets,
					variantAnalyses,
					scaleToZeroConfig,
				)

				Expect(applied).To(BeTrue())
				Expect(result["variant-b"]).To(Equal(1))
			})
		})

		Context("when variants have equal cost", func() {
			BeforeEach(func() {
				enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
					return 0, nil
				})
				targets = map[string]int{
					"variant-z": 0,
					"variant-a": 0,
				}
				variantAnalyses = []interfaces.VariantSaturationAnalysis{
					{VariantName: "variant-z", Cost: 1.0},
					{VariantName: "variant-a", Cost: 1.0},
				}
			})

			It("should use alphabetical order as tiebreaker", func() {
				scaleToZeroConfig := config.ScaleToZeroConfigData{
					"test-model": {
						EnableScaleToZero: boolPtr(false),
					},
				}

				result, applied := enforcer.EnforcePolicy(
					ctx,
					"test-model",
					"test-ns",
					targets,
					variantAnalyses,
					scaleToZeroConfig,
				)

				Expect(applied).To(BeTrue())
				Expect(result["variant-a"]).To(Equal(1))
				Expect(result["variant-z"]).To(Equal(0))
			})
		})

		Context("when variant cost is missing from analysis", func() {
			BeforeEach(func() {
				enforcer = NewEnforcer(func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error) {
					return 0, nil
				})
				targets = map[string]int{
					"variant-a":       0,
					"variant-missing": 0,
				}
				variantAnalyses = []interfaces.VariantSaturationAnalysis{
					{VariantName: "variant-a", Cost: 100.0}, // Very expensive
					// variant-missing not in analysis - uses saturation.DefaultVariantCost (10.0)
				}
			})

			It("should use default cost for missing variants", func() {
				scaleToZeroConfig := config.ScaleToZeroConfigData{
					"test-model": {
						EnableScaleToZero: boolPtr(false),
					},
				}

				result, applied := enforcer.EnforcePolicy(
					ctx,
					"test-model",
					"test-ns",
					targets,
					variantAnalyses,
					scaleToZeroConfig,
				)

				Expect(applied).To(BeTrue())
				Expect(result["variant-a"]).To(Equal(0))
				Expect(result["variant-missing"]).To(Equal(1))
			})
		})
	})
})
