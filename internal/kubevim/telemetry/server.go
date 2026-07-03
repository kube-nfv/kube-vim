package telemetry

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// metricsPath is the single route served by the telemetry HTTP server.
	// Health/readiness probes are intentionally not served here — they are a
	// separate concern and must not be gated behind the monitoring toggle.
	metricsPath = "/metrics"

	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// newMetricsServer builds the Prometheus /metrics HTTP server. A
// ReadHeaderTimeout is set to protect against slow-loris style stalls; the
// handler streams from the OTEL-backed registry.
func newMetricsServer(port int, registry *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}))
	return &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(port)),
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
}
