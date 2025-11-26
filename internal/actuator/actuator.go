package actuator

import (
	"context"
	"fmt"

	llmdOptv1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/metrics"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Actuator struct {
	Client         client.Client
	MetricsEmitter *metrics.MetricsEmitter
}

func NewActuator(k8sClient client.Client) *Actuator {
	return &Actuator{
		Client:         k8sClient,
		MetricsEmitter: metrics.NewMetricsEmitter(),
	}
}

// GetCurrentDeploymentReplicas gets the real current replica count from the actual Deployment
func (a *Actuator) GetCurrentDeploymentReplicas(ctx context.Context, va *llmdOptv1alpha1.VariantAutoscaling) (int32, error) {
	var deploy appsv1.Deployment
	deployName := va.Spec.ScaleTargetRef.Name
	err := utils.GetDeploymentWithBackoff(ctx, a.Client, deployName, va.Namespace, &deploy)
	if err != nil {
		return 0, fmt.Errorf("failed to get Deployment %s/%s: %w", va.Namespace, deployName, err)
	}

	// Prefer status replicas (actual current state)
	if deploy.Status.Replicas >= 0 {
		return deploy.Status.Replicas, nil
	}

	// Fallback to spec if status not ready
	if deploy.Spec.Replicas != nil {
		return *deploy.Spec.Replicas, nil
	}

	// Final fallback
	return 1, nil
}

func (a *Actuator) EmitMetrics(ctx context.Context, VariantAutoscaling *llmdOptv1alpha1.VariantAutoscaling) error {
	// Emit replica metrics with real-time data for external autoscalers
	if VariantAutoscaling.Status.DesiredOptimizedAlloc.NumReplicas >= 0 {

		// Get real current replicas from Deployment (not stale VariantAutoscaling status)
		currentReplicas, err := a.GetCurrentDeploymentReplicas(ctx, VariantAutoscaling)
		if err != nil {
			logger.Log.Warn("Could not get current deployment replicas, using VariantAutoscaling status",
				"error", err, "variant", VariantAutoscaling.Name)
			currentReplicas = VariantAutoscaling.Status.CurrentAlloc.NumReplicas // fallback
		}

		// In single-variant architecture, accelerator is in spec
		if err := a.MetricsEmitter.EmitReplicaMetrics(
			ctx,
			VariantAutoscaling,
			currentReplicas, // Real current from Deployment
			VariantAutoscaling.Status.DesiredOptimizedAlloc.NumReplicas, // Inferno's optimization target
			VariantAutoscaling.Spec.Accelerator,                         // In single-variant architecture, accelerator is in spec
		); err != nil {
			logger.Log.Error(err, "Failed to emit optimization signals for variantAutoscaling",
				"variantAutoscaling-name", VariantAutoscaling.Name)
			// Don't fail the reconciliation for metric emission errors
			// Metrics are critical for HPA, but emission failures shouldn't break core functionality
			return nil
		}
		logger.Log.Info(fmt.Sprintf("EmitReplicaMetrics completed - variant: %s, current-replicas: %d, desired-replicas: %d, accelerator: %s",
			VariantAutoscaling.Name,
			VariantAutoscaling.Status.CurrentAlloc.NumReplicas,
			VariantAutoscaling.Status.DesiredOptimizedAlloc.NumReplicas,
			VariantAutoscaling.Spec.Accelerator))
		return nil
	}
	logger.Log.Info(fmt.Sprintf("Skipping EmitReplicaMetrics for variantAutoscaling: %s - NumReplicas is 0", VariantAutoscaling.Name))
	return nil
}
