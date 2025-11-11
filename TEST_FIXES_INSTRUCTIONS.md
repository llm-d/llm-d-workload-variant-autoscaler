# E2E OpenShift Test Fixes

## Summary
Two tests failed in the full e2e suite run:
1. **ConfigMap enableScaleToZero test** - HTTP 409 Conflict error (race condition)
2. **ShareGPT Scale-Up test** - Deployment scaled DOWN instead of UP

## Fix 1: ConfigMap Test (hpa_scale_to_zero_test.go)

**Location:** Line 198, in the "should react to ConfigMap enableScaleToZero changes" test

**Problem:** Using stale ConfigMap object causes conflict when controller modifies it between read and update.

**Solution:** Re-fetch ConfigMap before the second update operation.

**Change Required:**
After line 197 (`By("restoring ConfigMap to enable scale-to-zero")`), add these 3 lines:

```go
		// Re-fetch ConfigMap to avoid conflict errors (controller may have modified it)
		configMap, err = k8sClient.CoreV1().ConfigMaps(configMapNamespace).Get(ctx, configMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get ConfigMap for restore")

```

**Full Context (lines 197-202 after fix):**
```go
		By("restoring ConfigMap to enable scale-to-zero")
		// Re-fetch ConfigMap to avoid conflict errors (controller may have modified it)
		configMap, err = k8sClient.CoreV1().ConfigMaps(configMapNamespace).Get(ctx, configMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get ConfigMap for restore")

		configMap.Data[fmt.Sprintf("model.%s", modelKey)] = fmt.Sprintf(`modelID: "%s"
enableScaleToZero: true
retentionPeriod: "4m"`, modelID)
```

---

## Fix 2: ShareGPT Scale-Up Test (sharegpt_scaleup_test.go)

**Location:** Line 87, in the BeforeAll block after HPA verification

**Problem:** Test expects scale-UP but deployment scales DOWN from 2→1 because VA minReplicas defaults to 0.

**Solution:** Set VA minReplicas=1 before starting load test to establish stable baseline.

**Change Required:**
After line 87 (after the `Expect(hpa.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.WVADesiredReplicas)...` line), add this block:

```go

		By("ensuring VA minReplicas is set to 1 for stable baseline")
		va = &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Name: vaName, Namespace: llmDNamespace}, va)
		Expect(err).NotTo(HaveOccurred(), "Should be able to get VA")

		if va.Spec.MinReplicas == nil || *va.Spec.MinReplicas != 1 {
			minReplicas := int32(1)
			va.Spec.MinReplicas = &minReplicas
			err = crClient.Update(ctx, va)
			Expect(err).NotTo(HaveOccurred(), "Should be able to set VA minReplicas=1")
			_, _ = fmt.Fprintf(GinkgoWriter, "Set VA minReplicas=1 for stable baseline, waiting for reconciliation...\n")
			time.Sleep(30 * time.Second)
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "VA minReplicas=%d confirmed\n", *va.Spec.MinReplicas)
```

**Full Context (lines 85-102 after fix):**
```go
		Expect(hpa.Spec.Metrics).To(HaveLen(1), "HPA should have one metric")
		Expect(hpa.Spec.Metrics[0].Type).To(Equal(autoscalingv2.ExternalMetricSourceType), "HPA should use external metrics")
		Expect(hpa.Spec.Metrics[0].External.Metric.Name).To(Equal(constants.WVADesiredReplicas), "HPA should use wva_desired_replicas metric")

		By("ensuring VA minReplicas is set to 1 for stable baseline")
		va = &v1alpha1.VariantAutoscaling{}
		err = crClient.Get(ctx, client.ObjectKey{Name: vaName, Namespace: llmDNamespace}, va)
		Expect(err).NotTo(HaveOccurred(), "Should be able to get VA")

		if va.Spec.MinReplicas == nil || *va.Spec.MinReplicas != 1 {
			minReplicas := int32(1)
			va.Spec.MinReplicas = &minReplicas
			err = crClient.Update(ctx, va)
			Expect(err).NotTo(HaveOccurred(), "Should be able to set VA minReplicas=1")
			_, _ = fmt.Fprintf(GinkgoWriter, "Set VA minReplicas=1 for stable baseline, waiting for reconciliation...\n")
			time.Sleep(30 * time.Second)
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "VA minReplicas=%d confirmed\n", *va.Spec.MinReplicas)
	})

	It("should verify external metrics API is accessible", func() {
```

---

## Skipped Tests

The 5 skipped tests will automatically run once these failures are fixed:
- "should enforce VA minReplicas even with HPA minReplicas=0" (Basic Integration)
- "should trigger HPA to scale up the deployment" (ShareGPT)
- "should scale deployment to match recommended replicas" (ShareGPT)
- "should maintain scaled state while load is active" (ShareGPT)
- "should complete the load generation job successfully" (ShareGPT)

These tests were skipped because Ginkgo's Ordered container skips remaining tests when an earlier test fails.

---

## Apply Changes

Option 1 - Manual editing:
1. Open `test/e2e-openshift/hpa_scale_to_zero_test.go` and add Fix 1
2. Open `test/e2e-openshift/sharegpt_scaleup_test.go` and add Fix 2

Option 2 - Use patch file:
```bash
cd C:\DataD\Work\gpuoptimization\llmd-autoscaler-priv
# Review the patch first
cat test-fixes.patch
# Apply it (may need Git Bash or WSL)
git apply test-fixes.patch
```

---

## After Applying Fixes

Run the full test suite again:
```bash
cd test/e2e-openshift
go clean -testcache
KUBECONFIG='C:\Users\826657756\.kube\config-pokprod001' go test -v -ginkgo.v -count=1 -timeout=60m
```

Expected result: **10 Passed | 0 Failed | 5 Skipped** (instead of 8 Passed | 2 Failed | 5 Skipped)
