# API Reference

## Packages
- [llmd.ai/v1alpha1](#llmdaiv1alpha1)


## llmd.ai/v1alpha1

Package v1alpha1 contains API Schema definitions for the llmd v1alpha1 API group.

### Resource Types
- [VariantAutoscaling](#variantautoscaling)
- [VariantAutoscalingList](#variantautoscalinglist)



#### AcceleratorProfile



AcceleratorProfile defines the configuration for an accelerator used in autoscaling.
It specifies the type and count of accelerator, as well as parameters for scaling behavior.



_Appears in:_
- [ModelProfile](#modelprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `acc` _string_ | Acc specifies the type or name of the accelerator (e.g., GPU type). |  | MinLength: 1 <br /> |
| `accCount` _integer_ | AccCount specifies the number of accelerator units to be used. |  | Minimum: 1 <br /> |
| `perfParms` _[PerfParms](#perfparms)_ | PerParms specifies the prefill and decode parameters for ttft and itl models |  | Optional: \{\} <br /> |
| `maxBatchSize` _integer_ | MaxBatchSize is the maximum batch size supported by the accelerator. |  | Minimum: 1 <br /> |


#### ActuationStatus



ActuationStatus provides details about the actuation process and its current status.



_Appears in:_
- [VariantAutoscalingStatus](#variantautoscalingstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `applied` _boolean_ | Applied indicates whether the actuation was successfully applied. |  |  |


#### Allocation



Allocation describes the current resource allocation for a model variant.



_Appears in:_
- [VariantAutoscalingStatus](#variantautoscalingstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `numReplicas` _integer_ | NumReplicas is the number of replicas currently allocated.<br />This is the primary field; other fields are deprecated. |  | Minimum: 0 <br /> |
| `accelerator` _string_ | Accelerator is the type of accelerator currently allocated.<br />Deprecated: Use spec.acceleratorType instead. Maintained for backward compatibility. |  | Optional: \{\} <br /> |
| `maxBatch` _integer_ | MaxBatch is the maximum batch size currently allocated.<br />Deprecated: Available via metrics from Prometheus. |  | Optional: \{\} <br /> |
| `variantCost` _string_ | VariantCost is the cost associated with the current variant allocation.<br />Deprecated: Use spec.variantCost instead. |  | Optional: \{\} <br />Pattern: `^\d+(\.\d+)?$` <br /> |
| `itlAverage` _string_ | ITLAverage is the average inter token latency for the current allocation.<br />Deprecated: Available via metrics from Prometheus. |  | Optional: \{\} <br />Pattern: `^\d+(\.\d+)?$` <br /> |
| `ttftAverage` _string_ | TTFTAverage is the average time to first token for the current allocation<br />Deprecated: Available via metrics from Prometheus. |  | Optional: \{\} <br />Pattern: `^\d+(\.\d+)?$` <br /> |
| `load` _[LoadProfile](#loadprofile)_ | Load describes the workload characteristics for the current allocation.<br />Deprecated: Available via metrics from Prometheus. |  | Optional: \{\} <br /> |


#### LoadProfile



LoadProfile represents the configuration for workload characteristics,
including the rate of incoming requests (ArrivalRate) and the average
length of each request (AvgLength). Both fields are specified as strings
to allow flexible input formats.



_Appears in:_
- [Allocation](#allocation)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `arrivalRate` _string_ | ArrivalRate is the rate of incoming requests in inference server. |  |  |
| `avgInputTokens` _string_ | AvgInputTokens is the average number of input(prefill) tokens per request in inference server. |  |  |
| `avgOutputTokens` _string_ | AvgOutputTokens is the average number of output(decode) tokens per request in inference server. |  |  |


#### ModelProfile



ModelProfile provides resource and performance characteristics for the model variant.
Deprecated: Use AcceleratorType, AcceleratorCount, and VariantProfile instead.



_Appears in:_
- [VariantAutoscalingSpec](#variantautoscalingspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `accelerators` _[AcceleratorProfile](#acceleratorprofile) array_ | Accelerators is a list of accelerator profiles for the model variant. |  | Optional: \{\} <br /> |


#### OptimizedAlloc



OptimizedAlloc describes the target optimized allocation for a model variant.



_Appears in:_
- [VariantAutoscalingStatus](#variantautoscalingstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `numReplicas` _integer_ | NumReplicas is the number of replicas for the optimized allocation. |  | Minimum: 0 <br /> |
| `lastRunTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastRunTime is the timestamp of the last optimization run. |  | Optional: \{\} <br /> |
| `accelerator` _string_ | Accelerator is the type of accelerator for the optimized allocation.<br />Deprecated: Use spec.acceleratorType instead. Maintained for backward compatibility. |  | Optional: \{\} <br /> |


#### PerfParms







_Appears in:_
- [AcceleratorProfile](#acceleratorprofile)
- [VariantProfile](#variantprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `decodeParms` _object (keys:string, values:string)_ | DecodeParms contains parameters for the decode phase (ITL calculation)<br />Expected keys: "alpha", "beta" for equation: itl = alpha + beta * maxBatchSize |  | MinProperties: 1 <br /> |
| `prefillParms` _object (keys:string, values:string)_ | PrefillParms contains parameters for the prefill phase (TTFT calculation)<br />Expected keys: "gamma", "delta" for equation: ttft = gamma + delta * tokens * maxBatchSize |  | MinProperties: 1 <br /> |


#### VariantAutoscaling



VariantAutoscaling is the Schema for the variantautoscalings API.
It represents the autoscaling configuration and status for a model variant.



_Appears in:_
- [VariantAutoscalingList](#variantautoscalinglist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `llmd.ai/v1alpha1` | | |
| `kind` _string_ | `VariantAutoscaling` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[VariantAutoscalingSpec](#variantautoscalingspec)_ | Spec defines the desired state for autoscaling the model variant. |  |  |
| `status` _[VariantAutoscalingStatus](#variantautoscalingstatus)_ | Status represents the current status of autoscaling for the model variant. |  |  |


#### VariantAutoscalingList



VariantAutoscalingList contains a list of VariantAutoscaling resources.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `llmd.ai/v1alpha1` | | |
| `kind` _string_ | `VariantAutoscalingList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[VariantAutoscaling](#variantautoscaling) array_ | Items is the list of VariantAutoscaling resources. |  |  |


#### VariantAutoscalingSpec



VariantAutoscalingSpec defines the desired state for autoscaling a model variant.



_Appears in:_
- [VariantAutoscaling](#variantautoscaling)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `scaleTargetRef` _[CrossVersionObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#crossversionobjectreference-v1-autoscaling)_ | ScaleTargetRef references the scalable resource to manage.<br />This follows the same pattern as HorizontalPodAutoscaler. |  | Required: \{\} <br /> |
| `modelID` _string_ | ModelID specifies the unique identifier of the model to be autoscaled. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `acceleratorType` _string_ | AcceleratorType specifies the type of accelerator used by this variant (e.g., "nvidia-a100-80gb").<br />This replaces the nested accelerator definition in ModelProfile. |  | Optional: \{\} <br /> |
| `acceleratorCount` _integer_ | AcceleratorCount specifies the number of accelerators per replica. | 1 | Minimum: 1 <br />Optional: \{\} <br /> |
| `variantProfile` _[VariantProfile](#variantprofile)_ | VariantProfile provides performance characteristics for this specific variant.<br />This is a simplified alternative to ModelProfile for single-accelerator deployments. |  | Optional: \{\} <br /> |
| `minReplicas` _integer_ | MinReplicas is the minimum number of replicas for this variant.<br />Defaults to 0 if not specified (allows scale to zero). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `maxReplicas` _integer_ | MaxReplicas is the maximum number of replicas for this variant.<br />If not specified, there is no upper limit. |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `modelProfile` _[ModelProfile](#modelprofile)_ | ModelProfile provides resource and performance characteristics for the model variant.<br />Deprecated: Use AcceleratorType, AcceleratorCount, and VariantProfile instead.<br />Maintained for backward compatibility. |  | Optional: \{\} <br /> |
| `variantCost` _string_ | VariantCost specifies the cost per replica for this variant (used in saturation analysis). | 10.0 | Optional: \{\} <br />Pattern: `^\d+(\.\d+)?$` <br /> |


#### VariantAutoscalingStatus



VariantAutoscalingStatus represents the current status of autoscaling for a variant,
including the current allocation, desired optimized allocation, and actuation status.



_Appears in:_
- [VariantAutoscaling](#variantautoscaling)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `currentAlloc` _[Allocation](#allocation)_ | CurrentAlloc specifies the current resource allocation for the variant. |  | Optional: \{\} <br /> |
| `desiredOptimizedAlloc` _[OptimizedAlloc](#optimizedalloc)_ | DesiredOptimizedAlloc indicates the target optimized allocation based on autoscaling logic. |  |  |
| `actuation` _[ActuationStatus](#actuationstatus)_ | Actuation provides details about the actuation process and its current status. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent the latest available observations of the VariantAutoscaling's state |  | Optional: \{\} <br /> |


#### VariantProfile



VariantProfile provides performance characteristics for a single-accelerator variant.
This is a simplified alternative to ModelProfile for common use cases.



_Appears in:_
- [VariantAutoscalingSpec](#variantautoscalingspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `maxBatchSize` _integer_ | MaxBatchSize is the maximum batch size supported by the variant. |  | Minimum: 1 <br />Required: \{\} <br /> |
| `perfParms` _[PerfParms](#perfparms)_ | PerfParms specifies the prefill and decode parameters for TTFT and ITL models. |  | Optional: \{\} <br /> |


