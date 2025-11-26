package v1alpha1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// helper: build a valid VariantAutoscaling object
// TODO: move to utils??
func makeValidVA() *VariantAutoscaling {
	return &VariantAutoscaling{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "inferencev1alpha1",
			Kind:       "VariantAutoscaling",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "va-sample",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/name": "workload-variant-autoscaler",
			},
		},
		Spec: VariantAutoscalingSpec{
			ScaleTargetRef: CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "va-sample",
			},
			ModelID:          "model-123",
			VariantID:        "model-123-nvidia-1",
			Accelerator:      "nvidia.com/mig-1g.5gb",
			AcceleratorCount: 1,
			VariantCost:      "10.00",
			SLOClassRef: ConfigMapKeyRef{
				Name: "slo-config",
				Key:  "gold",
			},
			VariantProfile: VariantProfile{
				PerfParms: PerfParms{
					DecodeParms:  map[string]string{"alpha": "0.8", "beta": "0.2"},
					PrefillParms: map[string]string{"gamma": "0.8", "delta": "0.2"},
				},
				MaxBatchSize: 8,
			},
		},
		Status: VariantAutoscalingStatus{
			CurrentAlloc: Allocation{
				NumReplicas: 1,
			},
			DesiredOptimizedAlloc: OptimizedAlloc{
				LastRunTime: metav1.NewTime(time.Unix(1730000000, 0).UTC()),
				NumReplicas: 2,
			},
			Actuation: ActuationStatus{
				Applied: true,
			},
		},
	}
}

func TestSchemeRegistration(t *testing.T) {
	s := runtime.NewScheme()
	if err := SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	kinds, _, err := s.ObjectKinds(&VariantAutoscaling{})
	if err != nil {
		t.Fatalf("ObjectKinds for VariantAutoscaling failed: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatalf("no GVK registered for VariantAutoscaling")
	}

	listKinds, _, err := s.ObjectKinds(&VariantAutoscalingList{})
	if err != nil {
		t.Fatalf("ObjectKinds for VariantAutoscalingList failed: %v", err)
	}
	if len(listKinds) == 0 {
		t.Fatalf("no GVK registered for VariantAutoscalingList")
	}
}

func TestDeepCopyIndependence(t *testing.T) {
	orig := makeValidVA()
	cp := orig.DeepCopy()

	cp.Spec.ModelID = "model-456"
	cp.Spec.SLOClassRef.Name = "slo-config-2"
	cp.Spec.VariantProfile.MaxBatchSize = 16
	cp.Spec.Accelerator = "nvidia.com/mig-3g.20gb"

	if orig.Spec.ModelID == cp.Spec.ModelID {
		t.Errorf("DeepCopy did not create independent copy for Spec.ModelID")
	}
	if orig.Spec.SLOClassRef.Name == cp.Spec.SLOClassRef.Name {
		t.Errorf("DeepCopy did not create independent copy for Spec.SLOClassRef.Name")
	}
	if orig.Spec.Accelerator == cp.Spec.Accelerator {
		t.Errorf("DeepCopy did not create independent copy for Spec.Accelerator")
	}
	if orig.Spec.VariantProfile.MaxBatchSize == cp.Spec.VariantProfile.MaxBatchSize {
		t.Errorf("DeepCopy did not create independent copy for VariantProfile")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	orig := makeValidVA()

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var back VariantAutoscaling
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	ot := orig.Status.DesiredOptimizedAlloc.LastRunTime.Time
	bt := back.Status.DesiredOptimizedAlloc.LastRunTime.Time
	if !ot.Equal(bt) {
		t.Fatalf("LastRunTime mismatch by instant: orig=%v back=%v", ot, bt)
	}

	back.Status.DesiredOptimizedAlloc.LastRunTime = orig.Status.DesiredOptimizedAlloc.LastRunTime

	if !reflect.DeepEqual(orig, &back) {
		t.Errorf("round-trip mismatch:\norig=%#v\nback=%#v", orig, &back)
	}
}

func TestListDeepCopyAndItemsIndependence(t *testing.T) {
	va1 := makeValidVA()
	va2 := makeValidVA()
	va2.Name = "va-other"
	list := &VariantAutoscalingList{
		Items: []VariantAutoscaling{*va1, *va2},
	}

	cp := list.DeepCopy()
	if len(cp.Items) != 2 {
		t.Fatalf("DeepCopy list items count mismatch: got %d", len(cp.Items))
	}
	// mutate copy
	cp.Items[0].Spec.ModelID = "changed"

	if list.Items[0].Spec.ModelID == cp.Items[0].Spec.ModelID {
		t.Errorf("DeepCopy did not isolate list items")
	}
}

func TestStatusOmitEmpty(t *testing.T) {
	empty := &VariantAutoscaling{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "va-empty-status",
			Namespace: "default",
		},
		Spec: VariantAutoscalingSpec{
			ScaleTargetRef: CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "va-empty-status",
			},
			ModelID:          "m",
			VariantID:        "m-gpu-1",
			Accelerator:      "gpu",
			AcceleratorCount: 1,
			SLOClassRef: ConfigMapKeyRef{
				Name: "slo",
				Key:  "bronze",
			},
			VariantProfile: VariantProfile{
				PerfParms: PerfParms{
					DecodeParms:  map[string]string{"alpha": "1", "beta": "1"},
					PrefillParms: map[string]string{"gamma": "1", "delta": "1"},
				},
				MaxBatchSize: 1,
			},
		},
	}

	b, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if !jsonContainsKey(b, "status") {
		t.Fatalf("expected status to be present for non-pointer struct with omitempty; got: %s", string(b))
	}

	// Optional: sanity-check a couple of zero values inside status
	var probe struct {
		Status struct {
			CurrentAlloc struct {
				NumReplicas int32 `json:"numReplicas"`
			} `json:"currentAlloc"`
			DesiredOptimizedAlloc struct {
				LastRunTime *string `json:"lastRunTime"`
				NumReplicas int32   `json:"numReplicas"`
			} `json:"desiredOptimizedAlloc"`
			Actuation struct {
				Applied bool `json:"applied"`
			} `json:"actuation"`
		} `json:"status"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal probe failed: %v", err)
	}
	if probe.Status.CurrentAlloc.NumReplicas != 0 ||
		probe.Status.DesiredOptimizedAlloc.NumReplicas != 0 ||
		probe.Status.Actuation.Applied != false {
		t.Errorf("unexpected non-zero defaults in status: %+v", probe.Status)
	}
	empty.Status.Actuation.Applied = true
	b2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !jsonContainsKey(b2, "status") {
		t.Errorf("status should be present when non-zero, but json did not contain it: %s", string(b2))
	}
}

func TestOptimizedAllocLastRunTimeJSON(t *testing.T) {
	va := makeValidVA()
	// ensure LastRunTime survives marshal/unmarshal with RFC3339 format used by metav1.Time
	raw, err := json.Marshal(va)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	type optimizedAlloc struct {
		Status struct {
			DesiredOptimizedAlloc struct {
				LastRunTime string `json:"lastRunTime"`
			} `json:"desiredOptimizedAlloc"`
		} `json:"status"`
	}
	var probe optimizedAlloc
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe failed: %v", err)
	}
	if probe.Status.DesiredOptimizedAlloc.LastRunTime == "" {
		t.Errorf("expected lastRunTime to be serialized, got empty")
	}
}

func jsonContainsKey(b []byte, key string) bool {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
