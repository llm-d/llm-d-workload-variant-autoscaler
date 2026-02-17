package controller

import (
	"context"
	"fmt"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/config"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BootstrapInitialConfigMaps performs an initial sync of known ConfigMaps before manager start.
// It ensures dynamic ConfigMap-backed settings are loaded before other reconcilers and engines run.
func (r *ConfigMapReconciler) BootstrapInitialConfigMaps(ctx context.Context) error {
	logger := log.FromContext(ctx)

	if r.Config == nil {
		err := fmt.Errorf("config is nil")
		logger.Error(err, "Config is nil in ConfigMapReconciler bootstrap")
		return err
	}

	systemNamespace := config.SystemNamespace()
	targets := []struct {
		name      string
		namespace string
		isGlobal  bool
	}{
		{name: config.SaturationConfigMapName(), namespace: systemNamespace, isGlobal: true},
		{name: config.DefaultScaleToZeroConfigMapName, namespace: systemNamespace, isGlobal: true},
	}

	if watchNamespace := r.Config.WatchNamespace(); watchNamespace != "" && watchNamespace != systemNamespace {
		targets = append(targets,
			struct {
				name      string
				namespace string
				isGlobal  bool
			}{name: config.SaturationConfigMapName(), namespace: watchNamespace, isGlobal: false},
			struct {
				name      string
				namespace string
				isGlobal  bool
			}{name: config.DefaultScaleToZeroConfigMapName, namespace: watchNamespace, isGlobal: false},
		)
	}

	for _, target := range targets {
		if err := r.bootstrapConfigMap(ctx, target.name, target.namespace, target.isGlobal); err != nil {
			r.Config.MarkConfigMapsBootstrapFailed(err)
			return err
		}
	}

	r.Config.MarkConfigMapsBootstrapComplete()
	logger.Info("Initial ConfigMap bootstrap completed", "targets", len(targets))
	return nil
}

func (r *ConfigMapReconciler) bootstrapConfigMap(ctx context.Context, name, namespace string, isGlobal bool) error {
	logger := log.FromContext(ctx)
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("Bootstrap ConfigMap not found, continuing with defaults", "name", name, "namespace", namespace)
			return nil
		}
		return fmt.Errorf("failed to bootstrap ConfigMap %s/%s: %w", namespace, name, err)
	}

	switch name {
	case config.SaturationConfigMapName():
		r.handleSaturationConfigMap(ctx, cm, namespace, isGlobal)
	case config.DefaultScaleToZeroConfigMapName:
		r.handleScaleToZeroConfigMap(ctx, cm, namespace, isGlobal)
	default:
		logger.V(1).Info("Ignoring unrecognized bootstrap ConfigMap", "name", name, "namespace", namespace)
	}

	return nil
}
