# SR-IOV Networks

## Overview

Kube-vim supports SR-IOV (Single Root I/O Virtualization) as a network type alongside OVERLAY and UNDERLAY. SR-IOV networks are allocated through the standard VI-VNFM `AllocateVirtualisedNetworkResource` RPC and can then be attached to a VM via `AllocateVirtualisedComputeResource`.

## Background

### What SR-IOV is

A Physical Function (PF) on a capable NIC can be split into many Virtual Functions (VFs). Each VF is a lightweight PCIe device that can be assigned directly to a pod or VM. Unlike a software bridge, a VF gives the workload a near-native path to the wire — the NIC hardware handles switching between VFs with no host-kernel involvement in the data path.

Kube-vim wires SR-IOV networks for **OVS hardware offload**: the PF runs in `switchdev` mode, each VF has a host-side *representor* netdev, and the representor is attached to an OVS bridge. OVS programs the forwarding rules and the NIC ASIC offloads them, so VF traffic is switched in hardware while OVS keeps control-plane visibility. See the sriov-network-operator [OVS hardware offload guide](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/doc/ovs-hw-offload.md) for the underlying mechanism.

SR-IOV is the standard approach for:
- **Low-latency data planes** — vRouter, vBNG, vFW — where kernel bridging overhead is unacceptable
- **DPDK applications** — VFs can be bound to `vfio-pci` and used directly from userspace
- **Line-rate throughput** — hardware offloads (checksum, segmentation, RSS) remain available to the VM

### Component roles

```
┌────────────────────────────────────────────────────────────────┐
│ kube-vim                                                       │
│  network request ──► composite manager ──► sriov backend      │
│                                               │                │
│                                               ▼                │
│                                         NetworkAttachmentDefinition
│                                         (type: ovs, resourceName)
│                                                                │
│  compute request ──► kubevirt manager                          │
│                         │                                      │
│                         ▼                                      │
│                    KubeVirt VM spec                            │
│                    interface.sriov: {}                         │
└──────────────────────────┬─────────────────────────────────────┘
                           │ at pod start
                    ┌──────┴─────────────────────┐
                    │ Multus CNI                 │
                    │  reads NAD resourceName    │
                    │  requests VF from device   │
                    │  plugin pool               │
                    └──────┬─────────────────────┘
                           │ PCI address
                    ┌──────┴─────────────────────┐
                    │ ovs-cni                    │
                    │  resolves VF representor   │
                    │  auto-discovers OVS bridge │
                    │  from the VF's PF, attaches│
                    │  representor (VLAN on port)│
                    └──────┬─────────────────────┘
                           │
                    ┌──────┴─────────────────────┐
                    │ VF visible inside VM       │
                    │ (PCI passthrough via       │
                    │  KubeVirt SRIOV binding);  │
                    │  traffic HW-offloaded via  │
                    │  representor ⇄ OVS ⇄ uplink │
                    └────────────────────────────┘
```

**sriov-network-operator** is responsible for the admin layer: creating `SriovNetworkNodePolicy` CRs that configure PFs, set the number of VFs, and register them as Kubernetes extended resources. Kube-vim consumes the resource pools the operator exposes; it does not drive the operator directly. See [Phase 2 (NodeNetworkProfile)](#phase-2-node-network-profile-admin-api) for the planned admin API.

## API

SR-IOV networks use the same `VirtualNetworkData` message as OVERLAY and UNDERLAY networks. The relevant fields are:

| Field | SR-IOV meaning |
|---|---|
| `networkType` | `SRIOV` |
| `providerNetwork` | Device plugin resource name (e.g. `openshift.io/sriov_netdevice`) |
| `segmentationId` | VLAN ID applied to the OVS port — `0` means untagged |
| `bandwidth` | Ignored. ovs-cni has no per-port rate limiting; use OVS QoS if needed |

When attaching an SR-IOV network to a VM, set `typeVirtualNic` to `TYPE_VIRTUAL_NIC_SRIOV` in the `VirtualNetworkInterfaceData`.

SR-IOV networks do not support subnets. All subnet operations on an SR-IOV network return an error.

Networks placed on the **same PF** are separated from one another by their access VLAN: give each co-located SR-IOV network a distinct `segmentationId` to isolate it. `segmentationId: 0` leaves the port untagged, so multiple untagged networks on the same PF land in a single shared L2 broadcast domain. Networks on different PFs are already isolated by hardware.

## Architecture

### Composite network manager

Kube-vim routes network operations to the correct backend through a composite manager (`internal/kubevim/network/composite/`). It holds an OVN backend and an SR-IOV backend:

- `CreateNetwork` — dispatches on `networkData.NetworkType`: `SRIOV` → sriov backend, else → OVN backend.
- `GetNetwork` / `DeleteNetwork` — tries OVN first, falls through on not-found, then tries SR-IOV.
- `ListNetworks` — returns the union of both backends.
- Subnet operations — resolves the parent network's type; SR-IOV → unsupported, OVN → OVN backend.
- `EnsureManagementNetwork` — delegates to OVN only (SR-IOV has no management-network concept).

### SR-IOV network backend

`internal/kubevim/network/sriov/` implements `network.Manager` against Multus `NetworkAttachmentDefinition` objects. The NAD is the sole source of truth — kube-vim creates it on `AllocateVirtualisedNetworkResource` and parses it back on `QueryVirtualisedNetworkResource`. No separate CRD or database entry is required.

**NAD structure**

```
NetworkAttachmentDefinition
  name: <network name>
  namespace: <kube-vim namespace>
  annotations:
    k8s.v1.cni.cncf.io/resourceName: <providerNetwork>
  labels:
    app.kubernetes.io/managed-by: kube-nfv
    network.kubevim.kubenfv.io/netowrk-type: SRIOV
    network.kubevim.kubenfv.io/network-name: <name>
  spec.config: |
    {
      "cniVersion": "0.3.1",
      "type": "ovs",
      "name": "<name>",
      "vlan": <segmentationId>,     // omitted when 0
      "socket_file": "<socketFile>" // from config; omitted when empty
    }
```

The OVS `bridge` is intentionally **not** set in the config. ovs-cni auto-discovers it from the VF's PF (`FindBridgeByInterface`), which is the only correct behaviour on multi-PF hosts where each PF is attached to its own bridge — a single static bridge name could not serve more than one PF. This requires the PF uplink to already be a port on its OVS bridge (see Prerequisites).

**ovs-cni configuration**

| Field | Source | Notes |
|---|---|---|
| `vlan` | `segmentationId` | Access VLAN on the OVS port; omitted when `0` |
| `socket_file` | `network.sriov.socketFile` config | OVSDB socket; defaults to `unix:/var/run/openvswitch/db.sock` |
| `bridge` | (unset) | Auto-discovered by ovs-cni from the VF's PF |

The `vfio`/`netdevice` driver mode is set by the VF pool (`SriovNetworkNodePolicy.deviceType`), not by the NAD. ovs-cni handles both: for `vfio-pci` VFs it runs in userspace mode (attaches the representor, configures the VF via PF netlink, skips IPAM); for `netdevice` VFs it additionally moves the VF netdev into the pod.

### Compute layer

When `AllocateVirtualisedComputeResource` references an SR-IOV network, kube-vim skips subnet and IPAM resolution and emits a KubeVirt VM spec with an SR-IOV interface binding (`interface.sriov: {}`). No OVN annotations are written: the VF data path is offloaded to the NIC and is **not** attached to any OVN logical switch. The underlying OVS bridge may itself be a kube-OVN ProviderNetwork, but that registration alone does not place the VF port on the overlay — see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation).

The KubeVirt binding is unchanged by the move to ovs-cni: KubeVirt discovers the VF PCI address from the Multus `network-status` `device-info`, independent of the CNI type, and passes it through to QEMU.

If a MAC address is provided in the IPAM entry, kube-vim writes a `k8s.v1.cni.cncf.io/networks` runtime-config annotation on the virt-launcher pod so Multus passes it to ovs-cni at VF setup time.

### Bridge provisioning and overlay isolation

ovs-cni attaches representors but never creates bridges. The per-PF OVS bridges — each with its PF uplink attached — must be provisioned out of band by the cluster operator. Common mechanisms:

- **kube-OVN ProviderNetwork** (with hardware offload enabled) — creates a bridge per PF, moves the PF uplink onto it, enables `hw-offload`, and registers an OVS bridge mapping. The natural choice when kube-OVN is already the cluster CNI.
- **sriov-network-operator `manageSoftwareBridges`** — the operator creates and manages the OVS bridges directly.
- **Manual / out-of-band** — bridges created by external tooling.

kube-vim is agnostic to which is used; it only requires that each PF's bridge exists with the uplink attached, so ovs-cni's auto-discovery can resolve it.

**Overlay isolation.** When these bridges are kube-OVN ProviderNetworks, kube-OVN *knows* them (they appear in the OVS bridge mappings) — but knowing a bridge is not the same as connecting it to the overlay. An SR-IOV provider bridge stays isolated from the OVN overlay (`br-int`) **as long as no OVN logical switch has a localnet port on that provider** — i.e. as long as no kube-OVN `Vlan` + provider-bound `Subnet` references it.

Creating such a `Subnet` makes OVN add a localnet patch between `br-int` and the provider bridge. That would:

- merge the SR-IOV L2 domain into the OVN overlay, and
- push the OVN pipeline (ACLs, conntrack, NAT) onto the offloaded bridge — much of which does not hardware-offload and silently falls back to the software data path, defeating the point of switchdev.

So the isolation guarantee is: on a provider network used for SR-IOV, the only intended attachments are VF representors (added by ovs-cni) and the PF uplink. **Do not bind kube-OVN `Vlan`/`Subnet` objects to it.** This is enforced by convention, not by a hard barrier — worth guarding in CI/policy on clusters that also use kube-OVN VLAN provider subnets for other purposes.

## Prerequisites

### Cluster-level components

| Component | Notes |
|---|---|
| sriov-network-operator | Manages VF pools and the SR-IOV device plugin; PFs configured in `switchdev` (`eSwitchMode: switchdev`) |
| Multus CNI | Multi-NIC support; usually bundled with the SR-IOV operator |
| ovs-cni | Installed on each worker node (`ovs` binary in the CNI bin dir). Attaches the VF representor to OVS |
| Open vSwitch | One OVS bridge **per PF**, with the PF uplink already attached as a port (so ovs-cni's auto-discovery resolves it) |
| KubeVirt | SR-IOV feature gate must be enabled |

> **Note:** the data path is OVS hardware offload — `switchdev` PFs + per-PF OVS bridges with uplinks attached are mandatory. ovs-cni does **not** create bridges; it only attaches representors to existing ones. Provisioning the bridges/switchdev is the cluster operator's responsibility — see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation) for the supported mechanisms and the isolation boundary. A future `NodeNetworkProfile` (Phase 2) will let kube-vim own this declaratively.

### KubeVirt SR-IOV feature gate

```yaml
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
spec:
  configuration:
    developerConfiguration:
      featureGates:
        - SRIOV
```

Without this gate, KubeVirt ignores the `interface.sriov` field and the VM starts without the VF attached.

### kube-vim configuration

SR-IOV networks are rendered as ovs-cni NADs. The only tunable is the OVSDB socket ovs-cni connects to:

```yaml
network:
  sriov:
    # OVSDB socket ovs-cni connects to. Default below.
    # On kube-OVN/Talos the socket lives at /run/openvswitch/db.sock,
    # reachable via the /var/run -> /run symlink, or set explicitly.
    socketFile: "unix:/var/run/openvswitch/db.sock"
```

There is deliberately no bridge setting — the bridge is auto-discovered per VF from its PF (see [NAD structure](#sr-iov-network-backend)).

### VF pool provisioning

Before allocating SR-IOV networks through kube-vim, the cluster operator must provision VF pools via `SriovNetworkNodePolicy`. Example policy for an Intel NIC:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: intel-sriov-netdevice
  namespace: sriov-network-operator
spec:
  resourceName: sriov_netdevice
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 8
  nicSelector:
    vendor: "8086"
    pfNames: ["ens2f0"]
  deviceType: netdevice                # use vfio-pci for DPDK
```

The full resource name consumed by kube-vim is `<prefix>/<resourceName>`. The prefix is determined by the SR-IOV device plugin configuration — the default with sriov-network-operator is `openshift.io`, giving `openshift.io/sriov_netdevice`. Some deployments override this prefix, so always verify the actual resource names advertised on your nodes:

```
kubectl get node <node> -o json \
  | jq '.status.allocatable | with_entries(select(.key | contains("sriov")))'
```

## Operator runbook

### Allocate an SR-IOV network

```bash
curl -X POST http://kube-vim-gateway/vivnfm/v5/networks \
  -H 'Content-Type: application/json' \
  -d '{
    "networkResourceName": "data-net-1",
    "networkType": "SRIOV",
    "providerNetwork": "openshift.io/sriov_netdevice",
    "segmentationId": 100
  }'
```

Verify the NAD was created:

```
kubectl get net-attach-def data-net-1 -n kube-vim -o yaml
```

Expect `spec.config` to contain `"type":"ovs"` and annotation `k8s.v1.cni.cncf.io/resourceName: openshift.io/sriov_netdevice`.

### Allocate a VM with an SR-IOV interface

```json
{
  "computeData": { ... },
  "virtualNetworkInterface": [
    {
      "networkId": "<network-resource-id>",
      "typeVirtualNic": "TYPE_VIRTUAL_NIC_SRIOV"
    }
  ]
}
```

Verify the VM spec has an SR-IOV binding:

```
kubectl get vm <vm-name> -n kube-vim \
  -o jsonpath='{.spec.template.spec.domain.devices.interfaces}' | jq
```

Expect an interface with `"sriov": {}` and no `"bridge"` key.

### Verify inside the VM

```
ip link show       # VF appears as an additional NIC
ethtool -i eth1    # driver: the switchdev VF driver, e.g. mlx5_core
```

### Verify the representor is attached (host)

On the worker running the VM, the VF representor should be a port on its PF's OVS bridge, and the data path should be offloaded:

```
ovs-vsctl list-ports <bridge>                       # expect <pf>_<vfN> alongside the uplink
ovs-appctl dpctl/dump-flows type=offloaded          # forwarding rules in hardware
```

### Troubleshooting

| Symptom | Likely cause | Check |
|---|---|---|
| VM pod stuck in `Pending` | No VFs available on the node | `kubectl describe pod <virt-launcher>` → `Insufficient <resourceName>` |
| VM starts but VF not visible inside guest | KubeVirt SRIOV feature gate disabled | Inspect `KubeVirt` CR `featureGates` |
| VF up but no traffic / representor not on bridge | ovs-cni couldn't reach OVSDB, or the PF isn't a bridge port (auto-discovery failed) | `ovs-vsctl list-ports <bridge>`; check `socketFile`; verify the PF uplink is attached to its bridge |
| Traffic works but not offloaded | PF not in switchdev, or `hw-offload` disabled on OVS | `ovs-vsctl get Open_vSwitch . other_config:hw-offload`; check PF `eSwitchMode` |
| VF visible but incorrect VLAN tagging | `segmentationId` mismatch on the OVS port | Delete and recreate the SR-IOV network |
| Not-found error on network query | NAD missing `managed-by: kube-nfv` label | Label may have been removed out of band |

## Limitations

- **No admin API for VF pools or bridges.** `SriovNetworkNodePolicy` CRs, switchdev config, and the per-PF OVS bridges must be created out of band. See Phase 2 below.
- **OVS bridges must pre-exist.** ovs-cni attaches representors but never creates bridges; auto-discovery also requires the PF uplink to already be a bridge port.
- **No subnet support.** SR-IOV networks are L2 only; IPAM must be handled inside the VM or by an external DHCP server on the VLAN.
- **No rate limiting.** ovs-cni has no `min_tx_rate`/`max_tx_rate`; `bandwidth` is ignored. Use OVS QoS out of band if required.
- **One VF per network per VM.** Multiple SR-IOV interfaces on the same VM require separate network resources with distinct names.

## Phase 2: NodeNetworkProfile admin API

A `NodeNetworkProfile` CR will allow cluster operators to express the full node NIC topology declaratively through kube-vim, without writing sriov-network-operator CRs by hand:

- **Ports** — physical NIC ports and their roles.
- **Bonds / LAG** — hardware or kernel bonding; members, mode, LACP. Reconciled via NMState-operator.
- **VF pool partitioning** — per-PF named pools with non-overlapping VF ranges by device type (`netdevice` / `vfio-pci`). Reconciled into `SriovNetworkNodePolicy` objects.
- **kube-OVN ProviderNetwork wiring** — declaratively bind a kube-OVN ProviderNetwork uplink to a specific host interface, bond, or VF netdevice (automating what operators wire by hand today; see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation)).
- **OVS-DPDK / vhost-user** — bootstrap userspace OVS + DPDK, hugepages, and vhost-user socket paths for line-rate NFV data planes.

The current tenant path is forward-compatible: it consumes any resource name the device plugin exposes, regardless of whether the VF pool was provisioned by hand, by raw `SriovNetworkNodePolicy`, or by a future `NodeNetworkProfile`.
