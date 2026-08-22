package telemetry

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// The /metrics handler must fan out over both the telemetry registry and
// controller-runtime's global registry, so a series registered in either shows
// up on a single scrape. rest_client_requests_total can't be exercised without a
// real apiserver, so a probe metric stands in for controller-runtime's registry.
func TestMetricsServerGathersBothRegistries(t *testing.T) {
	telemetryReg := prometheus.NewRegistry()
	telemetryReg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubevim_probe_telemetry",
	}))

	ctrlProbe := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubevim_probe_ctrlruntime",
	})
	require.NoError(t, ctrlmetrics.Registry.Register(ctrlProbe))
	t.Cleanup(func() { ctrlmetrics.Registry.Unregister(ctrlProbe) })

	gatherer := prometheus.Gatherers{telemetryReg, ctrlmetrics.Registry}
	srv := newMetricsServer(0, gatherer)

	req := httptest.NewRequest("GET", metricsPath, nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "kubevim_probe_telemetry"), "telemetry registry series missing")
	assert.True(t, strings.Contains(body, "kubevim_probe_ctrlruntime"), "controller-runtime registry series missing")
}
