package source

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	llmdv1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
)

var _ = Describe("PodVAMapper", func() {
	var (
		ctx                 context.Context
		deployments         map[string]*appsv1.Deployment
		variantAutoscalings map[string]*llmdv1alpha1.VariantAutoscaling
	)

	BeforeEach(func() {
		ctx = context.Background()
		deployments = make(map[string]*appsv1.Deployment)
		variantAutoscalings = make(map[string]*llmdv1alpha1.VariantAutoscaling)
	})

	Describe("FindVAForPod", func() {
		It("should find VA for a pod through its deployment", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llama-deploy",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "llama",
						},
					},
				},
			}
			deployments["llama-deploy"] = deployment

			variantAutoscalings["llama-va"] = &llmdv1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llama-va",
					Namespace: "default",
				},
				Spec: llmdv1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
						Kind:       "Deployment",
						Name:       "llama-deploy",
						APIVersion: "apps/v1",
					},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llama-deploy-abc123-xyz",
					Namespace: "default",
					Labels: map[string]string{
						"app": "llama",
					},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod).
				Build()

			mapper := NewPodVAMapper(fakeClient)
			result := mapper.FindVAForPod(ctx, "llama-deploy-abc123-xyz", "default", deployments, variantAutoscalings)
			Expect(result).To(Equal("llama-va"))
		})

		It("should return empty when pod has no matching deployment", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			mapper := NewPodVAMapper(fakeClient)
			result := mapper.FindVAForPod(ctx, "unknown-pod", "default", deployments, variantAutoscalings)
			Expect(result).To(BeEmpty())
		})

		It("should return empty when deployment has no matching VA", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphan-deploy",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "orphan",
						},
					},
				},
			}
			deployments["orphan-deploy"] = deployment

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphan-deploy-abc123",
					Namespace: "default",
					Labels: map[string]string{
						"app": "orphan",
					},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod).
				Build()

			mapper := NewPodVAMapper(fakeClient)
			result := mapper.FindVAForPod(ctx, "orphan-deploy-abc123", "default", deployments, variantAutoscalings)
			Expect(result).To(BeEmpty())
		})

		It("should not match VA in different namespace", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llama-deploy",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "llama",
						},
					},
				},
			}
			deployments["llama-deploy"] = deployment

			variantAutoscalings["llama-va"] = &llmdv1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llama-va",
					Namespace: "production", // Different namespace
				},
				Spec: llmdv1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
						Kind:       "Deployment",
						Name:       "llama-deploy",
						APIVersion: "apps/v1",
					},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llama-deploy-abc123-xyz",
					Namespace: "default",
					Labels: map[string]string{
						"app": "llama",
					},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod).
				Build()

			mapper := NewPodVAMapper(fakeClient)
			result := mapper.FindVAForPod(ctx, "llama-deploy-abc123-xyz", "default", deployments, variantAutoscalings)
			Expect(result).To(BeEmpty())
		})

		It("should handle nil VA in map", func() {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "target-deploy",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "target",
						},
					},
				},
			}
			deployments["target-deploy"] = deployment

			variantAutoscalings["nil-va"] = nil
			variantAutoscalings["valid-va"] = &llmdv1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-va",
					Namespace: "default",
				},
				Spec: llmdv1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
						Kind:       "Deployment",
						Name:       "target-deploy",
						APIVersion: "apps/v1",
					},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "target-deploy-abc123",
					Namespace: "default",
					Labels: map[string]string{
						"app": "target",
					},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod).
				Build()

			mapper := NewPodVAMapper(fakeClient)
			result := mapper.FindVAForPod(ctx, "target-deploy-abc123", "default", deployments, variantAutoscalings)
			Expect(result).To(Equal("valid-va"))
		})

		It("should handle multiple deployments and VAs", func() {
			// Setup multiple deployments
			for _, name := range []string{"deploy-a", "deploy-b", "deploy-c"} {
				deployments[name] = &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: "default",
					},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": name,
							},
						},
					},
				}
			}

			// Setup corresponding VAs
			variantAutoscalings["va-b"] = &llmdv1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "va-b",
					Namespace: "default",
				},
				Spec: llmdv1alpha1.VariantAutoscalingSpec{
					ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
						Kind:       "Deployment",
						Name:       "deploy-b",
						APIVersion: "apps/v1",
					},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deploy-b-pod-xyz",
					Namespace: "default",
					Labels: map[string]string{
						"app": "deploy-b",
					},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod).
				Build()

			mapper := NewPodVAMapper(fakeClient)
			result := mapper.FindVAForPod(ctx, "deploy-b-pod-xyz", "default", deployments, variantAutoscalings)
			Expect(result).To(Equal("va-b"))
		})
	})
})
