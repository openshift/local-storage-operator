package localmetrics

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/pkg/common"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sYAML "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateOrUpdateAlertRules installs all LSO alerting rules
func CreateOrUpdateAlertRules(ctx context.Context, client client.Client, namespace string, diskmakerName string, ownerRefs []metav1.OwnerReference) error {
	replacer := strings.NewReplacer(
		"${OBJECT_NAMESPACE}", namespace,
		"${DAEMONSET_NAME}", diskmakerName,
	)
	rule, err := getPrometheusRule(replacer)
	if err != nil {
		return fmt.Errorf("failed to get prometheus rule. %v", err)
	}

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

func getPrometheusRule(replacer *strings.Replacer) (*monitoringv1.PrometheusRule, error) {
	file, err := assets.ReadFile(common.PrometheusRuleTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch prometheus rule file. %v", err)
	}

	ruleYaml := replacer.Replace(string(file))

	var rule monitoringv1.PrometheusRule
	err = k8sYAML.NewYAMLOrJSONDecoder(bytes.NewBufferString(ruleYaml), 1000).Decode(&rule)
	if err != nil {
		return nil, fmt.Errorf("failed to decode prometheus rule file: %s", err)
	}
	return &rule, nil
}
