package v1alpha1

import (
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VariantAutoscalingSpec defines the desired state for autoscaling a model variant.
type VariantAutoscalingSpec struct {
	// ScaleTargetRef references the scalable resource to manage.
	// This follows the same pattern as HorizontalPodAutoscaler.
	// +kubebuilder:validation:Required
	ScaleTargetRef autoscalingv1.CrossVersionObjectReference `json:"scaleTargetRef"`

	// ModelID specifies the unique identifier of the model to be autoscaled.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ModelID string `json:"modelID"`

	// AcceleratorType specifies the type of accelerator used by this variant (e.g., "nvidia-a100-80gb").
	// This replaces the nested accelerator definition in ModelProfile.
	// +kubebuilder:validation:Optional
	AcceleratorType string `json:"acceleratorType,omitempty"`

	// AcceleratorCount specifies the number of accelerators per replica.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	AcceleratorCount int `json:"acceleratorCount,omitempty"`

	// VariantProfile provides performance characteristics for this specific variant.
	// This is a simplified alternative to ModelProfile for single-accelerator deployments.
	// +kubebuilder:validation:Optional
	VariantProfile *VariantProfile `json:"variantProfile,omitempty"`

	// MinReplicas is the minimum number of replicas for this variant.
	// Defaults to 0 if not specified (allows scale to zero).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas for this variant.
	// If not specified, there is no upper limit.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// ModelProfile provides resource and performance characteristics for the model variant.
	// Deprecated: Use AcceleratorType, AcceleratorCount, and VariantProfile instead.
	// Maintained for backward compatibility.
	// +kubebuilder:validation:Optional
	ModelProfile ModelProfile `json:"modelProfile,omitempty"`

	// VariantCost specifies the cost per replica for this variant (used in saturation analysis).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^\d+(\.\d+)?$`
	// +kubebuilder:default="10.0"
	VariantCost string `json:"variantCost,omitempty"`
}

// ModelProfile provides resource and performance characteristics for the model variant.
// Deprecated: Use AcceleratorType, AcceleratorCount, and VariantProfile instead.
type ModelProfile struct {
	// Accelerators is a list of accelerator profiles for the model variant.
	// +kubebuilder:validation:Optional
	Accelerators []AcceleratorProfile `json:"accelerators,omitempty"`
}

type PerfParms struct {
	// DecodeParms contains parameters for the decode phase (ITL calculation)
	// Expected keys: "alpha", "beta" for equation: itl = alpha + beta * maxBatchSize
	// +kubebuilder:validation:MinProperties=1
	DecodeParms map[string]string `json:"decodeParms"`
	// PrefillParms contains parameters for the prefill phase (TTFT calculation)
	// Expected keys: "gamma", "delta" for equation: ttft = gamma + delta * tokens * maxBatchSize
	// +kubebuilder:validation:MinProperties=1
	PrefillParms map[string]string `json:"prefillParms"`
}

// AcceleratorProfile defines the configuration for an accelerator used in autoscaling.
// It specifies the type and count of accelerator, as well as parameters for scaling behavior.
type AcceleratorProfile struct {
	// Acc specifies the type or name of the accelerator (e.g., GPU type).
	// +kubebuilder:validation:MinLength=1
	Acc string `json:"acc"`

	// AccCount specifies the number of accelerator units to be used.
	// +kubebuilder:validation:Minimum=1
	AccCount int `json:"accCount"`

	// PerParms specifies the prefill and decode parameters for ttft and itl models
	// +kubebuilder:validation:Optional
	PerfParms PerfParms `json:"perfParms,omitempty"`

	// MaxBatchSize is the maximum batch size supported by the accelerator.
	// +kubebuilder:validation:Minimum=1
	MaxBatchSize int `json:"maxBatchSize"`
}

// VariantProfile provides performance characteristics for a single-accelerator variant.
// This is a simplified alternative to ModelProfile for common use cases.
type VariantProfile struct {
	// MaxBatchSize is the maximum batch size supported by the variant.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Required
	MaxBatchSize int `json:"maxBatchSize"`

	// PerfParms specifies the prefill and decode parameters for TTFT and ITL models.
	// +kubebuilder:validation:Optional
	PerfParms *PerfParms `json:"perfParms,omitempty"`
}

// VariantAutoscalingStatus represents the current status of autoscaling for a variant,
// including the current allocation, desired optimized allocation, and actuation status.
type VariantAutoscalingStatus struct {
	// CurrentAlloc specifies the current resource allocation for the variant.
	// +kubebuilder:validation:Optional
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

// Allocation describes the current resource allocation for a model variant.
type Allocation struct {
	// NumReplicas is the number of replicas currently allocated.
	// This is the primary field; other fields are deprecated.
	// +kubebuilder:validation:Minimum=0
	NumReplicas int `json:"numReplicas"`

	// Accelerator is the type of accelerator currently allocated.
	// Deprecated: Use spec.acceleratorType instead. Maintained for backward compatibility.
	// +kubebuilder:validation:Optional
	Accelerator string `json:"accelerator,omitempty"`

	// MaxBatch is the maximum batch size currently allocated.
	// Deprecated: Available via metrics from Prometheus.
	// +kubebuilder:validation:Optional
	MaxBatch int `json:"maxBatch,omitempty"`

	// VariantCost is the cost associated with the current variant allocation.
	// Deprecated: Use spec.variantCost instead.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^\d+(\.\d+)?$`
	VariantCost string `json:"variantCost,omitempty"`

	// ITLAverage is the average inter token latency for the current allocation.
	// Deprecated: Available via metrics from Prometheus.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^\d+(\.\d+)?$`
	ITLAverage string `json:"itlAverage,omitempty"`

	// TTFTAverage is the average time to first token for the current allocation
	// Deprecated: Available via metrics from Prometheus.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^\d+(\.\d+)?$`
	TTFTAverage string `json:"ttftAverage,omitempty"`

	// Load describes the workload characteristics for the current allocation.
	// Deprecated: Available via metrics from Prometheus.
	// +kubebuilder:validation:Optional
	Load *LoadProfile `json:"load,omitempty"`
}

// LoadProfile represents the configuration for workload characteristics,
// including the rate of incoming requests (ArrivalRate) and the average
// length of each request (AvgLength). Both fields are specified as strings
// to allow flexible input formats.
type LoadProfile struct {
	// ArrivalRate is the rate of incoming requests in inference server.
	ArrivalRate string `json:"arrivalRate"`

	// AvgInputTokens is the average number of input(prefill) tokens per request in inference server.
	AvgInputTokens string `json:"avgInputTokens"`

	// AvgOutputTokens is the average number of output(decode) tokens per request in inference server.
	AvgOutputTokens string `json:"avgOutputTokens"`
}

// OptimizedAlloc describes the target optimized allocation for a model variant.
type OptimizedAlloc struct {
	// NumReplicas is the number of replicas for the optimized allocation.
	// +kubebuilder:validation:Minimum=0
	NumReplicas int `json:"numReplicas"`

	// LastRunTime is the timestamp of the last optimization run.
	// +kubebuilder:validation:Optional
	LastRunTime metav1.Time `json:"lastRunTime,omitempty"`

	// Accelerator is the type of accelerator for the optimized allocation.
	// Deprecated: Use spec.acceleratorType instead. Maintained for backward compatibility.
	// +kubebuilder:validation:Optional
	Accelerator string `json:"accelerator,omitempty"`
}

// ActuationStatus provides details about the actuation process and its current status.
type ActuationStatus struct {
	// Applied indicates whether the actuation was successfully applied.
	Applied bool `json:"applied"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=va
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=".spec.scaleTargetRef.name"
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=".spec.modelID"
// +kubebuilder:printcolumn:name="Accelerator",type=string,JSONPath=".status.currentAlloc.accelerator"
// +kubebuilder:printcolumn:name="CurrentReplicas",type=integer,JSONPath=".status.currentAlloc.numReplicas"
// +kubebuilder:printcolumn:name="Optimized",type=string,JSONPath=".status.desiredOptimizedAlloc.numReplicas"
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
	// TypeTargetResolved indicates whether the target model variant has been resolved successfully
	TypeTargetResolved = "TargetResolved"
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

	// ReasonTargetFound indicates the scale target was successfully resolved
	ReasonTargetFound = "TargetFound"
	// ReasonTargetNotFound indicates the scale target could not be found
	ReasonTargetNotFound = "TargetNotFound"
)

// GetScaleTargetAPI returns the API of the scale target resource.
func (va *VariantAutoscaling) GetScaleTargetAPI() string {
	return va.Spec.ScaleTargetRef.APIVersion
}

// GetScaleTargetName returns the name of the scale target resource.
func (va *VariantAutoscaling) GetScaleTargetName() string {
	return va.Spec.ScaleTargetRef.Name
}

// GetScaleTargetKind returns the kind of the scale target resource.
func (va *VariantAutoscaling) GetScaleTargetKind() string {
	return va.Spec.ScaleTargetRef.Kind
}

// GetAcceleratorType returns the accelerator type for this variant.
// It first checks the new AcceleratorType field, then falls back to ModelProfile for backward compatibility.
func (va *VariantAutoscaling) GetAcceleratorType() string {
	if va.Spec.AcceleratorType != "" {
		return va.Spec.AcceleratorType
	}
	// Fallback to ModelProfile for backward compatibility
	if len(va.Spec.ModelProfile.Accelerators) > 0 {
		return va.Spec.ModelProfile.Accelerators[0].Acc
	}
	return ""
}

// GetAcceleratorCount returns the number of accelerators per replica.
// It first checks the new AcceleratorCount field, then falls back to ModelProfile for backward compatibility.
func (va *VariantAutoscaling) GetAcceleratorCount() int {
	if va.Spec.AcceleratorCount > 0 {
		return va.Spec.AcceleratorCount
	}
	// Fallback to ModelProfile for backward compatibility
	if len(va.Spec.ModelProfile.Accelerators) > 0 {
		return va.Spec.ModelProfile.Accelerators[0].AccCount
	}
	return 1 // Default to 1 accelerator
}

// GetMaxBatchSize returns the maximum batch size for this variant.
// It first checks VariantProfile, then falls back to ModelProfile for backward compatibility.
func (va *VariantAutoscaling) GetMaxBatchSize() int {
	if va.Spec.VariantProfile != nil && va.Spec.VariantProfile.MaxBatchSize > 0 {
		return va.Spec.VariantProfile.MaxBatchSize
	}
	// Fallback to ModelProfile for backward compatibility
	if len(va.Spec.ModelProfile.Accelerators) > 0 {
		return va.Spec.ModelProfile.Accelerators[0].MaxBatchSize
	}
	return 0
}

// GetMinReplicas returns the minimum number of replicas, defaulting to 0 if not set.
func (va *VariantAutoscaling) GetMinReplicas() int32 {
	if va.Spec.MinReplicas != nil {
		return *va.Spec.MinReplicas
	}
	return 0 // Default: allow scale to zero
}

// GetMaxReplicas returns the maximum number of replicas, or -1 if unlimited.
func (va *VariantAutoscaling) GetMaxReplicas() int32 {
	if va.Spec.MaxReplicas != nil {
		return *va.Spec.MaxReplicas
	}
	return -1 // -1 indicates no upper limit
}
