package localvolumeset

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DaemonSetsAvailable
	DaemonSetsAvailableAndConfigured = "DaemonSetsAvailable"
)

// SetCondition creates or updates a condition of type conditionType in conditions and returns changed
func SetCondition(conditions *[]operatorv1.OperatorCondition, conditionType, conditionMessage string, conditionStatus operatorv1.ConditionStatus) bool {
	newCondition := operatorv1.OperatorCondition{
		Type:               conditionType,
		Status:             conditionStatus,
		Message:            conditionMessage,
		LastTransitionTime: metav1.Now(),
	}
	if len(*conditions) < 1 {
		*conditions = []operatorv1.OperatorCondition{newCondition}
		return true
	}
	for i, condition := range *conditions {
		if condition.Type == conditionType {
			changed := false
			newCondition.LastTransitionTime = condition.LastTransitionTime
			if condition.Status != conditionStatus || condition.LastTransitionTime.IsZero() {
				changed = true
			} else {
				newCondition.LastTransitionTime = condition.LastTransitionTime
			}
			if condition.Message != conditionMessage {
				changed = true
			}
			(*conditions)[i] = newCondition
			return changed
		}
	}
	*conditions = append(*conditions, newCondition)
	return true

}
