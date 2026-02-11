package saturation

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/engines/pipeline"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
)

var _ = Describe("V2 Engine Integration", func() {

	Context("extractTargetsFromDecisions", func() {

		It("should extract targets for specific model", func() {
			decisions := []interfaces.VariantDecision{
				{VariantName: "v1", ModelID: "model-1", Namespace: "ns-1", TargetReplicas: 3},
				{VariantName: "v2", ModelID: "model-1", Namespace: "ns-1", TargetReplicas: 2},
				{VariantName: "v3", ModelID: "model-2", Namespace: "ns-1", TargetReplicas: 5},
			}

			targets := extractTargetsFromDecisions(decisions, "model-1", "ns-1")

			Expect(targets).To(HaveLen(2))
			Expect(targets["v1"]).To(Equal(3))
			Expect(targets["v2"]).To(Equal(2))
		})

		It("should return empty map when no decisions match", func() {
			decisions := []interfaces.VariantDecision{
				{VariantName: "v1", ModelID: "model-1", Namespace: "ns-1", TargetReplicas: 3},
			}

			targets := extractTargetsFromDecisions(decisions, "model-2", "ns-1")

			Expect(targets).To(BeEmpty())
		})
	})

	Context("buildVariantAnalysesFromDecisions", func() {

		It("should build variant analyses with cost info", func() {
			decisions := []interfaces.VariantDecision{
				{VariantName: "v1", ModelID: "model-1", Namespace: "ns-1", AcceleratorName: "A100", Cost: 5.0, CurrentReplicas: 2},
				{VariantName: "v2", ModelID: "model-1", Namespace: "ns-1", AcceleratorName: "H100", Cost: 15.0, CurrentReplicas: 1},
				{VariantName: "v3", ModelID: "model-2", Namespace: "ns-1", AcceleratorName: "A100", Cost: 10.0, CurrentReplicas: 3},
			}

			analyses := buildVariantAnalysesFromDecisions(decisions, "model-1", "ns-1")

			Expect(analyses).To(HaveLen(2))
			Expect(analyses[0].VariantName).To(Equal("v1"))
			Expect(analyses[0].Cost).To(Equal(5.0))
			Expect(analyses[0].AcceleratorName).To(Equal("A100"))
			Expect(analyses[1].VariantName).To(Equal("v2"))
			Expect(analyses[1].Cost).To(Equal(15.0))
		})
	})

	Context("applyEnforcedTargetsToDecisions", func() {

		It("should update targets from enforcer", func() {
			decisions := []interfaces.VariantDecision{
				{VariantName: "v1", ModelID: "model-1", Namespace: "ns-1", CurrentReplicas: 2, TargetReplicas: 3, Action: interfaces.ActionScaleUp},
				{VariantName: "v2", ModelID: "model-1", Namespace: "ns-1", CurrentReplicas: 1, TargetReplicas: 1, Action: interfaces.ActionNoChange},
			}

			enforced := map[string]int{
				"v1": 0,
				"v2": 0,
			}

			result := applyEnforcedTargetsToDecisions(decisions, enforced, "model-1", "ns-1")

			Expect(result[0].TargetReplicas).To(Equal(0))
			Expect(result[0].Action).To(Equal(interfaces.ActionScaleDown))
			Expect(result[1].TargetReplicas).To(Equal(0))
			Expect(result[1].Action).To(Equal(interfaces.ActionScaleDown))
		})

		It("should not modify decisions for other models", func() {
			decisions := []interfaces.VariantDecision{
				{VariantName: "v1", ModelID: "model-1", Namespace: "ns-1", CurrentReplicas: 2, TargetReplicas: 3},
				{VariantName: "v2", ModelID: "model-2", Namespace: "ns-1", CurrentReplicas: 1, TargetReplicas: 2},
			}

			enforced := map[string]int{"v1": 0}

			result := applyEnforcedTargetsToDecisions(decisions, enforced, "model-1", "ns-1")

			Expect(result[0].TargetReplicas).To(Equal(0))
			Expect(result[1].TargetReplicas).To(Equal(2)) // unchanged
		})

		It("should not modify decisions when enforced matches current target", func() {
			decisions := []interfaces.VariantDecision{
				{VariantName: "v1", ModelID: "model-1", Namespace: "ns-1", CurrentReplicas: 2, TargetReplicas: 3, Action: interfaces.ActionScaleUp, Reason: "original"},
			}

			enforced := map[string]int{"v1": 3} // same as target

			result := applyEnforcedTargetsToDecisions(decisions, enforced, "model-1", "ns-1")

			Expect(result[0].TargetReplicas).To(Equal(3))
			Expect(result[0].Action).To(Equal(interfaces.ActionScaleUp))
			Expect(result[0].Reason).To(Equal("original"))
		})
	})

	Context("CostAwareOptimizer via engine path", func() {

		It("should scale up cheapest variant by cost-efficiency", func() {
			optimizer := pipeline.NewCostAwareOptimizer()
			requests := []pipeline.ModelScalingRequest{
				{
					ModelID:   "model-1",
					Namespace: "default",
					Result: &interfaces.AnalyzerResult{
						ModelID:          "model-1",
						Namespace:        "default",
						RequiredCapacity: 5000,
						VariantCapacities: []interfaces.VariantCapacity{
							{VariantName: "variant-cheap", AcceleratorName: "A100", Cost: 5.0, ReplicaCount: 2, PerReplicaCapacity: 10000},
							{VariantName: "variant-expensive", AcceleratorName: "H100", Cost: 15.0, ReplicaCount: 1, PerReplicaCapacity: 20000},
						},
					},
					VariantStates: []interfaces.VariantReplicaState{
						{VariantName: "variant-cheap", CurrentReplicas: 2},
						{VariantName: "variant-expensive", CurrentReplicas: 1},
					},
				},
			}

			decisions := optimizer.Optimize(context.Background(), requests, nil)

			dm := decisionsByVariant(decisions)
			// cost-efficiency: cheap=5/10000=0.0005, expensive=15/20000=0.00075
			// cheap is more cost-efficient, ceil(5000/10000)=1
			Expect(dm["variant-cheap"].TargetReplicas).To(Equal(3))
			Expect(dm["variant-expensive"].TargetReplicas).To(Equal(1))
		})

		It("should scale down most expensive variant", func() {
			optimizer := pipeline.NewCostAwareOptimizer()
			requests := []pipeline.ModelScalingRequest{
				{
					ModelID:   "model-1",
					Namespace: "default",
					Result: &interfaces.AnalyzerResult{
						ModelID:       "model-1",
						Namespace:     "default",
						SpareCapacity: 25000,
						VariantCapacities: []interfaces.VariantCapacity{
							{VariantName: "variant-cheap", Cost: 5.0, ReplicaCount: 3, PerReplicaCapacity: 10000},
							{VariantName: "variant-expensive", Cost: 15.0, ReplicaCount: 2, PerReplicaCapacity: 20000},
						},
					},
					VariantStates: []interfaces.VariantReplicaState{
						{VariantName: "variant-cheap", CurrentReplicas: 3},
						{VariantName: "variant-expensive", CurrentReplicas: 2},
					},
				},
			}

			decisions := optimizer.Optimize(context.Background(), requests, nil)

			dm := decisionsByVariant(decisions)
			Expect(dm["variant-expensive"].TargetReplicas).To(Equal(1))
			Expect(dm["variant-cheap"].TargetReplicas).To(Equal(3))
		})

		It("should protect cheapest variant at 1 during scale-down", func() {
			optimizer := pipeline.NewCostAwareOptimizer()
			requests := []pipeline.ModelScalingRequest{
				{
					ModelID:   "model-1",
					Namespace: "default",
					Result: &interfaces.AnalyzerResult{
						ModelID:       "model-1",
						Namespace:     "default",
						SpareCapacity: 30000,
						VariantCapacities: []interfaces.VariantCapacity{
							{VariantName: "variant-expensive", Cost: 15.0, ReplicaCount: 1, PerReplicaCapacity: 20000},
							{VariantName: "variant-cheap", Cost: 5.0, ReplicaCount: 1, PerReplicaCapacity: 10000},
						},
					},
					VariantStates: []interfaces.VariantReplicaState{
						{VariantName: "variant-expensive", CurrentReplicas: 1},
						{VariantName: "variant-cheap", CurrentReplicas: 1},
					},
				},
			}

			decisions := optimizer.Optimize(context.Background(), requests, nil)

			dm := decisionsByVariant(decisions)
			Expect(dm["variant-expensive"].TargetReplicas).To(Equal(0))
			Expect(dm["variant-cheap"].TargetReplicas).To(Equal(1))
		})

		It("should not skip variants with pending replicas", func() {
			optimizer := pipeline.NewCostAwareOptimizer()
			requests := []pipeline.ModelScalingRequest{
				{
					ModelID:   "model-1",
					Namespace: "default",
					Result: &interfaces.AnalyzerResult{
						ModelID:          "model-1",
						Namespace:        "default",
						RequiredCapacity: 5000,
						VariantCapacities: []interfaces.VariantCapacity{
							{VariantName: "variant-cheap", Cost: 5.0, ReplicaCount: 2, PerReplicaCapacity: 10000},
							{VariantName: "variant-mid", Cost: 10.0, ReplicaCount: 1, PerReplicaCapacity: 15000},
						},
					},
					VariantStates: []interfaces.VariantReplicaState{
						{VariantName: "variant-cheap", CurrentReplicas: 2, PendingReplicas: 1},
						{VariantName: "variant-mid", CurrentReplicas: 1},
					},
				},
			}

			decisions := optimizer.Optimize(context.Background(), requests, nil)

			dm := decisionsByVariant(decisions)
			// cheap has pending but is more cost-efficient → still gets allocation
			Expect(dm["variant-cheap"].TargetReplicas).To(Equal(3))
			Expect(dm["variant-mid"].TargetReplicas).To(Equal(1))
		})
	})
})

func decisionsByVariant(decisions []interfaces.VariantDecision) map[string]interfaces.VariantDecision {
	m := make(map[string]interfaces.VariantDecision, len(decisions))
	for _, d := range decisions {
		m[d.VariantName] = d
	}
	return m
}
