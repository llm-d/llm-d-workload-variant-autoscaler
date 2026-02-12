package source

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/indexers"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/logging"
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

// FindVAForPod finds the VariantAutoscaling object for a Pod by:
// 1. finding the Deployment owning the Pod
// 2. finding the VariantAutoscaling that targets that Deployment, using indexed lookups.
// Returns the VariantAutoscaling name if found, empty string otherwise.
func (m *PodVAMapper) FindVAForPod(
	ctx context.Context,
	podName string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
) string {
	logger := ctrl.LoggerFrom(ctx)

	deploymentName := m.findDeploymentForPod(ctx, podName, namespace, deployments)
	if deploymentName == "" {
		return ""
	}

	// Use indexed lookup for VariantAutoscaling targeting this Deployment
	va, err := indexers.FindVAForDeployment(ctx, m.k8sClient, deploymentName, namespace)
	if err != nil {
		logger.V(logging.DEBUG).Error(err, "failed to find VariantAutoscaling for deployment", "deployment", deploymentName, "namespace", namespace)
		return ""
	}

	if va == nil {
		logger.V(logging.DEBUG).Info("no VariantAutoscaling matched for deployment", "deployment", deploymentName, "namespace", namespace)
		return ""
	}

	return va.Name
}

// findDeploymentForPod finds which Deployment owns a Pod by traversing owner references.
func (m *PodVAMapper) findDeploymentForPod(
	ctx context.Context,
	podName string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
) string {
	logger := ctrl.LoggerFrom(ctx)

	pod := &corev1.Pod{}
	if err := m.k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: podName}, pod); err != nil {
		logger.V(logging.DEBUG).Error(err, "failed to get pod", "pod", podName, "namespace", namespace)
		return ""
	}

	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.Kind != "ReplicaSet" {
		logger.V(logging.DEBUG).Info("Pod has no ReplicaSet owner", "pod", podName, "namespace", namespace)
		return ""
	}

	rs := &appsv1.ReplicaSet{}
	if err := m.k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, rs); err != nil {
		logger.V(logging.DEBUG).Error(err, "failed to get ReplicaSet", "replicaset", owner.Name, "namespace", namespace)
		return ""
	}

	rsOwner := metav1.GetControllerOf(rs)
	if rsOwner == nil || rsOwner.Kind != "Deployment" {
		logger.V(logging.DEBUG).Info("ReplicaSet has no Deployment owner", "replicaset", owner.Name, "namespace", namespace)
		return ""
	}

	// Verify the Deployment is in our map of tracked Deployments
	deploymentKey := namespace + "/" + rsOwner.Name
	if deploy, ok := deployments[deploymentKey]; ok && deploy != nil && deploy.Namespace == namespace {
		return rsOwner.Name
	}
	return ""
}
