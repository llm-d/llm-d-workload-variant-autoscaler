package resources

import (
	"fmt"

	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// creates a llm-d-sim deployment with the specified configuration
func CreateLlmdSimDeployment(namespace, deployName, modelName, appLabel, port string, avgTTFT, avgITL int, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":                       appLabel,
					"llm-d.ai/inferenceServing": "true",
					"llm-d.ai/model":            "ms-sim-llm-d-modelservice",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                       appLabel,
						"llm-d.ai/inferenceServing": "true",
						"llm-d.ai/model":            "ms-sim-llm-d-modelservice",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            appLabel,
							Image:           "ghcr.io/llm-d/llm-d-inference-sim:latest",
							ImagePullPolicy: corev1.PullAlways,
							Args: []string{
								"--model",
								modelName,
								"--port",
								port,
								fmt.Sprintf("--time-to-first-token=%d", avgTTFT),
								fmt.Sprintf("--inter-token-latency=%d", avgITL),
								"--mode=random",
								"--enable-kvcache",
								"--kv-cache-size=1024",
								"--block-size=16",
							},
							Env: []corev1.EnvVar{
								{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.name",
									},
								}},
								{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.namespace",
									},
								}},
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8000, Name: appLabel, Protocol: corev1.ProtocolTCP},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}
}

// creates a service for the llm-d-sim deployment
func CreateLlmdSimService(namespace, serviceName, appLabel string, nodePort, port int) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                       appLabel,
				"llm-d.ai/inferenceServing": "true",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":                       appLabel,
				"llm-d.ai/inferenceServing": "true",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       appLabel,
					Port:       int32(port),
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt32(int32(port)),
					NodePort:   int32(nodePort),
				},
			},
			Type: corev1.ServiceTypeNodePort,
		},
	}
}

// creates a ServiceMonitor for llm-d-sim metrics collection
func CreateLlmdSimServiceMonitor(name, namespace, targetNamespace, appLabel string) *promoperator.ServiceMonitor {
	return &promoperator.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":     appLabel,
				"release": "kube-prometheus-stack",
			},
		},
		Spec: promoperator.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": appLabel,
				},
			},
			Endpoints: []promoperator.Endpoint{
				{
					Port:     appLabel,
					Path:     "/metrics",
					Interval: promoperator.Duration("15s"),
				},
			},
			NamespaceSelector: promoperator.NamespaceSelector{
				MatchNames: []string{targetNamespace},
			},
		},
	}
}
