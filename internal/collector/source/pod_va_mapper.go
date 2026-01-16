package source

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	llmdv1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
)

// PodVAMapper maps pod names to their corresponding VariantAutoscaling objects.
type PodVAMapper struct {
	k8sClient client.Client
}

// NewPodVAMapper creates a new PodVAMapper.
func NewPodVAMapper(k8sClient client.Client) *PodVAMapper {
	return &PodVAMapper{
		k8sClient: k8sClient,
	}
}

// FindVAForPod finds the VariantAutoscaling object for a pod by first finding
// its deployment and then finding the VA that targets that deployment.
//
// Returns the VA name if found, empty string otherwise.
func (m *PodVAMapper) FindVAForPod(
	ctx context.Context,
	podName string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	variantAutoscalings map[string]*llmdv1alpha1.VariantAutoscaling,
) string {
	deploymentName := m.findDeploymentForPod(ctx, podName, namespace, deployments)
	if deploymentName == "" {
		return ""
	}
	return m.findVAForDeployment(deploymentName, namespace, variantAutoscalings)
}

// findDeploymentForPod finds which deployment owns a pod using Kubernetes API.
// Uses the deployment's label selector to find matching pods.
func (m *PodVAMapper) findDeploymentForPod(
	ctx context.Context,
	podName string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
) string {
	logger := ctrl.LoggerFrom(ctx)

	// TODO: optimize
	for deploymentName, deployment := range deployments {
		selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		if err != nil {
			logger.Info("Invalid label selector for deployment", "deployment", deploymentName, "error", err)
			continue
		}

		podList := &corev1.PodList{}
		listOpts := &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: selector,
		}

		if err := m.k8sClient.List(ctx, podList, listOpts); err != nil {
			logger.Info("Failed to list pods for deployment", "deployment", deploymentName, "error", err)
			continue
		}

		for _, pod := range podList.Items {
			if pod.Name == podName {
				return deploymentName
			}
		}
	}
	return ""
}

// findVAForDeployment finds the VariantAutoscaling object that targets a deployment.
func (m *PodVAMapper) findVAForDeployment(
	deploymentName string,
	namespace string,
	variantAutoscalings map[string]*llmdv1alpha1.VariantAutoscaling,
) string {
	for vaName, va := range variantAutoscalings {
		if va == nil {
			continue
		}
		if va.Spec.ScaleTargetRef.Name == deploymentName &&
			va.Namespace == namespace {
			return vaName
		}
	}
	return ""
}
