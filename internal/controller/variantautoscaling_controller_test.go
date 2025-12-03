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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	interfaces "github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	logger "github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	utils "github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
	testutils "github.com/llm-d-incubation/workload-variant-autoscaler/test/utils"
)

var _ = Describe("VariantAutoscalings Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		VariantAutoscalings := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}

		BeforeEach(func() {
			logger.Log = zap.NewNop().Sugar()
			ns := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "workload-variant-autoscaler-system",
				},
			}
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).NotTo(HaveOccurred())

			By("creating the required configmap for optimization")
			configMap := testutils.CreateServiceClassConfigMap(ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			configMap = testutils.CreateAcceleratorUnitCostConfigMap(ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			configMap = testutils.CreateVariantAutoscalingConfigMap(configMapName, ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			By("creating the custom resource for the Kind VariantAutoscalings")
			err := k8sClient.Get(ctx, typeNamespacedName, VariantAutoscalings)
			if err != nil && errors.IsNotFound(err) {
				resource := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
					Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
						// Example spec fields, adjust as necessary
						ScaleTargetRef: llmdVariantAutoscalingV1alpha1.CrossVersionObjectReference{
							Kind: "Deployment",
							Name: resourceName,
						},
						ModelID:          "default-default",
						VariantID:        "default-default-A100-1",
						Accelerator:      "A100",
						AcceleratorCount: 1,
						VariantProfile: llmdVariantAutoscalingV1alpha1.VariantProfile{
							PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
								DecodeParms:  map[string]string{"alpha": "20.28", "beta": "0.72"},
								PrefillParms: map[string]string{"gamma": "0", "delta": "0"},
							},
							MaxBatchSize: 4,
						},
						SLOClassRef: llmdVariantAutoscalingV1alpha1.ConfigMapKeyRef{
							Name: "premium",
							Key:  "default-default",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance VariantAutoscalings")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Deleting the configmap resources")
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-classes-config",
					Namespace: "workload-variant-autoscaler-system",
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "accelerator-unit-costs",
					Namespace: "workload-variant-autoscaler-system",
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			mockPromAPI := &testutils.MockPromAPI{
				QueryResults: map[string]model.Value{},
				QueryErrors:  map[string]error{},
			}
			controllerReconciler := &VariantAutoscalingReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				PromAPI: mockPromAPI,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When handling error conditions on missing config maps", func() {
		BeforeEach(func() {
			logger.Log = zap.NewNop().Sugar()
		})

		It("should fail on missing serviceClass ConfigMap", func() {
			By("Creating VariantAutoscaling without required ConfigMaps")
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.readServiceClassConfig(ctx, "service-classes-config", configMapNamespace)
			Expect(err).To(HaveOccurred(), "Expected error when reading missing serviceClass ConfigMap")
		})

		It("should fail on missing accelerator ConfigMap", func() {
			By("Creating VariantAutoscaling without required ConfigMaps")
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.readAcceleratorConfig(ctx, "accelerator-unit-costs", configMapNamespace)
			Expect(err).To(HaveOccurred(), "Expected error when reading missing accelerator ConfigMap")
		})

		It("should fail on missing variant autoscaling optimization ConfigMap", func() {
			By("Creating VariantAutoscaling without required ConfigMaps")
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.readOptimizationConfig(ctx)
			Expect(err).To(HaveOccurred(), "Expected error when reading missing variant autoscaling optimization ConfigMap")
		})
	})

	Context("When validating configurations", func() {
		const configResourceName = "config-test-resource"

		BeforeEach(func() {
			logger.Log = zap.NewNop().Sugar()
			ns := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "workload-variant-autoscaler-system",
				},
			}
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).NotTo(HaveOccurred())

			By("creating the required configmaps")
			configMap := testutils.CreateServiceClassConfigMap(ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).NotTo(HaveOccurred())

			configMap = testutils.CreateAcceleratorUnitCostConfigMap(ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).NotTo(HaveOccurred())

			configMap = testutils.CreateVariantAutoscalingConfigMap(configMapName, ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			By("Deleting the configmap resources")
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-classes-config",
					Namespace: "workload-variant-autoscaler-system",
				},
			}
			err := k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "accelerator-unit-costs",
					Namespace: "workload-variant-autoscaler-system",
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		})

		It("should return empty on variant autoscaling optimization ConfigMap with missing interval value", func() {
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// delete correct configMap
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err := k8sClient.Delete(ctx, configMap)
			Expect(err).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name": "workload-variant-autoscaler",
					},
				},
				Data: map[string]string{
					"PROMETHEUS_BASE_URL": "https://kube-prometheus-stack-prometheus.workload-variant-autoscaler-monitoring.svc.cluster.local:9090",
					"GLOBAL_OPT_INTERVAL": "",
					"GLOBAL_OPT_TRIGGER":  "false",
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			interval, err := controllerReconciler.readOptimizationConfig(ctx)
			Expect(err).NotTo(HaveOccurred(), "Unexpected error when reading variant autoscaling optimization ConfigMap with missing interval")
			Expect(interval).To(Equal(""), "Expected empty interval value")
		})

		It("should return empty on variant autoscaling optimization ConfigMap with missing prometheus base URL", func() {
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// delete correct configMap
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err := k8sClient.Delete(ctx, configMap)
			Expect(err).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name": "workload-variant-autoscaler",
					},
				},
				Data: map[string]string{
					"PROMETHEUS_BASE_URL": "",
					"GLOBAL_OPT_INTERVAL": "60s",
					"GLOBAL_OPT_TRIGGER":  "false",
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			prometheusURL, err := controllerReconciler.getPrometheusConfigFromConfigMap(ctx)
			Expect(err).NotTo(HaveOccurred(), "Unexpected error when reading variant autoscaling optimization ConfigMap with missing Prometheus URL")
			Expect(prometheusURL).To(BeNil(), "Expected empty Prometheus URL")
		})

		It("should return error on VA optimization ConfigMap with missing prometheus base URL and no env variable", func() {
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// delete correct configMap
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err := k8sClient.Delete(ctx, configMap)
			Expect(err).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name": "workload-variant-autoscaler",
					},
				},
				Data: map[string]string{
					"PROMETHEUS_BASE_URL": "",
					"GLOBAL_OPT_INTERVAL": "60s",
					"GLOBAL_OPT_TRIGGER":  "false",
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			_, err = controllerReconciler.getPrometheusConfig(ctx)
			Expect(err).To(HaveOccurred(), "It should fail when neither env variable nor Prometheus URL are found")
		})

		It("should return default values on variant autoscaling optimization ConfigMap with missing TLS values", func() {
			controllerReconciler := &VariantAutoscalingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// delete correct configMap
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err := k8sClient.Delete(ctx, configMap)
			Expect(err).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/name": "workload-variant-autoscaler",
					},
				},
				Data: map[string]string{
					"PROMETHEUS_BASE_URL":                 "https://kube-prometheus-stack-prometheus.workload-variant-autoscaler-monitoring.svc.cluster.local:9090",
					"GLOBAL_OPT_INTERVAL":                 "60s",
					"GLOBAL_OPT_TRIGGER":                  "false",
					"PROMETHEUS_TLS_INSECURE_SKIP_VERIFY": "true",
					// no values set for TLS config - dev env
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			prometheusConfig, err := controllerReconciler.getPrometheusConfigFromConfigMap(ctx)
			Expect(err).NotTo(HaveOccurred(), "It should not fail when neither env variable nor Prometheus URL are found")

			Expect(prometheusConfig.BaseURL).To(Equal("https://kube-prometheus-stack-prometheus.workload-variant-autoscaler-monitoring.svc.cluster.local:9090"), "Expected Base URL to be set")
			Expect(prometheusConfig.InsecureSkipVerify).To(BeTrue(), "Expected Insecure Skip Verify to be true")

			Expect(prometheusConfig.CACertPath).To(Equal(""), "Expected CA Cert Path to be empty")
			Expect(prometheusConfig.ClientCertPath).To(Equal(""), "Expected Client Cert path to be empty")
			Expect(prometheusConfig.ClientKeyPath).To(Equal(""), "Expected Client Key path to be empty")
			Expect(prometheusConfig.BearerToken).To(Equal(""), "Expected Bearer Token to be empty")
			Expect(prometheusConfig.TokenPath).To(Equal(""), "Expected Token Path to be empty")
			Expect(prometheusConfig.ServerName).To(Equal(""), "Expected Server Name to be empty")
		})

		It("should validate variant profile", func() {
			By("Creating VariantAutoscaling with invalid variant profile")
			resource := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configResourceName,
					Namespace: "default",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: llmdVariantAutoscalingV1alpha1.CrossVersionObjectReference{
						Kind: "Deployment",
						Name: configResourceName,
					},
					ModelID:          "default-default",
					VariantID:        "default-default-INVALID-1",
					Accelerator:      "INVALID_GPU",
					AcceleratorCount: 0, // Invalid count (minimum is 1)
					VariantProfile: llmdVariantAutoscalingV1alpha1.VariantProfile{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms:  map[string]string{"alpha": "invalid", "beta": "invalid"},
							PrefillParms: map[string]string{"gamma": "invalid", "delta": "invalid"},
						},
						MaxBatchSize: 0, // Invalid batch size (minimum is 1)
					},
					SLOClassRef: llmdVariantAutoscalingV1alpha1.ConfigMapKeyRef{
						Name: "premium",
						Key:  "default-default",
					},
				},
			}
			err := k8sClient.Create(ctx, resource)
			Expect(err).To(HaveOccurred()) // Expect validation error at API level
			Expect(err.Error()).To(ContainSubstring("Invalid value"))
		})

		It("should handle empty ModelID value", func() {
			By("Creating VariantAutoscaling with empty ModelID")
			resource := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-model-id",
					Namespace: "default",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: llmdVariantAutoscalingV1alpha1.CrossVersionObjectReference{
						Kind: "Deployment",
						Name: "invalid-model-id",
					},
					ModelID:          "", // Empty ModelID
					VariantID:        "test-A100-1",
					Accelerator:      "A100",
					AcceleratorCount: 1,
					VariantProfile: llmdVariantAutoscalingV1alpha1.VariantProfile{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms:  map[string]string{"alpha": "0.28", "beta": "0.72"},
							PrefillParms: map[string]string{"gamma": "0", "delta": "0"},
						},
						MaxBatchSize: 4,
					},
					SLOClassRef: llmdVariantAutoscalingV1alpha1.ConfigMapKeyRef{
						Name: "premium",
						Key:  "default-default",
					},
				},
			}
			err := k8sClient.Create(ctx, resource)
			Expect(err).To(HaveOccurred()) // Expect validation error at API level
			Expect(err.Error()).To(ContainSubstring("spec.modelID"))
		})

		It("should handle empty accelerator field", func() {
			By("Creating VariantAutoscaling with empty accelerator")
			resource := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-accelerator",
					Namespace: "default",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: llmdVariantAutoscalingV1alpha1.CrossVersionObjectReference{
						Kind: "Deployment",
						Name: "empty-accelerator",
					},
					ModelID:          "default-default",
					VariantID:        "default-default--1", // Invalid - empty accelerator portion
					Accelerator:      "",                   // Empty accelerator
					AcceleratorCount: 1,
					VariantProfile: llmdVariantAutoscalingV1alpha1.VariantProfile{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms:  map[string]string{"alpha": "0.28", "beta": "0.72"},
							PrefillParms: map[string]string{"gamma": "0", "delta": "0"},
						},
						MaxBatchSize: 4,
					},
					SLOClassRef: llmdVariantAutoscalingV1alpha1.ConfigMapKeyRef{
						Name: "premium",
						Key:  "default-default",
					},
				},
			}
			err := k8sClient.Create(ctx, resource)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.accelerator"))
		})

		It("should handle empty SLOClassRef", func() {
			By("Creating VariantAutoscaling with no SLOClassRef")
			resource := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-slo-class-ref",
					Namespace: "default",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: llmdVariantAutoscalingV1alpha1.CrossVersionObjectReference{
						Kind: "Deployment",
						Name: "empty-slo-class-ref",
					},
					ModelID:          "default-default",
					VariantID:        "default-default-A100-1",
					Accelerator:      "A100",
					AcceleratorCount: 1,
					VariantProfile: llmdVariantAutoscalingV1alpha1.VariantProfile{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms:  map[string]string{"alpha": "0.28", "beta": "0.72"},
							PrefillParms: map[string]string{"gamma": "0", "delta": "0"},
						},
						MaxBatchSize: 4,
					},
					SLOClassRef: llmdVariantAutoscalingV1alpha1.ConfigMapKeyRef{
						// no configuration for SLOClassRef
					},
				},
			}
			err := k8sClient.Create(ctx, resource)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.sloClassRef"))
		})
	})

	Context("When handling multiple VariantAutoscalings", func() {
		const totalVAs = 3

		var CreateServiceClassConfigMap = func(controllerNamespace string, models ...string) *v1.ConfigMap {
			data := map[string]string{}

			// Build premium.yaml with all models
			premiumModels := ""
			freemiumModels := ""

			for _, model := range models {
				premiumModels += fmt.Sprintf("  - model: %s\n    slo-tpot: 24\n    slo-ttft: 500\n", model)
				freemiumModels += fmt.Sprintf("  - model: %s\n    slo-tpot: 200\n    slo-ttft: 2000\n", model)
			}

			data["premium.yaml"] = fmt.Sprintf(`name: Premium
priority: 1
data:
%s`, premiumModels)

			data["freemium.yaml"] = fmt.Sprintf(`name: Freemium
priority: 10
data:
%s`, freemiumModels)

			return &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-classes-config",
					Namespace: controllerNamespace,
				},
				Data: data,
			}
		}

		BeforeEach(func() {
			logger.Log = zap.NewNop().Sugar()
			ns := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "workload-variant-autoscaler-system",
				},
			}
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).NotTo(HaveOccurred())

			By("creating the required configmaps")
			// Use custom configmap creation function
			var modelNames []string
			for i := range totalVAs {
				modelNames = append(modelNames, fmt.Sprintf("model-%d-model-%d", i, i))
			}
			configMap := CreateServiceClassConfigMap(ns.Name, modelNames...)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			configMap = testutils.CreateAcceleratorUnitCostConfigMap(ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			configMap = testutils.CreateVariantAutoscalingConfigMap(configMapName, ns.Name)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			By("Creating VariantAutoscaling resources and Deployments")
			for i := range totalVAs {
				modelID := fmt.Sprintf("model-%d-model-%d", i, i)
				name := fmt.Sprintf("multi-test-resource-%d", i)

				d := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: "default",
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: utils.Ptr(int32(1)),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": name},
						},
						Template: v1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{"app": name},
							},
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name:  "test-container",
										Image: "quay.io/infernoautoscaler/vllme:0.2.1-multi-arch",
										Ports: []v1.ContainerPort{{ContainerPort: 80}},
									},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, d)).To(Succeed())

				r := &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: "default",
						Labels: map[string]string{
							"inference.optimization/acceleratorName": "A100",
						},
					},
					Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
						ScaleTargetRef: llmdVariantAutoscalingV1alpha1.CrossVersionObjectReference{
							Kind: "Deployment",
							Name: name,
						},
						ModelID:          modelID,
						VariantID:        modelID + "-A100-1",
						Accelerator:      "A100",
						AcceleratorCount: 1,
						VariantProfile: llmdVariantAutoscalingV1alpha1.VariantProfile{
							PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
								DecodeParms:  map[string]string{"alpha": "0.28", "beta": "0.72"},
								PrefillParms: map[string]string{"gamma": "0", "delta": "0"},
							},
							MaxBatchSize: 4,
						},
						SLOClassRef: llmdVariantAutoscalingV1alpha1.ConfigMapKeyRef{
							Name: "premium",
							Key:  modelID,
						},
					},
				}
				Expect(k8sClient.Create(ctx, r)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Deleting the configmap resources")
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-classes-config",
					Namespace: "workload-variant-autoscaler-system",
				},
			}
			err := k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "accelerator-unit-costs",
					Namespace: "workload-variant-autoscaler-system",
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}
			err = k8sClient.Delete(ctx, configMap)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			var variantAutoscalingList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
			err = k8sClient.List(ctx, &variantAutoscalingList)
			Expect(err).NotTo(HaveOccurred(), "Failed to list VariantAutoscaling resources")

			var deploymentList appsv1.DeploymentList
			err = k8sClient.List(ctx, &deploymentList, client.InNamespace("default"))
			Expect(err).NotTo(HaveOccurred(), "Failed to list deployments")

			// Clean up all deployments
			for i := range deploymentList.Items {
				deployment := &deploymentList.Items[i]
				if strings.HasPrefix(deployment.Spec.Template.Labels["app"], "multi-test-resource") {
					err = k8sClient.Delete(ctx, deployment)
					Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), "Failed to delete deployment")
				}
			}

			// Clean up all VariantAutoscaling resources
			for i := range variantAutoscalingList.Items {
				err = k8sClient.Delete(ctx, &variantAutoscalingList.Items[i])
				Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred(), "Failed to delete VariantAutoscaling resource")
			}
		})

		It("should filter out VAs marked for deletion", func() {
			var variantAutoscalingList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
			err := k8sClient.List(ctx, &variantAutoscalingList)
			Expect(err).NotTo(HaveOccurred(), "Failed to list VariantAutoscaling resources")
			filterActiveVariantAutoscalings(variantAutoscalingList.Items)
			Expect(len(variantAutoscalingList.Items)).To(Equal(3), "All VariantAutoscaling resources should be active before deletion")

			// Delete the VAs (this sets DeletionTimestamp)
			for i := range totalVAs {
				Expect(k8sClient.Delete(ctx, &variantAutoscalingList.Items[i])).To(Succeed())
			}

			err = k8sClient.List(ctx, &variantAutoscalingList)
			Expect(err).NotTo(HaveOccurred(), "Failed to list VariantAutoscaling resources")
			filterActiveVariantAutoscalings(variantAutoscalingList.Items)
			Expect(len(variantAutoscalingList.Items)).To(Equal(0), "No active VariantAutoscaling resources should be found")
		})

		It("should prepare active VAs for optimization", func() {
			// Create a mock Prometheus API with valid metric data that passes validation
			mockPromAPI := &testutils.MockPromAPI{
				QueryResults: map[string]model.Value{
					// Default: return a vector with one sample to pass validation
				},
				QueryErrors: map[string]error{},
			}

			controllerReconciler := &VariantAutoscalingReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				PromAPI: mockPromAPI,
			}

			By("Reading the required configmaps")
			accMap, err := controllerReconciler.readAcceleratorConfig(ctx, "accelerator-unit-costs", configMapNamespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to read accelerator config")
			Expect(accMap).NotTo(BeNil(), "Accelerator config map should not be nil")

			serviceClassMap, err := controllerReconciler.readServiceClassConfig(ctx, "service-classes-config", configMapNamespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to read service class config")
			Expect(serviceClassMap).NotTo(BeNil(), "Service class config map should not be nil")

			var variantAutoscalingList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
			err = k8sClient.List(ctx, &variantAutoscalingList)
			Expect(err).NotTo(HaveOccurred(), "Failed to list VariantAutoscaling resources")
			activeVAs := filterActiveVariantAutoscalings(variantAutoscalingList.Items)
			Expect(len(activeVAs)).To(Equal(totalVAs), "All VariantAutoscaling resources should be active")

			// Prepare system data for VAs
			By("Preparing the system data for optimization")
			// WVA operates in unlimited mode - no inventory data needed
			systemData := utils.CreateSystemData(accMap, serviceClassMap)
			Expect(systemData).NotTo(BeNil(), "System data should not be nil")

			updateList, vaMap, allAnalyzerResponses, err := controllerReconciler.prepareVariantAutoscalings(ctx, activeVAs, accMap, serviceClassMap, systemData)

			Expect(err).NotTo(HaveOccurred(), "prepareVariantAutoscalings should not return an error")
			Expect(vaMap).NotTo(BeNil(), "VA map should not be nil")
			Expect(allAnalyzerResponses).NotTo(BeNil(), "Analyzer responses should not be nil")
			Expect(len(updateList.Items)).To(Equal(totalVAs), "UpdatedList should be the same number of all active VariantAutoscalings")

			var vaNames []string
			for _, va := range activeVAs {
				vaNames = append(vaNames, va.Name)
			}

			for _, updatedVa := range updateList.Items {
				Expect(vaNames).To(ContainElement(updatedVa.Name), fmt.Sprintf("Active VariantAutoscaling list should contain %s", updatedVa.Name))
				// In single-variant architecture, Accelerator is in Spec, not Status
				Expect(updatedVa.Spec.Accelerator).To(Equal("A100"), fmt.Sprintf("Accelerator for %s should be \"A100\"", updatedVa.Name))
				Expect(updatedVa.Status.CurrentAlloc.NumReplicas).To(Equal(int32(1)), fmt.Sprintf("Current NumReplicas for %s should be 1 after preparation", updatedVa.Name))
				Expect(updatedVa.Status.DesiredOptimizedAlloc.NumReplicas).To(BeZero(), fmt.Sprintf("Desired NumReplicas for %s should be zero after preparation", updatedVa.Name))
			}
		})

		It("should set MetricsAvailable condition when metrics validation fails", func() {
			By("Creating a mock Prometheus API that returns no metrics")
			mockPromAPI := &testutils.MockPromAPI{
				QueryResults: map[string]model.Value{},
				QueryErrors:  map[string]error{},
			}

			controllerReconciler := &VariantAutoscalingReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				PromAPI: mockPromAPI,
			}

			By("Reading the required configmaps")
			accMap, err := controllerReconciler.readAcceleratorConfig(ctx, "accelerator-unit-costs", configMapNamespace)
			Expect(err).NotTo(HaveOccurred())

			serviceClassMap, err := controllerReconciler.readServiceClassConfig(ctx, "service-classes-config", configMapNamespace)
			Expect(err).NotTo(HaveOccurred())

			var variantAutoscalingList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
			err = k8sClient.List(ctx, &variantAutoscalingList)
			Expect(err).NotTo(HaveOccurred())

			activeVAs := filterActiveVariantAutoscalings(variantAutoscalingList.Items)
			Expect(len(activeVAs)).To(BeNumerically(">", 0))

			By("Preparing system data and calling prepareVariantAutoscalings")
			systemData := utils.CreateSystemData(accMap, serviceClassMap)

			_, _, _, err = controllerReconciler.prepareVariantAutoscalings(ctx, activeVAs, accMap, serviceClassMap, systemData)
			Expect(err).NotTo(HaveOccurred())

			By("Checking that MetricsAvailable condition is set to False")
			for _, va := range activeVAs {
				var updatedVa llmdVariantAutoscalingV1alpha1.VariantAutoscaling
				err = k8sClient.Get(ctx, types.NamespacedName{Name: va.Name, Namespace: va.Namespace}, &updatedVa)
				Expect(err).NotTo(HaveOccurred())

				metricsCondition := llmdVariantAutoscalingV1alpha1.GetCondition(&updatedVa, llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable)
				if metricsCondition != nil {
					Expect(metricsCondition.Status).To(Equal(metav1.ConditionFalse),
						fmt.Sprintf("MetricsAvailable condition should be False for %s", va.Name))
					Expect(metricsCondition.Reason).To(Or(
						Equal(llmdVariantAutoscalingV1alpha1.ReasonPrometheusError),
						Equal(llmdVariantAutoscalingV1alpha1.ReasonMetricsMissing),
					))
				}
			}
		})

		It("should set OptimizationReady condition when optimization succeeds", func() {
			By("Using a working mock Prometheus API with sample data")
			mockPromAPI := &testutils.MockPromAPI{
				QueryResults: map[string]model.Value{
					// Add default responses for common queries
				},
				QueryErrors: map[string]error{},
			}

			controllerReconciler := &VariantAutoscalingReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				PromAPI: mockPromAPI,
			}

			By("Performing a full reconciliation")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that conditions are set correctly")
			var variantAutoscalingList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
			err = k8sClient.List(ctx, &variantAutoscalingList)
			Expect(err).NotTo(HaveOccurred())

			for _, va := range variantAutoscalingList.Items {
				if va.DeletionTimestamp.IsZero() {
					metricsCondition := llmdVariantAutoscalingV1alpha1.GetCondition(&va, llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable)
					if metricsCondition != nil && metricsCondition.Status == metav1.ConditionTrue {
						optimizationCondition := llmdVariantAutoscalingV1alpha1.GetCondition(&va, llmdVariantAutoscalingV1alpha1.TypeOptimizationReady)
						Expect(optimizationCondition).NotTo(BeNil(),
							fmt.Sprintf("OptimizationReady condition should be set for %s", va.Name))
					}
				}
			}
		})
	})

	Context("ServiceMonitor Watch", func() {
		var (
			controllerReconciler *VariantAutoscalingReconciler
			fakeRecorder         *record.FakeRecorder
		)

		BeforeEach(func() {
			logger.Log = zap.NewNop().Sugar()
			fakeRecorder = record.NewFakeRecorder(10)
			controllerReconciler = &VariantAutoscalingReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: fakeRecorder,
			}
		})

		Context("handleServiceMonitorEvent", func() {
			It("should log and emit event when ServiceMonitor is being deleted", func() {
				By("Creating a ServiceMonitor with deletion timestamp")
				serviceMonitor := &unstructured.Unstructured{}
				serviceMonitor.SetGroupVersionKind(serviceMonitorGVK)
				serviceMonitor.SetName(serviceMonitorName)
				serviceMonitor.SetNamespace(configMapNamespace)
				now := metav1.Now()
				serviceMonitor.SetDeletionTimestamp(&now)

				By("Calling handleServiceMonitorEvent")
				result := controllerReconciler.handleServiceMonitorEvent(ctx, serviceMonitor)

				By("Verifying no reconciliation is triggered")
				Expect(result).To(BeEmpty())

				By("Verifying event was emitted")
				select {
				case event := <-fakeRecorder.Events:
					Expect(event).To(ContainSubstring("ServiceMonitorDeleted"))
					Expect(event).To(ContainSubstring(serviceMonitorName))
				case <-time.After(2 * time.Second):
					Fail("Expected event to be emitted but none was received")
				}
			})

			It("should not emit event when ServiceMonitor is created", func() {
				By("Creating a ServiceMonitor without deletion timestamp")
				serviceMonitor := &unstructured.Unstructured{}
				serviceMonitor.SetGroupVersionKind(serviceMonitorGVK)
				serviceMonitor.SetName(serviceMonitorName)
				serviceMonitor.SetNamespace(configMapNamespace)

				By("Calling handleServiceMonitorEvent")
				result := controllerReconciler.handleServiceMonitorEvent(ctx, serviceMonitor)

				By("Verifying no reconciliation is triggered")
				Expect(result).To(BeEmpty())

				By("Verifying no error event was emitted")
				Consistently(fakeRecorder.Events).ShouldNot(Receive(ContainSubstring("ServiceMonitorDeleted")))
			})

			It("should handle non-unstructured objects gracefully", func() {
				By("Creating a non-unstructured object")
				configMap := &v1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-configmap",
						Namespace: configMapNamespace,
					},
				}

				By("Calling handleServiceMonitorEvent with non-unstructured object")
				result := controllerReconciler.handleServiceMonitorEvent(ctx, configMap)

				By("Verifying no reconciliation is triggered")
				Expect(result).To(BeEmpty())
			})
		})
	})

	Context("convertCapacityTargetsToDecisions", func() {
		BeforeEach(func() {
			logger.Log = zap.NewNop().Sugar()
		})

		It("should include ActionNoChange decisions in the result", func() {
			By("Creating test data where target equals current replicas")
			capacityTargets := map[string]int{
				"variant-a": 3, // Same as current - should be ActionNoChange
				"variant-b": 5, // Scale up
				"variant-c": 2, // Same as current - should be ActionNoChange
			}

			capacityAnalysis := &interfaces.ModelCapacityAnalysis{
				ModelID:   "test-model",
				Namespace: "test-ns",
				VariantAnalyses: []interfaces.VariantCapacityAnalysis{
					{VariantName: "variant-a", AcceleratorName: "A100", Cost: 10.0},
					{VariantName: "variant-b", AcceleratorName: "A100", Cost: 10.0},
					{VariantName: "variant-c", AcceleratorName: "A100", Cost: 10.0},
				},
			}

			variantStates := []interfaces.VariantReplicaState{
				{VariantName: "variant-a", CurrentReplicas: 3, DesiredReplicas: 3},
				{VariantName: "variant-b", CurrentReplicas: 3, DesiredReplicas: 3},
				{VariantName: "variant-c", CurrentReplicas: 2, DesiredReplicas: 2},
			}

			By("Converting capacity targets to decisions")
			decisions := convertCapacityTargetsToDecisions(capacityTargets, capacityAnalysis, variantStates)

			By("Verifying all variants are included in decisions")
			Expect(len(decisions)).To(Equal(3), "All 3 variants should have decisions including ActionNoChange")

			By("Verifying ActionNoChange decisions are present")
			decisionMap := make(map[string]interfaces.VariantDecision)
			for _, d := range decisions {
				decisionMap[d.VariantName] = d
			}

			// variant-a: target=3, current=3 -> ActionNoChange
			Expect(decisionMap).To(HaveKey("variant-a"))
			Expect(decisionMap["variant-a"].Action).To(Equal(interfaces.ActionNoChange),
				"variant-a should have ActionNoChange (target=current=3)")
			Expect(decisionMap["variant-a"].TargetReplicas).To(Equal(3))
			Expect(decisionMap["variant-a"].CurrentReplicas).To(Equal(3))

			// variant-b: target=5, current=3 -> ActionScaleUp
			Expect(decisionMap).To(HaveKey("variant-b"))
			Expect(decisionMap["variant-b"].Action).To(Equal(interfaces.ActionScaleUp),
				"variant-b should have ActionScaleUp (target=5 > current=3)")
			Expect(decisionMap["variant-b"].TargetReplicas).To(Equal(5))
			Expect(decisionMap["variant-b"].CurrentReplicas).To(Equal(3))

			// variant-c: target=2, current=2 -> ActionNoChange
			Expect(decisionMap).To(HaveKey("variant-c"))
			Expect(decisionMap["variant-c"].Action).To(Equal(interfaces.ActionNoChange),
				"variant-c should have ActionNoChange (target=current=2)")
			Expect(decisionMap["variant-c"].TargetReplicas).To(Equal(2))
			Expect(decisionMap["variant-c"].CurrentReplicas).To(Equal(2))
		})

		It("should set correct fields for ActionNoChange decisions", func() {
			By("Creating test data with only ActionNoChange scenario")
			capacityTargets := map[string]int{
				"stable-variant": 4,
			}

			capacityAnalysis := &interfaces.ModelCapacityAnalysis{
				ModelID:   "stable-model",
				Namespace: "prod-ns",
				VariantAnalyses: []interfaces.VariantCapacityAnalysis{
					{VariantName: "stable-variant", AcceleratorName: "H100", Cost: 20.0},
				},
			}

			variantStates := []interfaces.VariantReplicaState{
				{VariantName: "stable-variant", CurrentReplicas: 4, DesiredReplicas: 4},
			}

			By("Converting to decisions")
			decisions := convertCapacityTargetsToDecisions(capacityTargets, capacityAnalysis, variantStates)

			By("Verifying decision fields")
			Expect(len(decisions)).To(Equal(1))
			d := decisions[0]

			Expect(d.VariantName).To(Equal("stable-variant"))
			Expect(d.Namespace).To(Equal("prod-ns"))
			Expect(d.ModelID).To(Equal("stable-model"))
			Expect(d.Action).To(Equal(interfaces.ActionNoChange))
			Expect(d.CurrentReplicas).To(Equal(4))
			Expect(d.TargetReplicas).To(Equal(4))
			Expect(d.CapacityBased).To(BeTrue())
			Expect(d.CapacityOnly).To(BeTrue())
			Expect(d.ModelBasedDecision).To(BeFalse())
			Expect(d.AcceleratorName).To(Equal("H100"))
			Expect(d.Cost).To(Equal(20.0))
			Expect(d.Reason).To(ContainSubstring("no-change"))
		})

		It("should handle scale down decisions correctly", func() {
			By("Creating test data with scale down scenario")
			capacityTargets := map[string]int{
				"overprovisioned": 2,
			}

			capacityAnalysis := &interfaces.ModelCapacityAnalysis{
				ModelID:   "test-model",
				Namespace: "test-ns",
				VariantAnalyses: []interfaces.VariantCapacityAnalysis{
					{VariantName: "overprovisioned", AcceleratorName: "A100", Cost: 10.0},
				},
			}

			variantStates := []interfaces.VariantReplicaState{
				{VariantName: "overprovisioned", CurrentReplicas: 5, DesiredReplicas: 5},
			}

			By("Converting to decisions")
			decisions := convertCapacityTargetsToDecisions(capacityTargets, capacityAnalysis, variantStates)

			By("Verifying scale down decision")
			Expect(len(decisions)).To(Equal(1))
			Expect(decisions[0].Action).To(Equal(interfaces.ActionScaleDown))
			Expect(decisions[0].CurrentReplicas).To(Equal(5))
			Expect(decisions[0].TargetReplicas).To(Equal(2))
		})
	})

	Context("Capacity Config Cache", func() {
		var (
			ctx                  context.Context
			controllerReconciler *VariantAutoscalingReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			controllerReconciler = &VariantAutoscalingReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(100),
			}
		})

		It("should initialize cache with defaults when ConfigMap is missing", func() {
			By("Initializing cache")
			err := controllerReconciler.InitializeCapacityConfigCache(ctx)

			By("Verifying cache initialization succeeded (uses defaults)")
			Expect(err).NotTo(HaveOccurred())
			Expect(controllerReconciler.isCapacityConfigLoaded()).To(BeTrue())

			By("Verifying default config is in cache")
			configs := controllerReconciler.getCapacityConfigFromCache()
			Expect(configs).To(HaveKey("default"))
			Expect(configs["default"].KvCacheThreshold).To(Equal(0.80))
			Expect(configs["default"].QueueLengthThreshold).To(Equal(5.0))
		})

		It("should load config from ConfigMap when it exists", func() {
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capacity-scaling-config",
					Namespace: configMapNamespace,
				},
				Data: map[string]string{
					"default": `kvCacheThreshold: 0.75
queueLengthThreshold: 10
kvSpareTrigger: 0.15
queueSpareTrigger: 5`,
				},
			}

			By("Creating ConfigMap")
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			By("Initializing cache")
			err := controllerReconciler.InitializeCapacityConfigCache(ctx)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying custom config is loaded")
			configs := controllerReconciler.getCapacityConfigFromCache()
			Expect(configs).To(HaveKey("default"))
			Expect(configs["default"].KvCacheThreshold).To(Equal(0.75))
			Expect(configs["default"].QueueLengthThreshold).To(Equal(10.0))

			By("Cleaning up ConfigMap")
			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
		})

		It("should return copy of cache to prevent external modification", func() {
			By("Initializing cache")
			err := controllerReconciler.InitializeCapacityConfigCache(ctx)
			Expect(err).NotTo(HaveOccurred())

			By("Getting cache copy")
			configs1 := controllerReconciler.getCapacityConfigFromCache()
			configs2 := controllerReconciler.getCapacityConfigFromCache()

			By("Verifying copies are independent")
			configs1["test"] = interfaces.CapacityScalingConfig{KvCacheThreshold: 0.99}
			Expect(configs2).NotTo(HaveKey("test"))
		})

		It("should apply per-model overrides correctly", func() {
			configMap := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capacity-scaling-config",
					Namespace: configMapNamespace,
				},
				Data: map[string]string{
					"default": `kvCacheThreshold: 0.80
queueLengthThreshold: 5
kvSpareTrigger: 0.1
queueSpareTrigger: 3`,
					"custom": `model_id: test/model
namespace: test-ns
kvCacheThreshold: 0.90`,
				},
			}

			By("Creating ConfigMap with override")
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, configMap))).To(Succeed())

			By("Initializing cache")
			err := controllerReconciler.InitializeCapacityConfigCache(ctx)
			Expect(err).NotTo(HaveOccurred())

			By("Getting config for model with override")
			configs := controllerReconciler.getCapacityConfigFromCache()
			config := controllerReconciler.getCapacityScalingConfigForVariant(configs, "test/model", "test-ns")

			By("Verifying override is applied")
			Expect(config.KvCacheThreshold).To(Equal(0.90))
			// Verify other fields inherit from default
			Expect(config.QueueLengthThreshold).To(Equal(5.0))
			Expect(config.QueueSpareTrigger).To(Equal(3.0))

			By("Cleaning up ConfigMap")
			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
		})
	})
})
