package telemetry

import (
	"context"
	"fmt"
	"runtime"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// registerBuildInfo exposes a kubevim_build_info gauge (value always 1) carrying
// the build version and Go runtime version as labels — the standard cheap
// dashboard/alerting anchor for "what is running".
func registerBuildInfo(meter metric.Meter) error {
	buildInfo, err := meter.Int64ObservableGauge(
		"kubevim.build.info",
		metric.WithDescription("kube-vim build information (value always 1)."),
	)
	if err != nil {
		return fmt.Errorf("create kubevim.build.info gauge: %w", err)
	}
	_, err = meter.RegisterCallback(
		func(_ context.Context, obs metric.Observer) error {
			obs.ObserveInt64(buildInfo, 1, metric.WithAttributes(
				attribute.String("version", Version),
				attribute.String("go_version", runtime.Version()),
			))
			return nil
		},
		buildInfo,
	)
	if err != nil {
		return fmt.Errorf("register build info callback: %w", err)
	}
	return nil
}
