package telemetry

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
)

// infoCallbackTimeout bounds the k8s List calls performed on every scrape so a
// slow API server cannot stall the /metrics endpoint indefinitely.
const infoCallbackTimeout = 10 * time.Second

// IMPORTANT TODO(tests): cover the info-metric callbacks with unit tests using
// generated mocks of compute.Manager and network.Manager (mock the List methods,
// gather the prometheus registry, assert emitted series and their label sets,
// and run with -race since the callbacks are invoked concurrently on scrape).
// Tracked as a separate testing task.

// registerInfoMetrics registers the kubevim_*_info correlation gauges. Each is
// an observable gauge whose value is always 1; the payload is the label set.
// The callback reuses the domain managers' existing List methods, so the
// telemetry layer holds no state of its own.
func registerInfoMetrics(meter metric.Meter, logger *zap.Logger, computeMgr compute.Manager, networkMgr network.Manager) error {
	computeInfo, err := meter.Int64ObservableGauge(
		"kubevim.compute.info",
		metric.WithDescription("Correlation of a kube-vim virtualised compute resource to its KubeVirt VM and placement (value always 1)."),
	)
	if err != nil {
		return fmt.Errorf("create kubevim.compute.info gauge: %w", err)
	}
	vnicInfo, err := meter.Int64ObservableGauge(
		"kubevim.vnic.info",
		metric.WithDescription("Correlation of a compute resource virtual network interface to its network/subnet (value always 1)."),
	)
	if err != nil {
		return fmt.Errorf("create kubevim.vnic.info gauge: %w", err)
	}
	networkInfo, err := meter.Int64ObservableGauge(
		"kubevim.network.info",
		metric.WithDescription("Correlation of a kube-vim virtualised network resource to its provider network/segmentation (value always 1)."),
	)
	if err != nil {
		return fmt.Errorf("create kubevim.network.info gauge: %w", err)
	}

	_, err = meter.RegisterCallback(
		func(ctx context.Context, obs metric.Observer) error {
			ctx, cancel := context.WithTimeout(ctx, infoCallbackTimeout)
			defer cancel()
			observeComputeInfo(ctx, obs, logger, computeMgr, computeInfo, vnicInfo)
			observeNetworkInfo(ctx, obs, logger, networkMgr, networkInfo)
			return nil
		},
		computeInfo, vnicInfo, networkInfo,
	)
	if err != nil {
		return fmt.Errorf("register info metrics callback: %w", err)
	}
	return nil
}

func observeComputeInfo(ctx context.Context, obs metric.Observer, logger *zap.Logger, mgr compute.Manager, computeInfo, vnicInfo metric.Int64ObservableGauge) {
	computes, err := mgr.ListComputeResources(ctx)
	if err != nil {
		logger.Warn("list compute resources for info metrics", zap.Error(err))
		return
	}
	for _, c := range computes {
		computeID := c.GetComputeId().GetValue()
		computeName := c.GetComputeName()
		obs.ObserveInt64(computeInfo, 1, metric.WithAttributes(
			attribute.String("compute_id", computeID),
			// compute_name is the KubeVirt VM/VMI name, i.e. the join key to the
			// `name` label on kubevirt_vmi_* backend series.
			attribute.String("compute_name", computeName),
			attribute.String("flavour_id", c.GetFlavourId().GetValue()),
			attribute.String("image_id", c.GetVcImageId().GetValue()),
			attribute.String("host_id", c.GetHostId().GetValue()),
			// pod_name is the virt-launcher pod, the join key to cAdvisor/kube-state-metrics.
			attribute.String("pod_name", c.GetMetadata().GetFields()[compute.ComputePodNameMetadataKey]),
			attribute.String("operational_state", c.GetOperationalState().String()),
			attribute.String("running_state", c.GetRunningState().String()),
		))
		for _, nic := range c.GetVirtualNetworkInterface() {
			obs.ObserveInt64(vnicInfo, 1, metric.WithAttributes(
				attribute.String("compute_id", computeID),
				attribute.String("compute_name", computeName),
				attribute.String("vnic_id", nic.GetResourceId().GetValue()),
				attribute.String("network_id", nic.GetNetworkId().GetValue()),
				attribute.String("subnet_id", nic.GetSubnetId().GetValue()),
				attribute.String("network_port_id", nic.GetNetworkPortId().GetValue()),
				attribute.String("type", nic.GetTypeVirtualNic().String()),
				// host_id disambiguates the pci_address join across nodes (PCI addresses
				// are node-local, not cluster-unique).
				attribute.String("host_id", c.GetHostId().GetValue()),
				// pci_address is the host VF/pass-through address for host-PCI vNICs
				// (SR-IOV); empty for virtio/bridge vNICs.
				attribute.String("pci_address", nic.GetMetadata().GetFields()[compute.VnicHostPciAddressMetadataKey]),
			))
		}
	}
}

func observeNetworkInfo(ctx context.Context, obs metric.Observer, logger *zap.Logger, mgr network.Manager, networkInfo metric.Int64ObservableGauge) {
	networks, err := mgr.ListNetworks(ctx)
	if err != nil {
		logger.Warn("list networks for info metrics", zap.Error(err))
		return
	}
	for _, n := range networks {
		obs.ObserveInt64(networkInfo, 1, metric.WithAttributes(
			attribute.String("network_id", n.GetNetworkResourceId().GetValue()),
			attribute.String("network_name", n.GetNetworkResourceName()),
			attribute.String("network_type", n.GetNetworkType().String()),
			attribute.String("provider_network", n.GetProviderNetwork()),
			attribute.String("segmentation_id", strconv.FormatUint(n.GetSegmentationId(), 10)),
			attribute.String("operational_state", n.GetOperationalState().String()),
		))
	}
}
