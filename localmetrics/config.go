package localmetrics

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog"
)

const (
	defaultMetricsPath = "/metrics"
	defaultMetricsPort = "8383"
)

type config struct {
	metricsPath       string
	metricsPort       string
	metricsRegisterer prometheus.Registerer
	metricsGatherer   prometheus.Gatherer
	collectorList     []prometheus.Collector
}

type configBuilder struct {
	config config
}

// NewConfigBuilder initializes the config for the local server
func NewConfigBuilder() *configBuilder {
	return &configBuilder{
		config{
			metricsPath:       defaultMetricsPath,
			metricsPort:       defaultMetricsPort,
			collectorList:     nil,
			metricsRegisterer: prometheus.DefaultRegisterer,
			metricsGatherer:   prometheus.DefaultGatherer,
		},
	}
}

func (b *configBuilder) WithPort(port string) *configBuilder {
	b.config.metricsPort = port
	return b
}

func (b *configBuilder) WithPath(path string) *configBuilder {
	if !strings.HasPrefix(path, "/") {
		path = fmt.Sprintf("/%s", path)
	}
	b.config.metricsPath = path
	return b
}

func (b *configBuilder) WithCollectors(collectors []prometheus.Collector) *configBuilder {
	b.config.collectorList = collectors
	return b
}

// Build starts the server based on the metrics config provided by the user.
func (b *configBuilder) Build() error {
	err := registerMetrics(b.config.metricsRegisterer, b.config.collectorList)
	if err != nil {
		return errors.Wrap(err, "failed to register local metrics")
	}

	metricsHandler := promhttp.InstrumentMetricHandler(
		b.config.metricsRegisterer, promhttp.HandlerFor(b.config.metricsGatherer, promhttp.HandlerOpts{}),
	)

	klog.Infof("Port: %s", b.config.metricsPort)
	metricsPort := fmt.Sprintf(":%s", b.config.metricsPort)
	if free := isPortFree(metricsPort); !free {
		return fmt.Errorf("port %s is not free", b.config.metricsPort)
	}

	// start server
	server := &http.Server{
		Addr:    metricsPort,
		Handler: metricsHandler,
	}

	go server.ListenAndServe()

	return nil
}

func isPortFree(port string) bool {
	listener, err := net.Listen("tcp", port)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// registerMetrics registers metrics to prometheus.
func registerMetrics(metricsRegisterer prometheus.Registerer, list []prometheus.Collector) error {
	for _, metric := range list {
		err := metricsRegisterer.Register(metric)
		if err != nil {
			return err
		}
	}
	return nil
}
