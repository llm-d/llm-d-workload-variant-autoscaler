/*
Copyright 2025 The llm-d Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scalefromzero

import (
	"context"
	"errors"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	wvav1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/datastore"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/executor"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
)

// NOTE: This is a placeholder for the scale-from-zero engine implementation.
// The actual logic for the scale-from-zero engine should be implemented here.

type Engine struct {
	client        client.Client
	executor      executor.Executor
	Datastore     datastore.Datastore
	DynamicClient dynamic.Interface
	Mapper        meta.RESTMapper
}

// NewEngine creates a new instance of the scale-from-zero engine.
func NewEngine(client client.Client, mapper meta.RESTMapper, config *rest.Config, ds datastore.Datastore) (*Engine, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	engine := Engine{
		client:        client,
		Datastore:     ds,
		DynamicClient: dynamicClient,
		Mapper:        mapper,
	}

	// TODO: replace by an hybrid, polling and reactive executor when available
	engine.executor = executor.NewPollingExecutor(executor.PollingConfig{
		Config: executor.Config{
			OptimizeFunc: engine.optimize,
		},
		Interval:     100 * time.Millisecond, // frequent polling to quickly detect scale-from-zero opportunities
		RetryBackoff: 100 * time.Millisecond,
	})

	return &engine, nil
}

// StartOptimizeLoop starts the optimization loop for the scale-from-zero engine.
// It runs until the context is cancelled.
func (e *Engine) StartOptimizeLoop(ctx context.Context) {
	e.executor.Start(ctx)
}

// optimize performs the optimization logic.
func (e *Engine) optimize(ctx context.Context) error {
	// Get all inactive (replicas == 0) VAs
	inactiveVAs, err := utils.InactiveVariantAutoscaling(ctx, e.client)
	if err != nil {
		return err
	}

	ctrl.Log.V(logging.DEBUG).Info("Found inactive VariantAutoscaling resources", "count", len(inactiveVAs))

	var wg sync.WaitGroup
	const maxConcurrency = 20 // TO DO: Make this configurable via a flag or environment variable.
	sem := make(chan struct{}, maxConcurrency)
	errorCh := make(chan error, maxConcurrency)

	for _, va := range inactiveVAs {
		select {
		case <-ctx.Done():
			ctrl.Log.V(logging.DEBUG).Info("Context cancelled, exiting optimize loop")
			return ctx.Err()
		default:
			ctrl.Log.V(logging.DEBUG).Info("Processing variant", "name", va.Name)
			wg.Add(1)

			// This call blocks if the channel is full (concurrency limit reached)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				err := e.processInactiveVariant(ctx, va)
				if err != nil {
					ctrl.Log.V(logging.DEBUG).Error(err, "Error Processing variant", "name", va.Name)
					errorCh <- err
				} else {
					errorCh <- nil
				}
			}()
		}

	}

	go func() {
		wg.Wait()
		close(errorCh)
	}()

	// Aggregate errors
	var aggregatedErrors []error
	for err := range errorCh {
		if err != nil {
			aggregatedErrors = append(aggregatedErrors, err)
		}
	}
	if len(aggregatedErrors) > 0 {
		return errors.Join(aggregatedErrors...)
	}
	return nil
}

// ProcessInactiveVariant processes a single inactive VariantAutoscaling resource.
func (e *Engine) processInactiveVariant(ctx context.Context, va wvav1alpha1.VariantAutoscaling) error {
	objAPI := va.GetScaleTargetAPI()
	objKind := va.GetScaleTargetKind()
	objName := va.GetScaleTargetName()

	// Parse Group, Version, Kind, Resource
	gvr, err := GetResourceForKind(e.Mapper, objAPI, objKind)
	if err != nil {
		return err
	}

	unstructuredObj, err := e.DynamicClient.Resource(gvr).Namespace(va.Namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Extract Labels for the pods created by the ScaleTarget object
	labels, found, err := unstructured.NestedStringMap(unstructuredObj.Object, "spec", "template", "metadata", "labels")
	if err != nil {
		return err
	}

	if !found {
		return errors.New("labels are missing for target workload object")
	}

	// Find target EPP for metrics collection
	pool, err := e.Datastore.PoolGetFromLabels(labels)
	if err != nil {
		return err
	}

	epp := pool.EndpointPicker

	// For Tests only (REMOVE LATER)
	ctrl.Log.V(logging.DEBUG).Info(
		"Target EndpointPicker resolved for inactive variant",
		"service", epp.ServiceName,
		"namespace", epp.Namespace,
		"metricsPort", epp.MetricsPortNumber,
	)

	// TODO: Create EPP source and query metrics port
	return nil
}
