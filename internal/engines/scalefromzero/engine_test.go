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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsV1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	vav1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	poolreconciler "github.com/llm-d-incubation/workload-variant-autoscaler/internal/controller"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/datastore"
	unittestutil "github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"
	"sigs.k8s.io/gateway-api-inference-extension/apix/v1alpha2"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/common"
	utiltest "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/testing"
)

var (
	selector_v1     = map[string]string{"app": "vllm_v1"}
	namespace       = "pool1-ns"
	resourceName    = "resource-name"
	acceleratorName = "A100"
	modelId         = "unsloth/Meta-Llama-3.1-8B"
	variantCost     = float64(5)
)

func TestScaleFromZeroEngine(t *testing.T) {
	gvk := schema.GroupVersionKind{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "InferencePool",
	}
	pool1 := utiltest.MakeInferencePool("pool1").
		Namespace(namespace).
		Selector(selector_v1).
		TargetPorts(8080).
		EndpointPickerRef("epp-pool1-svc").ObjRef()
	pool1.SetGroupVersionKind(gvk)

	tests := []struct {
		name             string
		pool             *v1.InferencePool
		resourceReplicas int32
		labels           map[string]string
		datastoreSize    int
		wantErr          bool
	}{
		{
			name:             "one inactiveVariant: successful scalefromzero optimization",
			pool:             pool1,
			labels:           map[string]string{"app": "vllm_v1"},
			datastoreSize:    1,
			resourceReplicas: 0,
		},
		{
			name:             "zero inactiveVariant: skipped scalefromzero optimization",
			pool:             pool1,
			labels:           map[string]string{"app": "vllm_v1"},
			datastoreSize:    1,
			resourceReplicas: 1,
		},
		{
			name:             "Error from labels of inferencePool and deployment not matched",
			pool:             pool1,
			labels:           map[string]string{"vllm": "test"},
			datastoreSize:    1,
			resourceReplicas: 0,
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool1 := tt.pool

			va := unittestutil.CreateVariantAutoscalingResource(namespace, resourceName, modelId, acceleratorName, variantCost)
			dp := unittestutil.MakeDeployment(resourceName, namespace, tt.resourceReplicas, tt.labels)
			svc := unittestutil.MakeService("epp-pool1-svc", namespace)

			scheme := runtime.NewScheme()
			_ = clientgoscheme.AddToScheme(scheme)
			_ = v1alpha2.Install(scheme)
			_ = v1.Install(scheme)
			_ = vav1alpha1.AddToScheme(scheme)
			_ = appsV1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			fakeClientInitialObjs := []client.Object{pool1, dp, va, svc}
			fakeDynamicClientInitialObject := []runtime.Object{dp}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(fakeClientInitialObjs...).
				Build()

			fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(scheme, fakeDynamicClientInitialObject...)

			// Create a request for the existing resource.
			namespacedName := types.NamespacedName{Name: pool1.Name, Namespace: pool1.Namespace}
			gknn := common.GKNN{
				NamespacedName: namespacedName,
				GroupKind: schema.GroupKind{
					Group: pool1.GroupVersionKind().Group,
					Kind:  pool1.GroupVersionKind().Kind,
				},
			}

			req := ctrl.Request{NamespacedName: namespacedName}
			ctx := context.Background()

			ds := datastore.NewDatastore()
			inferencePoolReconciler := &poolreconciler.InferencePoolReconciler{Reader: fakeClient, Datastore: ds, PoolGKNN: gknn}

			// (1) Reconcile inferencePool and store generated endpointPool in the datastore
			if _, err := inferencePoolReconciler.Reconcile(ctx, req); err != nil {
				t.Errorf("Unexpected InferencePool reconcile error: %v", err)
			}

			// Check the size of the datastore
			assert.Equal(t, len(ds.PoolList()), tt.datastoreSize, "There should be one EndpointPool in the datastore")

			// (2) Create scalefromzero engine loop
			mapper := testrestmapper.TestOnlyStaticRESTMapper(scheme, schema.GroupVersion{Group: "apps", Version: "v1"})

			engine := &Engine{
				client:        fakeClient,
				executor:      nil,
				Datastore:     ds,
				DynamicClient: fakeDynamicClient,
				Mapper:        mapper,
			}

			// Call the optimize function.
			err := engine.optimize(ctx)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
