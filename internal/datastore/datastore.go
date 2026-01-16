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
	"errors"
	"sync"

	poolutil "github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils/pool"
)

var (
	errPoolNotSynced = errors.New("EndpointPool not found in datastore")
	errPoolIsNull    = errors.New("EndpointPool object is nil, does not exist")
)

// The datastore is a local cache of relevant data for the given InferencePool (currently all pulled from k8s-api)
type Datastore interface {
	// InferencePool operations
	PoolSet(pool *poolutil.EndpointPool) error
	PoolGet(name string) (*poolutil.EndpointPool, error)
	PoolList() []*poolutil.EndpointPool
	PoolGetFromLabels(labels map[string]string) (*poolutil.EndpointPool, error)
	PoolDelete(poolName string)

	// Clears the store state, happens when the pool gets deleted.
	Clear()
}

func NewDatastore() Datastore {
	store := &datastore{
		pools: &sync.Map{},
	}
	return store
}

type datastore struct {
	pools *sync.Map
}

// Datastore operations
func (ds *datastore) PoolSet(pool *poolutil.EndpointPool) error {
	if pool == nil {
		return errPoolIsNull
	}
	ds.pools.Store(pool.Name, pool)
	return nil
}

func (ds *datastore) PoolGet(name string) (*poolutil.EndpointPool, error) {

	pool, exist := ds.pools.Load(name)
	if !exist {
		return nil, errPoolNotSynced
	}

	epp := pool.(*poolutil.EndpointPool)
	return epp, nil
}

func (ds *datastore) PoolGetFromLabels(labels map[string]string) (*poolutil.EndpointPool, error) {
	exist := false
	var ep *poolutil.EndpointPool

	ds.pools.Range(func(k, v any) bool {
		ep = v.(*poolutil.EndpointPool)

		found := poolutil.IsSubset(ep.Selector, labels)
		if found {
			exist = true
			return false
		}
		return true
	})

	if !exist {
		return nil, errPoolNotSynced
	}
	return ep, nil
}

func (ds *datastore) PoolList() []*poolutil.EndpointPool {
	res := []*poolutil.EndpointPool{}
	ds.pools.Range(func(k, v any) bool {
		res = append(res, v.(*poolutil.EndpointPool))
		return true
	})

	return res
}

func (ds *datastore) PoolDelete(name string) {
	ds.pools.Delete(name)
}

func (ds *datastore) Clear() {
	ds.pools.Clear()
}
