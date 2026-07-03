package telemetry

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// meterName is the instrumentation scope shared by all kube-vim meters.
const meterName = "github.com/kube-nfv/kube-vim"

// newMeterProvider builds an OTEL MeterProvider whose metrics are exported in
// Prometheus text format through registry. The registry also carries the Go
// runtime and process collectors so a single /metrics endpoint reports both
// kube-vim instruments and the standard process metrics.
//
// Correlation identifiers (compute id, network id, ...) are always carried as
// datapoint attributes on the kubevim_*_info instruments — never as OTEL
// Resource attributes, which the Prometheus exporter would collapse into a
// single target_info series and thus break per-object joins.
func newMeterProvider(registry *prometheus.Registry) (*sdkmetric.MeterProvider, error) {
	exporter, err := otelprom.New(
		otelprom.WithRegisterer(registry),
		// Drop the otel_scope_* labels the exporter otherwise stamps on every
		// series; they add noise without helping downstream joins.
		otelprom.WithoutScopeInfo(),
	)
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	res := resource.NewWithAttributes(
		"",
		attribute.String("service.name", "kube-vim"),
		attribute.String("service.version", Version),
	)

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	), nil
}
