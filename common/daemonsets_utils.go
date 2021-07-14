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
