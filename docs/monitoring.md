# Monitoring & Telemetry

Status: implemented (Phase 1 — metrics endpoint + correlation metrics)

## Problem

kube-vim exposed no telemetry: no `/metrics`, no way to observe the VIM itself,
and no way to attribute backend (KubeVirt / kube-OVN / SR-IOV) metrics to the
ETSI resources kube-vim manages. Operators could see a VM's CPU in KubeVirt but
not answer "which ETSI compute resource / network is this?".

ETSI defines VIM telemetry as two producer interfaces, identical on Or-Vi
(IFA 005 §7.6/§7.7) and Vi-Vnfm (IFA 006 §7.6/§7.7): Performance Management
(PM jobs, thresholds) and Fault Management (alarms). The metric vocabulary lives
in IFA 027 (Measurement Names) and the alarm vocabulary in IFA 045. Both ETSI
interfaces are Subscribe/Notify based. None of this exists in the API yet.

## Approach — three layers, one seam

```
kube-vim OTEL instrumentation  →  Prometheus (scrape + recording rules)  →  PM/FM gRPC API
   (emission — this phase)          (query/store — later)                    (northbound — later)
```

kube-vim is the **emission** layer only. It exposes:

1. its own **operational** metrics (gRPC RED via otelgrpc, Go runtime/process,
   build-info), and
2. **`kubevim_*_info` correlation metrics** — value always `1`, the payload is
   the label set mapping ETSI resource IDs to the backends' native labels.

kube-vim **never proxies or re-exports backend counters**. The actual
performance counters (`kubevirt_vmi_*`, `ovs_vswitch_interface_*`, SR-IOV device
plugin, kubelet volume stats) are scraped **directly from the source exporters**
by Prometheus. Correlation into ETSI-shaped series happens Prometheus-side (later
phase) by joining backend series against the info metrics. Rationale: a process
that re-scrapes and re-emits every backend series just duplicates cardinality and
reimplements Prometheus. kube-vim owns the *correlation knowledge*; Prometheus
owns *collection and joining*.

### Why info metrics (and not OTEL Resource attributes)

The correlation IDs are emitted as **datapoint attributes** on the info gauges,
not as OTEL Resource attributes. The OTEL→Prometheus exporter collapses Resource
attributes into a single `target_info` series per scrape target, which cannot be
joined per-object. Datapoint attributes become per-series labels, which can.

## Emitted metrics

Operational: gRPC RED (`rpc_server_*` from otelgrpc), `go_*` / `process_*`,
`kubevim_build_info{version, go_version}`.

Correlation (value always `1`):

| Metric | Key labels |
|---|---|
| `kubevim_compute_info` | `compute_id`, `compute_name`, `flavour_id`, `image_id`, `host_id`, `pod_name`, `operational_state`, `running_state` |
| `kubevim_vnic_info` | `compute_id`, `compute_name`, `vnic_id`, `network_id`, `subnet_id`, `network_port_id`, `type`, `host_id`, `pci_address` |
| `kubevim_network_info` | `network_id`, `network_name`, `network_type`, `provider_network`, `segmentation_id`, `operational_state` |

`compute_name` equals the KubeVirt VM/VMI name — the join key to the `name` label
on `kubevirt_vmi_*`. `compute_id` is the ETSI compute id (the VM UID). `pod_name` is
the virt-launcher pod — the join key to pod-scoped series (cAdvisor `container_*`,
kube-state-metrics `kube_pod_*`) that `kubevirt_vmi_*` does not cover.

On `kubevim_vnic_info`, two labels make a vNIC deterministically joinable to its
backend counters:

- `pci_address` — the **host** VF/pass-through PCI address of a host-PCI vNIC
  (`type=TYPE_VIRTUAL_NIC_SRIOV`); the join key to the SR-IOV VF exporter's
  `sriov_vf_*{pciAddr}` series. Empty for virtio/bridge vNICs. Sourced from the
  virt-launcher pod's `kubevirt.io/network-info` annotation.
- `host_id` — the node name (matches `kubevim_compute_info.host_id`). Keeps the
  `pci_address` join unambiguous: PCI addresses are node-local, not cluster-unique.

`pod_name`/`pci_address` are read best-effort from the virt-launcher pod on each
scrape; if the pod or annotation is absent the labels are empty and the scrape still
succeeds. The `kubevirt.io/network-info` payload type is redefined locally
(`internal/kubevim/compute/kubevirt`) rather than imported — the upstream type sits
in the `kubevirt.io/kubevirt` application module, which is not consumable as a
library (kube-vim depends only on the `kubevirt.io/api` / `client-go` staging modules).

Bridge/virtio vNICs need no extra label: KubeVirt sets the `interface` label on
`kubevirt_vmi_network_*` to the network name, which equals `vnic_id`, so the join
keys on `vnic_id` directly. (SR-IOV VF traffic bypasses virtio and never appears in
`kubevirt_vmi_network_*`.)

```promql
# SR-IOV VF counters → ETSI compute/vNIC
sriov_vf_rx_packets
  * on(pciAddr) group_left(compute_id, vnic_id)
  label_replace(kubevim_vnic_info{type="TYPE_VIRTUAL_NIC_SRIOV"},
                "pciAddr", "$1", "pci_address", "(.+)")

# bridge/virtio counters → ETSI compute/vNIC
kubevirt_vmi_network_receive_packets_total
  * on(name, interface) group_left(compute_id, vnic_id)
  label_replace(label_replace(kubevim_vnic_info{type="TYPE_VIRTUAL_NIC_BRIDGE"},
      "name", "$1", "compute_name", "(.+)"), "interface", "$1", "vnic_id", "(.+)")
```

The gauges are OTEL observable gauges whose callback reuses the domain managers'
existing `List*` methods (`compute.Manager.ListComputeResources`,
`network.Manager.ListNetworks`) — the telemetry layer holds no state of its own,
consistent with kube-vim being stateless. The List calls run per scrape, bounded
by a 10s timeout; a backend error is logged and skipped, never failing the scrape.

## Implementation

- `internal/kubevim/telemetry/` — `provider.go` (OTEL MeterProvider + OTEL
  Prometheus exporter into a dedicated `prometheus.Registry` + Go/process
  collectors), `server.go` (`/metrics` `http.Server` with `ReadHeaderTimeout` +
  graceful `Shutdown`), `info.go` (the correlation gauges), `metrics.go`
  (build-info), `manager.go` (lifecycle; inert no-op provider when disabled).
- `internal/kubevim/manager.go` — `initTelemetryManager`; the MeterProvider is
  threaded into the northbound server for otelgrpc; the metrics server runs as a
  goroutine in `Start`.
- `internal/kubevim/server/server.go` — `otelgrpc` stats handler (metrics only;
  no trace exporter, so spans are no-ops).

## Configuration

```yaml
monitoring:
  enabled: false      # opt-in; when false no port is opened and behaviour is unchanged
  metricsPort: 9095   # Prometheus /metrics endpoint
```

Chart (`dist/chart`): when `vim.config.monitoring.enabled=true` the Deployment
gets a `metrics` containerPort and the Service a `metrics` port. For the
Prometheus Operator (prometheus-community / kube-prometheus-stack), set
`vim.metrics.serviceMonitor.enabled=true` to render a `ServiceMonitor`
(off by default so the chart does not depend on the Operator CRDs). Plain
Prometheus users scrape the metrics port directly. Legacy `prometheus.io/scrape`
pod annotations are intentionally not emitted — the Operator ignores them.

## Operator runbook

1. Deploy with `--set vim.config.monitoring.enabled=true` (add
   `--set vim.metrics.serviceMonitor.enabled=true` when running the Operator).
2. `curl <vim-pod>:9095/metrics` → expect `kubevim_compute_info`,
   `kubevim_vnic_info`, `kubevim_network_info`, `rpc_server_*`, `go_*`,
   `kubevim_build_info`.
3. Confirm the backends export their own series (KubeVirt/kube-OVN/SR-IOV) and
   that `kubevim_compute_info.compute_name` matches the `name` label on
   `kubevirt_vmi_*` — that shared label is what the recording rules will join on.

## Future work (not implemented)

- **Prometheus-side ETSI correlation:** `PrometheusRule` recording rules joining
  `kubevim_*_info` × backend series into IFA 027-named series
  (`etsi:VCpuUsageMean`, `etsi:VNetByteIncoming`, …) labelled with
  `objectType`/`objectInstanceId`/`subObjectInstanceId`.
- **OTLP exporter** (config-selectable transport), tracing, zap→OTEL log bridge.
- **FM gRPC API** (IFA 005/006 §7.6) over Alertmanager.
- **PM gRPC API** (§7.7) over Prometheus range queries — `PmJob` CRD, `Threshold`
  → generated `PrometheusRule`, and a Subscribe/Notify subsystem.
- **SR-IOV VF counters:** VF traffic bypasses OVS and is invisible to KubeVirt, so
  per-vNIC byte/packet counters come from a node-level VF exporter
  (`sriov_vf_*`, keyed by `pciAddr`). `kubevim_vnic_info` now carries `pci_address`
  + `host_id` to join those series to the ETSI compute/vNIC (see above); deploying
  the exporter and the recording rules remains operator/NFVO-side work.
