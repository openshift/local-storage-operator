package nodedaemon

import (
	"reflect"
	"testing"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtractLVSetInfo(t *testing.T) {

	lvSets := []localv1alpha1.LocalVolumeSet{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "a",
			},
			Spec: localv1alpha1.LocalVolumeSetSpec{
				NodeSelector: &corev1.NodeSelector{
					// 2 terms
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{MatchExpressions: []corev1.NodeSelectorRequirement{}},
						{MatchExpressions: []corev1.NodeSelectorRequirement{}},
					},
				},
				// 2 tolerations
				Tolerations: []corev1.Toleration{
					{
						Key:      "key1",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
					{
						Key:      "key1",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectPreferNoSchedule,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "b",
			},
			Spec: localv1alpha1.LocalVolumeSetSpec{
				NodeSelector: &corev1.NodeSelector{
					// 2 terms
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{MatchExpressions: []corev1.NodeSelectorRequirement{}},
						{MatchExpressions: []corev1.NodeSelectorRequirement{}},
					},
				},
				// 2 tolerations
				Tolerations: []corev1.Toleration{
					{
						Key:      "key1",
						Operator: corev1.TolerationOpEqual,
						Value:    "value1",
						Effect:   corev1.TaintEffectNoSchedule,
					},
					{
						Key:      "key1",
						Operator: corev1.TolerationOpEqual,
						Value:    "value2",
						Effect:   corev1.TaintEffectNoExecute,
					},
				},
			},
		},
	}
	tolerations, ownerRefs, terms := extractLVSetInfo(lvSets)
	// extractLVSetInfo(lvSets)
	assert.Len(t, tolerations, 2*len(lvSets))
	assert.Len(t, ownerRefs, len(lvSets))
	assert.Len(t, terms, 2*len(lvSets))

	// every value exists
	for _, lvSet := range lvSets {
		for _, toleration := range lvSet.Spec.Tolerations {
			found := false
			for _, foundToleration := range tolerations {
				if toleration == foundToleration {
					found = true
					break
				}
			}
			assert.True(t, found, "expected to find toleration: %+v", toleration)
		}

		found := false
		for _, foundOwnerRef := range ownerRefs {
			if foundOwnerRef.Name == lvSet.Name {
				found = true
				break
			}
		}
		assert.True(t, found, "expected to find ownerRef: %q", lvSet.Name)

		for _, term := range lvSet.Spec.NodeSelector.NodeSelectorTerms {
			found := false
			for _, foundTerm := range terms {
				if reflect.DeepEqual(term, foundTerm) {
					found = true
					break
				}
			}
			assert.True(t, found, "expected to find term: %+v", term)
		}
	}

}

func TestExtractLVSetInfoWithNilNodeSelector(t *testing.T) {

	lvSetWithNodeSelector := localv1alpha1.LocalVolumeSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "a",
		},
		Spec: localv1alpha1.LocalVolumeSetSpec{
			NodeSelector: &corev1.NodeSelector{
				// 2 terms
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{MatchExpressions: []corev1.NodeSelectorRequirement{}},
					{MatchExpressions: []corev1.NodeSelectorRequirement{}},
				},
			},
			// 2 tolerations
			Tolerations: []corev1.Toleration{
				{
					Key:      "key1",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				},
				{
					Key:      "key1",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectPreferNoSchedule,
				},
			},
		},
	}
	lvSetWithoutNodeSelector := localv1alpha1.LocalVolumeSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "b",
		},
		Spec: localv1alpha1.LocalVolumeSetSpec{
			// no nodeSelector
			// 2 tolerations
			Tolerations: []corev1.Toleration{
				{
					Key:      "key1",
					Operator: corev1.TolerationOpEqual,
					Value:    "value1",
					Effect:   corev1.TaintEffectNoSchedule,
				},
				{
					Key:      "key1",
					Operator: corev1.TolerationOpEqual,
					Value:    "value2",
					Effect:   corev1.TaintEffectNoExecute,
				},
			},
		},
	}

	count := 3
	for i := 0; i <= count; i++ {
		lvSets := []localv1alpha1.LocalVolumeSet{}
		for j := 0; j <= count; j++ {
			// place empty nodeSelector in ith place
			if i == j {
				lvSets = append(lvSets, lvSetWithoutNodeSelector)
			} else {
				lvSets = append(lvSets, lvSetWithNodeSelector)
			}

		}
		_, _, terms := extractLVSetInfo(lvSets)
		// empty nodeSelector in any spot should result in empty terms
		assert.Len(t, terms, 0)

	}
	for i := 0; i <= count; i++ {
		lvSets := []localv1alpha1.LocalVolumeSet{}
		for j := 0; j <= count; j++ {
			lvSets = append(lvSets, lvSetWithNodeSelector)
		}
		_, _, terms := extractLVSetInfo(lvSets)
		// empty nodeSelector in any spot should result in empty terms
		assert.Len(t, terms, len(lvSets)*2)

	}

}
