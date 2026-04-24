package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"
	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/library-go/test/library/metrics"
	framework "github.com/openshift/local-storage-operator/test/framework"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Copied & updated from openshift/origin test/extended/util/client.go
func newPrometheusClient(f *framework.Framework) prometheusv1.API {
	kubeClient := f.KubeClient
	routeClient, err := routev1client.NewForConfig(f.KubeConfig)
	if err != nil {
		panic(fmt.Errorf("failed to create Route client: %w", err))
	}

	var (
		lastErr          error
		prometheusClient prometheusv1.API
	)
	err = wait.PollUntilContextTimeout(context.TODO(), time.Second, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		prometheusClient, err = metrics.NewPrometheusClient(ctx, kubeClient, routeClient)
		if err != nil {
			if ctx.Err() == nil {
				lastErr = err
			}

			return false, nil
		}

		return true, nil
	})
	if err != nil {
		panic(fmt.Errorf("failed to create Prometheus client: %w: %w", err, lastErr))
	}

	return prometheusClient
}

// Wait for a given metric to have a given number of results.
func waitForMetric(t *testing.T, prometheusClient prometheusv1.API, metric string, value model.SampleValue) {
	t.Logf("Waiting for metric %s to be %f", metric, value)
	matcher := gomega.NewWithT(t)
	matcher.Eventually(func() model.SampleValue {
		result, err := runQueryAtTime(t, prometheusClient, metric, time.Now())
		matcher.Expect(err).NotTo(gomega.HaveOccurred())
		if len(result) == 0 {
			// report missing metric as "0"
			t.Logf("Metric %s has no results, reporting zero", metric)
			return 0.0
		}

		t.Logf("Metric %s has %d results, the last value is %f", metric, len(result), result[0].Value)
		return result[0].Value
	}, time.Minute*5, time.Second*5).Should(gomega.Equal(value))
}

// Copied & updated from openshift/origin test/extended/util/prometheus/helpers.go
func runQueryAtTime(t *testing.T, prometheusClient prometheusv1.API, query string, evaluationTime time.Time) (model.Vector, error) {
	var lastErr error
	var result model.Value
	var warnings prometheusv1.Warnings
	for i := range 5 {
		result, warnings, lastErr = prometheusClient.Query(context.TODO(), query, evaluationTime)
		if lastErr == nil {
			break
		}
		t.Logf("error querying metric %s (%d/5): %v", query, i+1, lastErr)
		time.Sleep(10 * time.Second)
	}
	if lastErr != nil {
		return nil, lastErr
	}

	if len(warnings) > 0 {
		t.Logf("#### warnings \n\t%v\n", strings.Join(warnings, "\n\t"))
	}
	if result.Type() != model.ValVector {
		return nil, fmt.Errorf("result type is not the vector: %v", result.Type())
	}

	return result.(model.Vector), nil
}
