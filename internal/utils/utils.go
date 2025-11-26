package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	interfaces "github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	infernoConfig "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

// Global backoff configurations
var (
	// Standard backoff for most operations
	StandardBackoff = wait.Backoff{
		Duration: 100 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5,
	}

	// Slow backoff for operations that need more time
	ReconcileBackoff = wait.Backoff{
		Duration: 500 * time.Millisecond,
		Factor:   2.0,
		Steps:    5,
	}

	// Prometheus validation backoff with longer intervals
	// TODO: investigate why Prometheus needs longer backoff durations
	PrometheusBackoff = wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    6, // 5s, 10s, 20s, 40s, 80s, 160s = ~5 minutes total
	}
)

// GetResourceWithBackoff performs a Get operation with exponential backoff retry logic
func GetResourceWithBackoff[T client.Object](ctx context.Context, c client.Client, objKey client.ObjectKey, obj T, backoff wait.Backoff, resourceType string) error {
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, objKey, obj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, err // Don't retry on notFound errors
			}

			logger.Log.Error(err, "transient error getting resource, retrying - ",
				"resourceType: ", resourceType,
				" name: ", objKey.Name,
				" namespace: ", objKey.Namespace)
			return false, nil // Retry on transient errors
		}

		return true, nil
	})
}

// Helper functions for common resource types with standard backoff
func GetDeploymentWithBackoff(ctx context.Context, c client.Client, name, namespace string, deploy *appsv1.Deployment) error {
	return GetResourceWithBackoff(ctx, c, client.ObjectKey{Name: name, Namespace: namespace}, deploy, StandardBackoff, "Deployment")
}

func GetConfigMapWithBackoff(ctx context.Context, c client.Client, name, namespace string, cm *corev1.ConfigMap) error {
	return GetResourceWithBackoff(ctx, c, client.ObjectKey{Name: name, Namespace: namespace}, cm, StandardBackoff, "ConfigMap")
}

func GetVariantAutoscalingWithBackoff(ctx context.Context, c client.Client, name, namespace string, va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) error {
	return GetResourceWithBackoff(ctx, c, client.ObjectKey{Name: name, Namespace: namespace}, va, StandardBackoff, "VariantAutoscaling")
}

// UpdateStatusWithBackoff performs a Status Update operation with exponential backoff retry logic.
// DEPRECATED: Use UpdateStatusWithOptimisticLocking for proper conflict handling.
// This function is kept for backward compatibility but doesn't handle resource version conflicts properly.
func UpdateStatusWithBackoff[T client.Object](ctx context.Context, c client.Client, obj T, backoff wait.Backoff, resourceType string) error {
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		err := c.Status().Update(ctx, obj)
		if err != nil {
			if apierrors.IsInvalid(err) || apierrors.IsForbidden(err) {
				logger.Log.Error(err, "permanent error updating status for resource ", resourceType, ", name: ", obj.GetName())
				return false, err // Don't retry on permanent errors
			}
			if apierrors.IsConflict(err) {
				// Resource version conflict - object was modified since we read it
				logger.Log.Warn("conflict updating status (resource version mismatch), retrying", "resource", resourceType, "name", obj.GetName())
				return false, nil // Retry on conflict
			}
			logger.Log.Error(err, "transient error updating status, retrying for resource ", resourceType, ", name: ", obj.GetName())
			return false, nil // Retry on transient errors
		}
		return true, nil
	})
}

// UpdateStatusWithOptimisticLocking performs a status update with proper optimistic locking.
// On conflict errors (resource version mismatch), it re-fetches the resource, applies the mutation,
// and retries the update. This ensures status updates don't fail due to concurrent modifications.
//
// Parameters:
//   - ctx: context for the operation
//   - c: Kubernetes client
//   - objKey: object key (name/namespace) to identify the resource
//   - obj: a zero-value instance of the resource type (used for fetching)
//   - mutate: function that modifies the status of the fetched resource
//   - backoff: retry backoff configuration
//   - resourceType: human-readable resource type name for logging
//
// Example:
//
//	err := UpdateStatusWithOptimisticLocking(ctx, r.Client,
//	    client.ObjectKey{Name: va.Name, Namespace: va.Namespace},
//	    &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{},
//	    func(obj *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) {
//	        obj.Status.DesiredAlloc = newAlloc
//	    },
//	    StandardBackoff,
//	    "VariantAutoscaling")
func UpdateStatusWithOptimisticLocking[T client.Object](
	ctx context.Context,
	c client.Client,
	objKey client.ObjectKey,
	obj T,
	mutate func(T),
	backoff wait.Backoff,
	resourceType string,
) error {
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		// Fetch the latest version of the resource
		err := c.Get(ctx, objKey, obj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				logger.Log.Error(err, "resource not found during status update", "resource", resourceType, "name", objKey.Name)
				return false, err // Don't retry on not found
			}
			logger.Log.Error(err, "error fetching resource for status update, retrying", "resource", resourceType, "name", objKey.Name)
			return false, nil // Retry on transient errors
		}

		// Apply the mutation to the fresh copy
		mutate(obj)

		// Try to update the status with the current resource version
		err = c.Status().Update(ctx, obj)
		if err != nil {
			if apierrors.IsInvalid(err) || apierrors.IsForbidden(err) {
				logger.Log.Error(err, "permanent error updating status", "resource", resourceType, "name", obj.GetName())
				return false, err // Don't retry on permanent errors
			}
			if apierrors.IsConflict(err) {
				// Resource was modified between fetch and update - retry with fresh fetch
				logger.Log.Debug("conflict updating status (concurrent modification), will retry with fresh fetch",
					"resource", resourceType, "name", obj.GetName())
				return false, nil // Retry - will fetch fresh copy
			}
			logger.Log.Error(err, "transient error updating status, retrying", "resource", resourceType, "name", obj.GetName())
			return false, nil // Retry on transient errors
		}

		logger.Log.Debug("status updated successfully", "resource", resourceType, "name", obj.GetName())
		return true, nil
	})
}

// Adapter to create wva system data types from config maps.
// Note: WVA operates in unlimited mode, so capacity data is not used.
func CreateSystemData(
	acceleratorCm map[string]map[string]string,
	serviceClassCm map[string]string) *infernoConfig.SystemData {

	systemData := &infernoConfig.SystemData{
		Spec: infernoConfig.SystemSpec{
			Accelerators:   infernoConfig.AcceleratorData{},
			Models:         infernoConfig.ModelData{},
			ServiceClasses: infernoConfig.ServiceClassData{},
			Servers:        infernoConfig.ServerData{},
			Optimizer:      infernoConfig.OptimizerData{},
			Capacity:       infernoConfig.CapacityData{},
		},
	}

	// get accelerator data
	acceleratorData := []infernoConfig.AcceleratorSpec{}
	for key, val := range acceleratorCm {
		cost, err := strconv.ParseFloat(val["cost"], 32)
		if err != nil {
			logger.Log.Warn("failed to parse accelerator cost in configmap, skipping accelerator", "name", key)
			continue
		}
		acceleratorData = append(acceleratorData, infernoConfig.AcceleratorSpec{
			Name:         key,
			Type:         val["device"],
			Multiplicity: 1,                         // TODO: multiplicity should be in the configured accelerator spec
			Power:        infernoConfig.PowerSpec{}, // Not currently used
			Cost:         float32(cost),
		})
	}
	systemData.Spec.Accelerators.Spec = acceleratorData

	// Capacity data is not used in unlimited mode - initialize empty for future limited mode work
	systemData.Spec.Capacity.Count = []infernoConfig.AcceleratorCount{}

	// get service class data
	serviceClassData := []infernoConfig.ServiceClassSpec{}
	for key, val := range serviceClassCm {
		var sc interfaces.ServiceClass
		if err := yaml.Unmarshal([]byte(val), &sc); err != nil {
			logger.Log.Warn("failed to parse service class data, skipping service class", "key", key, "err", err)
			continue
		}
		serviceClassSpec := infernoConfig.ServiceClassSpec{
			Name:         sc.Name,
			Priority:     sc.Priority,
			ModelTargets: make([]infernoConfig.ModelTarget, len(sc.Data)),
		}
		for i, entry := range sc.Data {
			serviceClassSpec.ModelTargets[i] = infernoConfig.ModelTarget{
				Model:    entry.Model,
				SLO_ITL:  float32(entry.SLOTPOT),
				SLO_TTFT: float32(entry.SLOTTFT),
			}
		}
		serviceClassData = append(serviceClassData, serviceClassSpec)
	}
	systemData.Spec.ServiceClasses.Spec = serviceClassData

	// set optimizer configuration
	// TODO: make it configurable
	systemData.Spec.Optimizer.Spec = infernoConfig.OptimizerSpec{
		Unlimited: true,
		// SaturationPolicy omitted - defaults to "None" (not relevant in unlimited mode)
	}

	// initialize model data
	systemData.Spec.Models.PerfData = []infernoConfig.ModelAcceleratorPerfData{}

	// initialize dynamic server data
	systemData.Spec.Servers.Spec = []infernoConfig.ServerSpec{}

	return systemData
}

// add model accelerator pair profile data to inferno system data
// In single-variant architecture, accelerator and acceleratorCount are passed separately
func AddModelAcceleratorProfileToSystemData(
	sd *infernoConfig.SystemData,
	modelName string,
	accelerator string,
	acceleratorCount int,
	variantProfile *llmdVariantAutoscalingV1alpha1.VariantProfile) (err error) {

	// extract decode model (itl) parameters
	decodeParms := variantProfile.PerfParms.DecodeParms
	if len(decodeParms) < 2 {
		return fmt.Errorf("length of decodeParms should be 2")
	}

	var alpha, beta float64
	if alpha, err = strconv.ParseFloat(decodeParms["alpha"], 32); err != nil {
		return err
	}
	if beta, err = strconv.ParseFloat(decodeParms["beta"], 32); err != nil {
		return err
	}

	// extract prefill model (ttft) parameters
	prefillParms := variantProfile.PerfParms.PrefillParms
	if len(prefillParms) < 2 {
		return fmt.Errorf("length of prefillParms should be 2")
	}

	var gamma, delta float64
	if gamma, err = strconv.ParseFloat(prefillParms["gamma"], 32); err != nil {
		return err
	}
	if delta, err = strconv.ParseFloat(prefillParms["delta"], 32); err != nil {
		return err
	}

	sd.Spec.Models.PerfData = append(sd.Spec.Models.PerfData,
		infernoConfig.ModelAcceleratorPerfData{
			Name:         modelName,
			Acc:          accelerator,
			AccCount:     acceleratorCount,
			MaxBatchSize: variantProfile.MaxBatchSize,
			DecodeParms: infernoConfig.DecodeParms{
				Alpha: float32(alpha),
				Beta:  float32(beta),
			},
			PrefillParms: infernoConfig.PrefillParms{
				Gamma: float32(gamma),
				Delta: float32(delta),
			},
		})
	return nil
}

// Add server specs to inferno system data
// In single-variant architecture, load and performance metrics are passed as parameters
// instead of being read from status (they will be collected from Prometheus in PR2)
func AddServerInfoToSystemData(
	sd *infernoConfig.SystemData,
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	className string,
	arrivalRate float64,
	avgInputTokens float64,
	avgOutputTokens float64,
	itlAverage float64,
	ttftAverage float64) (err error) {

	serverLoadSpec := &infernoConfig.ServerLoadSpec{
		ArrivalRate:  float32(arrivalRate),
		AvgInTokens:  int(avgInputTokens),
		AvgOutTokens: int(avgOutputTokens),
	}

	// server allocation
	// In single-variant architecture, variant characteristics are in spec, not status
	var cost float64
	if cost, err = strconv.ParseFloat(va.Spec.VariantCost, 32); err != nil || !CheckValue(cost) {
		cost = 10.0 // default cost
	}

	AllocationData := &infernoConfig.AllocationData{
		Accelerator: va.Spec.Accelerator,
		NumReplicas: int(va.Status.CurrentAlloc.NumReplicas),
		MaxBatch:    va.Spec.VariantProfile.MaxBatchSize,
		Cost:        float32(cost),
		ITLAverage:  float32(itlAverage),
		TTFTAverage: float32(ttftAverage),
		Load:        *serverLoadSpec,
	}

	// all server data
	minNumReplicas := 1 // scale to zero is disabled by default
	if os.Getenv("WVA_SCALE_TO_ZERO") == "true" {
		minNumReplicas = 0
	}
	serverSpec := &infernoConfig.ServerSpec{
		Name:            FullName(va.Name, va.Namespace),
		Class:           className,
		Model:           va.Spec.ModelID,
		KeepAccelerator: true,
		MinNumReplicas:  minNumReplicas,
		CurrentAlloc:    *AllocationData,
		DesiredAlloc:    infernoConfig.AllocationData{},
	}

	// set max batch size from variant profile
	// In single-variant architecture, max batch size is directly in spec
	maxBatchSize := va.Spec.VariantProfile.MaxBatchSize
	if maxBatchSize > 0 {
		serverSpec.MaxBatchSize = maxBatchSize
	}

	sd.Spec.Servers.Spec = append(sd.Spec.Servers.Spec, *serverSpec)
	return nil
}

// Adapter from inferno alloc solution to optimized alloc
func CreateOptimizedAlloc(name string,
	namespace string,
	allocationSolution *infernoConfig.AllocationSolution) (*llmdVariantAutoscalingV1alpha1.OptimizedAlloc, error) {

	serverName := FullName(name, namespace)
	var allocationData infernoConfig.AllocationData
	var exists bool
	if allocationData, exists = allocationSolution.Spec[serverName]; !exists {
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	logger.Log.Debug("Setting allocation ", "accelerator ", allocationData.Accelerator, "replicas ", allocationData.NumReplicas)

	// Get optimizer's suggested replica count
	numReplicas := int32(allocationData.NumReplicas)

	// In single-variant architecture, accelerator is in spec, not in OptimizedAlloc
	optimizedAlloc := &llmdVariantAutoscalingV1alpha1.OptimizedAlloc{
		LastRunTime: metav1.NewTime(time.Now()),
		NumReplicas: numReplicas,
	}
	return optimizedAlloc, nil
}

// Helper to create a (unique) full name from name and namespace
func FullName(name string, namespace string) string {
	return name + ":" + namespace
}

// Helper to check if a value is valid (not NaN or infinite)
func CheckValue(x float64) bool {
	return !(math.IsNaN(x) || math.IsInf(x, 0))
}

func GetZapLevelFromEnv() zapcore.Level {
	levelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch levelStr {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel // fallback
	}
}

func MarshalStructToJsonString(t any) string {
	jsonBytes, err := json.MarshalIndent(t, "", " ")
	if err != nil {
		return fmt.Sprintf("error marshalling: %v", err)
	}
	re := regexp.MustCompile("\"|\n")
	return re.ReplaceAllString(string(jsonBytes), "")
}

// Helper to find SLOs for a model variant
func FindModelSLO(cmData map[string]string, targetModel string) (*interfaces.ServiceClassEntry, string /* class name */, error) {
	for key, val := range cmData {
		var sc interfaces.ServiceClass
		if err := yaml.Unmarshal([]byte(val), &sc); err != nil {
			return nil, "", fmt.Errorf("failed to parse %s: %w", key, err)
		}

		for _, entry := range sc.Data {
			if entry.Model == targetModel {
				return &entry, sc.Name, nil
			}
		}
	}
	return nil, "", fmt.Errorf("model %q not found in any service class", targetModel)
}

func Ptr[T any](v T) *T {
	return &v
}

// ValidatePrometheusAPIWithBackoff validates Prometheus API connectivity with retry logic
func ValidatePrometheusAPIWithBackoff(ctx context.Context, promAPI promv1.API, backoff wait.Backoff) error {
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		// Test with a simple query that should always work
		query := "up"
		_, _, err := promAPI.Query(ctx, query, time.Now())
		if err != nil {
			logger.Log.Error(err, "Prometheus API validation failed, retrying - ",
				"query: ", query,
				"error: ", err.Error())
			return false, nil // Retry on transient errors
		}

		logger.Log.Info("Prometheus API validation successful with query", "query", query)
		return true, nil
	})
}

// ValidatePrometheusAPI validates Prometheus API connectivity using standard Prometheus backoff
func ValidatePrometheusAPI(ctx context.Context, promAPI promv1.API) error {
	return ValidatePrometheusAPIWithBackoff(ctx, promAPI, PrometheusBackoff)
}

// GetConfigValue retrieves a value from a ConfigMap with a default fallback
func GetConfigValue(data map[string]string, key, def string) string {
	if v, ok := data[key]; ok {
		return v
	}
	return def
}
