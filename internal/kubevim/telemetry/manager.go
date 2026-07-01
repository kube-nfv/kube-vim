// Package telemetry serves kube-vim's own observability: a Prometheus /metrics
// endpoint exposing kube-vim operational metrics and the kubevim_*_info
// correlation metrics that let Prometheus join backend (KubeVirt/kube-OVN/
// SR-IOV) series to ETSI resource IDs. kube-vim never proxies or re-exports
// backend counters — the actual performance counters are scraped directly from
// the source exporters and joined Prometheus-side.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"

	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
)

// defaultMetricsPort mirrors the default in the config schema; used only as a
// fallback when the (defaulted) config value is somehow absent.
const defaultMetricsPort = 9095

// Version is the kube-vim build version reported as a resource attribute and via
// kubevim_build_info. Overridden at build time with -ldflags.
var Version = "dev"

// Manager owns the telemetry MeterProvider and the /metrics HTTP server. When
// monitoring is disabled it is inert: MeterProvider returns a no-op provider and
// Start is a no-op, so callers can wire it unconditionally.
type Manager struct {
	logger   *zap.Logger
	provider metric.MeterProvider
	sdk      *sdkmetric.MeterProvider
	server   *http.Server
	port     int
}

// NewManager builds the telemetry manager. cfg nil/disabled yields an inert
// manager. When enabled it wires an OTEL MeterProvider to a dedicated Prometheus
// registry, registers build-info and the kubevim_*_info correlation gauges
// (backed by the compute and network managers), and prepares the HTTP server.
func NewManager(cfg *config.MonitoringConfig, logger *zap.Logger, computeMgr compute.Manager, networkMgr network.Manager) (*Manager, error) {
	if cfg == nil || cfg.Enabled == nil || !*cfg.Enabled {
		return &Manager{logger: logger, provider: metricnoop.NewMeterProvider()}, nil
	}
	if computeMgr == nil || networkMgr == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "managers", Reason: "compute and network managers are required when monitoring is enabled"}
	}

	port := defaultMetricsPort
	if cfg.MetricsPort != nil {
		port = *cfg.MetricsPort
	}

	registry := prometheus.NewRegistry()
	provider, err := newMeterProvider(registry)
	if err != nil {
		return nil, fmt.Errorf("create meter provider: %w", err)
	}
	meter := provider.Meter(meterName)
	if err := registerBuildInfo(meter); err != nil {
		return nil, err
	}
	if err := registerInfoMetrics(meter, logger, computeMgr, networkMgr); err != nil {
		return nil, err
	}

	return &Manager{
		logger:   logger,
		provider: provider,
		sdk:      provider,
		server:   newMetricsServer(port, registry),
		port:     port,
	}, nil
}

// MeterProvider returns the provider to instrument other components (e.g. the
// gRPC server). It is a no-op provider when monitoring is disabled.
func (m *Manager) MeterProvider() metric.MeterProvider { return m.provider }

// Enabled reports whether the /metrics endpoint will be served.
func (m *Manager) Enabled() bool { return m.server != nil }

// Start serves /metrics until ctx is cancelled, then gracefully shuts the HTTP
// server and the MeterProvider down. It is a no-op when monitoring is disabled.
func (m *Manager) Start(ctx context.Context) error {
	if m.server == nil {
		return nil
	}
	errCh := make(chan error, 1)
	go func() {
		m.logger.Info("metrics server started", zap.Int("port", m.port), zap.String("path", metricsPath))
		if err := m.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := m.server.Shutdown(shutdownCtx); err != nil {
			m.logger.Warn("metrics server shutdown", zap.Error(err))
		}
		if err := m.sdk.Shutdown(shutdownCtx); err != nil {
			m.logger.Warn("meter provider shutdown", zap.Error(err))
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("serve metrics: %w", err)
	}
}
