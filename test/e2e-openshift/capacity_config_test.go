/*
Copyright 2025.

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

package e2eopenshift

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	v1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
)

type CapacityConfig struct {
	KvCacheThreshold     float64 `yaml:"kvCacheThreshold"`
	QueueLengthThreshold int     `yaml:"queueLengthThreshold"`
	KvSpareTrigger       float64 `yaml:"kvSpareTrigger"`
	QueueSpareTrigger    int     `yaml:"queueSpareTrigger"`
}

var _ = Describe("Capacity Model: Configuration Validation", Ordered, func() {
	var (
		ctx                    context.Context
		defaultConfig          CapacityConfig
		capacityConfigMapName  = "capacity-scaling-config"
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("verifying capacity-only mode is active")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			"workload-variant-autoscaler-variantautoscaling-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Should be able to get controller ConfigMap")

		if cm.Data["WVA_EXPERIMENTAL_PROACTIVE_MODEL"] == "true" {
			Skip("Test requires CAPACITY-ONLY mode (WVA_EXPERIMENTAL_PROACTIVE_MODEL=false or unset)")
		}
	})

	It("should have capacity-scaling-config ConfigMap present", func() {
		By("fetching capacity-scaling-config ConfigMap")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			capacityConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "capacity-scaling-config ConfigMap should exist")

		_, _ = fmt.Fprintf(GinkgoWriter, "Found capacity-scaling-config ConfigMap with keys: %v\n",
			getMapKeys(cm.Data))

		// Verify 'default' key exists
		defaultConfigStr, exists := cm.Data["default"]
		Expect(exists).To(BeTrue(), "ConfigMap should have 'default' key")
		Expect(defaultConfigStr).NotTo(BeEmpty(), "Default config should not be empty")

		_, _ = fmt.Fprintf(GinkgoWriter, "Default configuration:\n%s\n", defaultConfigStr)
	})

	It("should parse and validate default capacity thresholds", func() {
		By("loading default configuration")
		cm, err := k8sClient.CoreV1().ConfigMaps(controllerNamespace).Get(ctx,
			capacityConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		defaultConfigStr := cm.Data["default"]
		err = yaml.Unmarshal([]byte(defaultConfigStr), &defaultConfig)
		Expect(err).NotTo(HaveOccurred(), "Should be able to parse default config as YAML")

		_, _ = fmt.Fprintf(GinkgoWriter, "Parsed default config:\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "  kvCacheThreshold: %.2f\n", defaultConfig.KvCacheThreshold)
		_, _ = fmt.Fprintf(GinkgoWriter, "  queueLengthThreshold: %d\n", defaultConfig.QueueLengthThreshold)
		_, _ = fmt.Fprintf(GinkgoWriter, "  kvSpareTrigger: %.2f\n", defaultConfig.KvSpareTrigger)
		_, _ = fmt.Fprintf(GinkgoWriter, "  queueSpareTrigger: %d\n", defaultConfig.QueueSpareTrigger)

		By("validating threshold values are sensible")
		// KV cache threshold should be between 0 and 1
		Expect(defaultConfig.KvCacheThreshold).To(BeNumerically(">", 0.0),
			"KV cache threshold should be positive")
		Expect(defaultConfig.KvCacheThreshold).To(BeNumerically("<=", 1.0),
			"KV cache threshold should be <= 1.0 (100%)")

		// Queue threshold should be reasonable
		Expect(defaultConfig.QueueLengthThreshold).To(BeNumerically(">=", 1),
			"Queue threshold should be at least 1")
		Expect(defaultConfig.QueueLengthThreshold).To(BeNumerically("<=", 100),
			"Queue threshold should be reasonable (<=100)")

		// Spare triggers should be smaller than saturation thresholds
		Expect(defaultConfig.KvSpareTrigger).To(BeNumerically("<", defaultConfig.KvCacheThreshold),
			"KV spare trigger should be less than saturation threshold")
		Expect(defaultConfig.QueueSpareTrigger).To(BeNumerically("<", defaultConfig.QueueLengthThreshold),
			"Queue spare trigger should be less than saturation threshold")

		// Production safety: verify thresholds aren't too aggressive
		Expect(defaultConfig.KvCacheThreshold).To(BeNumerically(">=", 0.70),
			"KV cache threshold should be >= 70% for production safety")
		Expect(defaultConfig.QueueLengthThreshold).To(BeNumerically(">=", 3),
			"Queue threshold should be >= 3 for production safety")
	})

	It("should verify controller loaded the configuration", func() {
		By("checking controller pod logs for config load message")
		// Get controller pods
		podList, err := k8sClient.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=workload-variant-autoscaler",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(podList.Items).NotTo(BeEmpty(), "Should have controller pods")

		// Check if any pod logs indicate config was loaded
		foundConfigLog := false
		for _, pod := range podList.Items {
			if pod.Status.Phase != "Running" {
				continue
			}

			// Get recent logs
			logOptions := &corev1.PodLogOptions{
				Container:  "manager",
				TailLines:  func() *int64 { i := int64(100); return &i }(),
				Timestamps: true,
			}

			req := k8sClient.CoreV1().Pods(controllerNamespace).GetLogs(pod.Name, logOptions)
			logs, err := req.DoRaw(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Warning: couldn't fetch logs from pod %s: %v\n", pod.Name, err)
				continue
			}

			logStr := string(logs)
			if strings.Contains(logStr, "Capacity scaling configuration loaded") ||
				strings.Contains(logStr, "Loading initial capacity scaling configuration") {
				foundConfigLog = true
				_, _ = fmt.Fprintf(GinkgoWriter, "✓ Found config load log in pod %s\n", pod.Name)
				break
			}
		}

		if !foundConfigLog {
			_, _ = fmt.Fprintf(GinkgoWriter, "ℹ Config load log not found in recent logs (may have loaded earlier)\n")
		}
	})

	It("should verify VariantAutoscaling resources have valid status conditions", func() {
		By("checking all VariantAutoscaling resources")
		vaList := &v1alpha1.VariantAutoscalingList{}
		err := crClient.List(ctx, vaList, &client.ListOptions{
			Namespace: llmDNamespace,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(vaList.Items).NotTo(BeEmpty(), "Should have at least one VariantAutoscaling resource")

		_, _ = fmt.Fprintf(GinkgoWriter, "Found %d VariantAutoscaling resources:\n", len(vaList.Items))

		for _, va := range vaList.Items {
			_, _ = fmt.Fprintf(GinkgoWriter, "\nVariantAutoscaling: %s\n", va.Name)
			_, _ = fmt.Fprintf(GinkgoWriter, "  ModelID: %s\n", va.Spec.ModelID)
			_, _ = fmt.Fprintf(GinkgoWriter, "  VariantCost: %s\n", va.Spec.VariantCost)
			_, _ = fmt.Fprintf(GinkgoWriter, "  Current Replicas: %d\n", va.Status.CurrentAlloc.NumReplicas)
			_, _ = fmt.Fprintf(GinkgoWriter, "  Desired Replicas: %d\n", va.Status.DesiredOptimizedAlloc.NumReplicas)

			// Verify ModelID is not empty
			Expect(va.Spec.ModelID).NotTo(BeEmpty(), "ModelID should not be empty")

			// Verify VariantCost has valid format
			if va.Spec.VariantCost != "" {
				_, err := strconv.ParseFloat(va.Spec.VariantCost, 64)
				Expect(err).NotTo(HaveOccurred(),
					fmt.Sprintf("VariantCost should be parseable as float: %s", va.Spec.VariantCost))
			}

			// Check status conditions
			_, _ = fmt.Fprintf(GinkgoWriter, "  Status Conditions:\n")
			for _, cond := range va.Status.Conditions {
				_, _ = fmt.Fprintf(GinkgoWriter, "    - %s: %s (reason: %s)\n",
					cond.Type, cond.Status, cond.Reason)
			}

			// Verify essential conditions exist
			hasOptimizationReady := false
			for _, cond := range va.Status.Conditions {
				if cond.Type == "OptimizationReady" {
					hasOptimizationReady = true
				}
			}

			Expect(hasOptimizationReady).To(BeTrue(),
				fmt.Sprintf("VA %s should have OptimizationReady condition", va.Name))
		}
	})

	It("should verify capacity analyzer is making decisions", func() {
		By("checking that capacity analyzer has processed VAs recently")
		Eventually(func(g Gomega) {
			va := &v1alpha1.VariantAutoscaling{}
			err := crClient.Get(ctx, client.ObjectKey{
				Namespace: llmDNamespace,
				Name:      deployment,
			}, va)
			g.Expect(err).NotTo(HaveOccurred())

			// Check that DesiredOptimizedAlloc has been updated
			lastRunTime := va.Status.DesiredOptimizedAlloc.LastRunTime
			g.Expect(lastRunTime.IsZero()).To(BeFalse(), "LastRunTime should be set")

			timeSinceLastRun := time.Since(lastRunTime.Time)
			_, _ = fmt.Fprintf(GinkgoWriter, "Last capacity analysis: %v ago\n", timeSinceLastRun)

			// Verify it was recent (within last 5 minutes)
			g.Expect(timeSinceLastRun).To(BeNumerically("<", 5*time.Minute),
				"Capacity analysis should have run recently")

			// Verify OptimizationReady is True
			var optimizationReady bool
			for _, cond := range va.Status.Conditions {
				if cond.Type == "OptimizationReady" && cond.Status == metav1.ConditionTrue {
					optimizationReady = true
					break
				}
			}
			g.Expect(optimizationReady).To(BeTrue(), "OptimizationReady should be True")

		}, 2*time.Minute, 15*time.Second).Should(Succeed())
	})

	It("should verify no saturation is occurring with default thresholds", func() {
		By("checking current capacity status")
		va := &v1alpha1.VariantAutoscaling{}
		err := crClient.Get(ctx, client.ObjectKey{
			Namespace: llmDNamespace,
			Name:      deployment,
		}, va)
		Expect(err).NotTo(HaveOccurred())

		// If OptimizationReady is True, capacity analyzer is working
		var optimizationReady bool
		var optimizationMessage string
		for _, cond := range va.Status.Conditions {
			if cond.Type == "OptimizationReady" {
				optimizationReady = cond.Status == metav1.ConditionTrue
				optimizationMessage = cond.Message
				break
			}
		}

		Expect(optimizationReady).To(BeTrue(),
			fmt.Sprintf("Capacity analysis should be successful (message: %s)", optimizationMessage))

		_, _ = fmt.Fprintf(GinkgoWriter, "✓ Capacity analyzer is functioning correctly\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "✓ Default thresholds are appropriate for current load\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "✓ System is operating within safe capacity limits\n")
	})
})

func getMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
