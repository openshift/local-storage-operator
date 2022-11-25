package localmetrics

import (
	"bytes"
	"context"
	"fmt"

	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/common"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sYAML "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateOrUpdateAlertRules installs all LSO alerting rules
func CreateOrUpdateAlertRules(ctx context.Context, client client.Client, namespace string, ownerRefs []metav1.OwnerReference) error {
	rule, err := getPrometheusRule()
	if err != nil {
		return fmt.Errorf("failed to get prometheus rule. %v", err)
	}

	rule.SetNamespace(namespace)
	rule.SetOwnerReferences(ownerRefs)

	if _, err = createOrUpdatePrometheusRule(ctx, client, rule); err != nil {
		return fmt.Errorf("failed to enable prometheus rule. %v", err)
	}

	return nil
}

// createOrUpdatePrometheusRule creates prometheusRule object or an error
func createOrUpdatePrometheusRule(ctx context.Context, client client.Client, rule *monitoringv1.PrometheusRule) (*monitoringv1.PrometheusRule, error) {
	namespacedName := types.NamespacedName{Name: rule.Name, Namespace: rule.Namespace}
	klog.InfoS("Reconciling prometheus rule", "namespace", rule.GetNamespace(), "name", rule.GetName())

	oldRule := &monitoringv1.PrometheusRule{}
	err := client.Get(ctx, namespacedName, oldRule)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = client.Create(ctx, rule)
			if err != nil {
				return nil, fmt.Errorf("failed to create prometheusrule %v. %v", namespacedName, err)
			}
			return rule, nil
		}
		return nil, fmt.Errorf("failed to retrieve prometheusrule %v. %v", namespacedName, err)
	}
	oldRule.Spec = rule.Spec
	err = client.Update(ctx, oldRule)
	if err != nil {
		return nil, fmt.Errorf("failed to update prometheusrule %v. %v", namespacedName, err)
	}
	return rule, nil
}

func getPrometheusRule() (*monitoringv1.PrometheusRule, error) {
	file, err := assets.ReadFile(common.PrometheusRuleTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch prometheus rule file. %v", err)
	}

	var rule monitoringv1.PrometheusRule
	err = k8sYAML.NewYAMLOrJSONDecoder(bytes.NewBufferString(string(file)), 10000).Decode(&rule)
	if err != nil {
		return nil, fmt.Errorf("failed to decode prometheus rule file: %s", err)
	}
	return &rule, nil
}
