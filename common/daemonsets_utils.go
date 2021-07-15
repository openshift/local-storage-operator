package common

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	v1helper "k8s.io/component-helpers/scheduling/corev1"
)

func NodeSelectorMatchesNodeLabels(node *corev1.Node, nodeSelector *corev1.NodeSelector) (bool, error) {
	if nodeSelector == nil {
		return true, nil
	}
	if node == nil {
		return false, fmt.Errorf("the node var is nil")
	}

	matches, err := v1helper.MatchNodeSelectorTerms(node, nodeSelector)
	return matches, err
}

func KubeProxySideCar() corev1.Container {
	return corev1.Container{
		Name:  "kube-rbac-proxy",
		Image: GetKubeRBACProxyImage(),
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: int32(9393),
				Name:          "metrics",
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Args: []string{
			"--logtostderr=true",
			"--secure-listen-address=0.0.0.0:9393",
			"--upstream=http://127.0.0.1:8383/",
			"--tls-cert-file=/etc/tls/private/tls.crt",
			"--tls-private-key-file=/etc/tls/private/tls.key",
		},

		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      metricsServingCert,
				MountPath: "/etc/tls/private",
			},
		},
	}

}
