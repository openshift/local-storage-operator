package common

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	corev1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
)

func NodeSelectorMatchesNodeLabels(node *corev1.Node, nodeSelector *corev1.NodeSelector) (bool, error) {
	if nodeSelector == nil {
		return true, nil
	}
	if node == nil {
		return false, fmt.Errorf("the node var is nil")
	}
	matches := corev1helper.MatchNodeSelectorTerms(nodeSelector.NodeSelectorTerms, node.Labels, fields.Set{
		"metadata.name": node.Name,
	})
	return matches, nil
}
