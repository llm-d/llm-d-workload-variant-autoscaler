/*
Copyright 2025 The Kubernetes Authors.

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

package datastore

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	poolutil "github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils/pool"
	unittestutil "github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"
	testutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/testing"
)

func TestDatastore(t *testing.T) {
	pool1Selector := map[string]string{"app": "vllm_v1"}
	pool1 := testutil.MakeInferencePool("pool1").
		Namespace("default").
		Selector(pool1Selector).
		EndpointPickerRef("epp-svc").ObjRef()
	tests := []struct {
		name                 string
		inferencePool        *v1.InferencePool
		labels               map[string]string
		wantPool             *v1.InferencePool
		wantErr              error
		wantLabelsMatch      bool
		listResultLen        int
		clearDeleteResultLen int
	}{
		{
			name:                 "Ready when InferencePool exists in data store",
			inferencePool:        pool1,
			wantPool:             pool1,
			wantLabelsMatch:      false,
			clearDeleteResultLen: 0,
			listResultLen:        1,
		},
		{
			name:                 "Labels matched",
			inferencePool:        pool1,
			labels:               map[string]string{"app": "vllm_v1"},
			wantPool:             pool1,
			wantLabelsMatch:      true,
			clearDeleteResultLen: 0,
			listResultLen:        1,
		},
		{
			name:    "Not ready when InferencePool is nil",
			wantErr: errPoolIsNull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Define the EPP service object
			eppSvc := unittestutil.MakeService("epp-svc", "default")

			// Set up the scheme.
			scheme := runtime.NewScheme()
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(eppSvc).
				Build()

			ds := NewDatastore()
			ctx := context.Background()

			ep, err := poolutil.InferencePoolToEndpointPool(ctx, fakeClient, tt.inferencePool)
			if err != nil {
				t.Errorf("Unexpected InferencePoolToEndpointPool error: %v", err)
			}

			// Test PoolSet
			gotErr := ds.PoolSet(ep)
			if diff := cmp.Diff(tt.wantErr, gotErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("Unexpected error diff (+got/-want): %s", diff)
			}

			// Test PoolGetFromLabels
			if tt.wantLabelsMatch {
				wantPoolMatch, err := poolutil.InferencePoolToEndpointPool(ctx, fakeClient, tt.wantPool)
				if err != nil {
					t.Errorf("Unexpected InferencePoolToEndpointPool error: %v", err)
				}

				gotPoolMatch, err := ds.PoolGetFromLabels(tt.labels)
				if err != nil {
					t.Errorf("Unexpected PoolGetFromLabels error: %v", err)
				}

				if diff := cmp.Diff(wantPoolMatch, gotPoolMatch); diff != "" {
					t.Errorf("Unexpected labels match diff (+got/-want): %s", diff)
				}
			}

			if tt.wantErr == nil {
				// Test PoolGet
				wantPool, err := poolutil.InferencePoolToEndpointPool(ctx, fakeClient, tt.wantPool)
				if err != nil {
					t.Errorf("Unexpected InferencePoolToEndpointPool error: %v", err)
				}

				gotPool, err := ds.PoolGet(ep.Name)
				if err != nil {
					t.Errorf("failed to add endpoint into the datastore: %v", err)
				}

				if diff := cmp.Diff(wantPool, gotPool); diff != "" {
					t.Errorf("Unexpected pool diff (+got/-want): %s", diff)
				}

				// Test Delete & PoolList
				ds.PoolDelete(ep.Name)
				assert.Equal(t, len(ds.PoolList()), tt.clearDeleteResultLen, "Pools map should have the expected length after item deleted")

				if err := ds.PoolSet(ep); err != nil {
					t.Errorf("failed to add endpoint into the datastore: %v", err)
				}
				assert.Equal(t, len(ds.PoolList()), tt.listResultLen, "Pools map should have the expected length after item added")

			}

			// Test Clear
			ds.Clear()
			assert.Equal(t, len(ds.PoolList()), tt.clearDeleteResultLen, "Pools map should have the expected length after clearing")
		})
	}
}
