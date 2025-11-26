package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VariantAutoscalingSpec defines the desired state for autoscaling a model variant.
// Note: variantID should follow the format {modelID}-{accelerator}-{acceleratorCount}
// to maintain consistency with spec fields. This is enforced by pattern validation.
type VariantAutoscalingSpec struct {
	// ScaleTargetRef references the target resource (Deployment) to scale.
	// +kubebuilder:validation:Required
	ScaleTargetRef CrossVersionObjectReference `json:"scaleTargetRef"`

	// ModelID specifies the unique identifier of the model to be autoscaled.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Required
	ModelID string `json:"modelID"`

	// VariantID uniquely identifies this variant (model + accelerator + acceleratorCount combination).
	// This is a business identifier that may contain slashes, dots, and mixed case.
	// Format: {modelID}-{accelerator}-{acceleratorCount}
	// Example: "meta/llama-3.1-8b-A100-4" or "model-H100-SXM4-80GB-2"
	//
	// The accelerator portion supports alphanumeric characters, hyphens, and underscores
	// to accommodate complex GPU names like "H100-SXM", "A100_80GB", etc.
	//
	// Note: VariantID (variant_id) is distinct from the VariantAutoscaling resource name (variant_name):
	//   - variant_id (this field): Business identifier, may contain non-K8s-compliant characters
	//   - variant_name (resource.Name): Kubernetes resource name (DNS-1123 compliant)
	//
	// Both identifiers are exposed as Prometheus labels for flexible querying:
	//   - Use variant_name to query by Kubernetes resource (typically matches Deployment name)
	//   - Use variant_id to query by business identifier (model/variant naming)
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^.+-[A-Za-z0-9_-]+-[1-9][0-9]*$`
	VariantID string `json:"variantID"`

	// Accelerator specifies the accelerator type for this variant (e.g., "A100", "L40S").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Required
	Accelerator string `json:"accelerator"`

	// AcceleratorCount specifies the number of accelerator units per replica.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Required
	AcceleratorCount int `json:"acceleratorCount"`

	//TODO: remove this
	// SLOClassRef references the ConfigMap key containing Service Level Objective (SLO) configuration.
	// +kubebuilder:validation:Optional
	SLOClassRef ConfigMapKeyRef `json:"sloClassRef"`

	// VariantProfile provides performance characteristics for this variant.
	// +kubebuilder:validation:Optional
	VariantProfile VariantProfile `json:"variantProfile"`

	// VariantCost specifies the cost per replica for this variant configuration.
	// This is a static characteristic of the variant (cost rate), not runtime cost.
	// Total cost can be calculated as: VariantCost * NumReplicas
	// If not specified, defaults to "10".
	// Note: When running multiple variants with different costs, it is recommended to explicitly
	// set this field for accurate cost comparisons. A warning will be logged if the default is used.
	// +kubebuilder:validation:Pattern=`^\d+(\.\d+)?$`
	// +kubebuilder:default="10.0"
	// +optional
	VariantCost string `json:"variantCost,omitempty"`
}

// CrossVersionObjectReference contains enough information to let you identify the target resource.
// This is the same structure as used in HorizontalPodAutoscaler.
type CrossVersionObjectReference struct {
	// APIVersion is the API version of the target resource.
	// +kubebuilder:validation:MinLength=1
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind is the kind of the target resource. Currently only "Deployment" is supported.
	// +kubebuilder:validation:Enum=Deployment
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// Name is the name of the target resource.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// ConfigMapKeyRef references a specific key within a ConfigMap.
type ConfigMapKeyRef struct {
	// Name is the name of the ConfigMap.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the key within the ConfigMap.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// VariantProfile provides performance characteristics for a specific variant.
type VariantProfile struct {
	// PerfParms specifies the prefill and decode parameters for TTFT and ITL models.
	// +kubebuilder:validation:Optional
	PerfParms PerfParms `json:"perfParms"`

	// MaxBatchSize is the maximum batch size supported by this variant.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Optional
	MaxBatchSize int `json:"maxBatchSize"`
}

// PerfParms contains performance parameters for the variant.
type PerfParms struct {
	// DecodeParms contains parameters for the decode phase (ITL calculation).
	// Expected keys: "alpha", "beta" for equation: itl = alpha + beta * maxBatchSize
	// +kubebuilder:validation:MinProperties=1
	DecodeParms map[string]string `json:"decodeParms"`

	// PrefillParms contains parameters for the prefill phase (TTFT calculation).
	// Expected keys: "gamma", "delta" for equation: ttft = gamma + delta * tokens * maxBatchSize
	// +kubebuilder:validation:MinProperties=1
	PrefillParms map[string]string `json:"prefillParms"`
}

// VariantAutoscalingStatus represents the current status of autoscaling for this specific variant.
// Since each VariantAutoscaling CR represents a single variant, status contains singular allocation
// fields rather than arrays.
type VariantAutoscalingStatus struct {
	// CurrentAlloc specifies the current resource allocation for this variant.
	CurrentAlloc Allocation `json:"currentAlloc,omitempty"`

	// DesiredOptimizedAlloc indicates the target optimized allocation based on autoscaling logic.
	DesiredOptimizedAlloc OptimizedAlloc `json:"desiredOptimizedAlloc,omitempty"`

	// Actuation provides details about the actuation process and its current status.
	Actuation ActuationStatus `json:"actuation,omitempty"`

	// Conditions represent the latest available observations of the VariantAutoscaling's state
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Allocation describes the current resource allocation for this variant.
// Note: In single-variant architecture, variantID, accelerator, maxBatch, and variantCost
// are not needed here as they are already defined in the parent VariantAutoscaling spec.
type Allocation struct {
	// NumReplicas is the number of replicas currently allocated.
	// +kubebuilder:validation:Minimum=0
	NumReplicas int32 `json:"numReplicas"`
}

// LastUpdateInfo tracks the last change to the allocation decision.
// This struct captures when the allocation decision changed, by how much, and why.
type LastUpdateInfo struct {
	// UpdateTime is the timestamp when NumReplicas or Reason changed from the previous state.
	// This field tracks when the allocation decision actually changed, which may be
	// different from LastRunTime (which is updated on every reconciliation).
	// +optional
	UpdateTime metav1.Time `json:"updateTime,omitempty"`

	// NumReplicasChanged is the delta (change) in replicas at the time of this update.
	// This is calculated as: (new desiredOptimized.NumReplicas) - (previous desiredOptimized.NumReplicas)
	// Positive values indicate scale-up, negative values indicate scale-down, zero means no change in replicas.
	// The field is always present (even when zero) to distinguish "no change" from "unset".
	// +kubebuilder:validation:Minimum=-10000
	// +kubebuilder:validation:Maximum=10000
	// +optional
	NumReplicasChanged int32 `json:"numReplicasChanged"`

	// Reason provides a human-readable explanation for the allocation decision.
	// This field indicates whether the allocation came from the optimizer,
	// fallback logic, or scale-to-zero enforcement.
	// Examples: "Optimizer solution: cost-optimal allocation",
	// "Fallback: metrics unavailable",
	// "Scale-to-zero: no load detected"
	// +optional
	Reason string `json:"reason,omitempty"`
}

// OptimizedAlloc describes the target optimized allocation for this variant.
type OptimizedAlloc struct {
	// LastRunTime is the timestamp of the last optimization run.
	LastRunTime metav1.Time `json:"lastRunTime,omitempty"`

	// NumReplicas is the number of replicas for the optimized allocation.
	// +kubebuilder:validation:Minimum=0
	NumReplicas int32 `json:"numReplicas"`

	// LastUpdate tracks when this allocation decision last changed and why.
	// This provides observability into when the controller made a scaling decision.
	// +optional
	LastUpdate LastUpdateInfo `json:"lastUpdate,omitempty"`
}

// ActuationStatus provides details about the actuation process and its current status.
type ActuationStatus struct {
	// Applied indicates whether the actuation was successfully applied.
	Applied bool `json:"applied"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=va
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=".spec.modelID"
// +kubebuilder:printcolumn:name="VariantID",type=string,JSONPath=".spec.variantID"
// +kubebuilder:printcolumn:name="Accelerator",type=string,JSONPath=".spec.accelerator"
// +kubebuilder:printcolumn:name="CurrentReplicas",type=integer,JSONPath=".status.currentAlloc.numReplicas"
// +kubebuilder:printcolumn:name="Optimized",type=integer,JSONPath=".status.desiredOptimizedAlloc.numReplicas"
// +kubebuilder:printcolumn:name="MetricsReady",type=string,JSONPath=".status.conditions[?(@.type=='MetricsAvailable')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// VariantAutoscaling is the Schema for the variantautoscalings API.
// It represents the autoscaling configuration and status for a model variant.
type VariantAutoscaling struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state for autoscaling the model variant.
	Spec VariantAutoscalingSpec `json:"spec,omitempty"`

	// Status represents the current status of autoscaling for the model variant.
	Status VariantAutoscalingStatus `json:"status,omitempty"`
}

// VariantAutoscalingList contains a list of VariantAutoscaling resources.
// +kubebuilder:object:root=true
type VariantAutoscalingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of VariantAutoscaling resources.
	Items []VariantAutoscaling `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VariantAutoscaling{}, &VariantAutoscalingList{})
}

// Condition Types for VariantAutoscaling
const (
	// TypeMetricsAvailable indicates whether vLLM metrics are available from Prometheus
	TypeMetricsAvailable = "MetricsAvailable"
	// TypeOptimizationReady indicates whether the optimization engine can run successfully
	TypeOptimizationReady = "OptimizationReady"
)

// Condition Reasons for MetricsAvailable
const (
	// ReasonMetricsFound indicates vLLM metrics were successfully retrieved
	ReasonMetricsFound = "MetricsFound"
	// ReasonMetricsMissing indicates vLLM metrics are not available (likely ServiceMonitor issue)
	ReasonMetricsMissing = "MetricsMissing"
	// ReasonMetricsStale indicates metrics exist but are outdated
	ReasonMetricsStale = "MetricsStale"
	// ReasonPrometheusError indicates error querying Prometheus
	ReasonPrometheusError = "PrometheusError"
)

// Condition Reasons for OptimizationReady
const (
	// ReasonOptimizationSucceeded indicates optimization completed successfully
	ReasonOptimizationSucceeded = "OptimizationSucceeded"
	// ReasonOptimizationFailed indicates optimization failed
	ReasonOptimizationFailed = "OptimizationFailed"
	// ReasonMetricsUnavailable indicates optimization cannot run due to missing metrics
	ReasonMetricsUnavailable = "MetricsUnavailable"
	// ReasonInvalidConfiguration indicates VA has invalid configuration (e.g., missing ModelID)
	ReasonInvalidConfiguration = "InvalidConfiguration"
	// ReasonSkippedProcessing indicates VA was skipped during processing
	ReasonSkippedProcessing = "SkippedProcessing"
)
