package localmetrics

import (
	"fmt"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metricsConfig struct {
	metricsPath       string
	metricsPort       string
	metricsRegisterer prometheus.Registerer
	metricsGatherer   prometheus.Gatherer
	collectorList     []prometheus.Collector
}

// NewBuilder initializes the configuration builder for a given namespace and service name
func NewMetricsConfig(path, port string, c []prometheus.Collector) metricsConfig {
	return metricsConfig{
		metricsPath:       path,
		metricsPort:       port,
		collectorList:     c,
		metricsRegisterer: prometheus.DefaultRegisterer,
		metricsGatherer:   prometheus.DefaultGatherer,
	}
}

// startMetrics starts the server based on the metricsConfig provided by the user.
func (config metricsConfig) startMetricsServer() error {
	err := registerMetrics(config.metricsRegisterer, config.collectorList)
	if err != nil {
		return err
	}
	metricsHandler := promhttp.InstrumentMetricHandler(
		config.metricsRegisterer, promhttp.HandlerFor(config.metricsGatherer, promhttp.HandlerOpts{}),
	)
	log.Info(fmt.Sprintf("Port: %s", config.metricsPort))
	metricsPort := fmt.Sprintf(":%s", config.metricsPort)
	if free := isPortFree(metricsPort); !free {
		return fmt.Errorf("port %s is not free", config.metricsPort)
	}
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
