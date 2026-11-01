# SR-IOV Networks

## Overview

Kube-vim attaches VM interfaces to VLAN and FLAT networks as SR-IOV virtual functions (VFs). SR-IOV is not a kind of network: a VLAN or FLAT network on a provider network carries SR-IOV settings (the VF pool), and each interface chooses how it attaches (`typeVirtualNic`: `BRIDGE` or `SRIOV`). One network can carry both kinds of interfaces on the same layer 2 segment.

## Background

### What SR-IOV is

A Physical Function (PF) on a capable NIC can be split into many Virtual Functions (VFs). Each VF is a lightweight PCIe device that can be assigned directly to a pod or VM. Unlike a software bridge, a VF gives the workload a near-native path to the wire: the NIC hardware handles switching between VFs with no host-kernel involvement in the data path.

Kube-vim wires SR-IOV networks for **OVS hardware offload**: the PF runs in `switchdev` mode, each VF has a host-side *representor* netdev, and the representor is attached to an OVS bridge. OVS programs the forwarding rules and the NIC ASIC offloads them, so VF traffic is switched in hardware while OVS keeps control-plane visibility. See the sriov-network-operator [OVS hardware offload guide](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/doc/ovs-hw-offload.md) for the underlying mechanism.

SR-IOV is the standard approach for:
- **Low-latency data planes** (vRouter, vBNG, vFW), where kernel bridging overhead is unacceptable
- **DPDK applications**: VFs can be bound to `vfio-pci` and used directly from userspace
- **Line-rate throughput**: hardware offloads (checksum, segmentation, RSS) remain available to the VM

### Component roles

```
┌────────────────────────────────────────────────────────────────┐
│ kube-vim                                                       │
│  network request (sriov.pool) ──► kube-ovn network manager    │
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

A network takes SR-IOV interfaces when it has `sriov` settings:

| Field | SR-IOV meaning |
|---|---|
| `networkType` | `NETWORK_TYPE_VLAN` or `NETWORK_TYPE_FLAT`; `GENEVE` networks take no `sriov` settings |
| `providerNetwork` | The Kube-OVN ProviderNetwork whose OVS bridge the PF uplink is on (e.g. `mx5p0`) |
| `segmentationId` | VLAN ID applied to the representor's OVS port; absent (untagged) for FLAT |
| `mtu` | MTU of the ovs-cni attachment, and of the Kube-OVN subnet when the network has one |
| `sriov.pool` | Device plugin resource name of the VF pool, e.g. `openshift.io/mlx_vf_pf0_vfio`; a `vfio-pci` pool on the provider network's PF |

An interface attaches through a VF when it has `typeVirtualNic: TYPE_VIRTUAL_NIC_SRIOV`, which needs `ipAssignment: IP_ASSIGNMENT_NONE`: kube-vim assigns no addresses to SR-IOV interfaces.

Kube-vim cannot check that the pool belongs to the provider network's PF: a pool of another PF puts the VF on another uplink without an error. On a network that also has a subnet, guests on SR-IOV interfaces must use addresses Kube-OVN does not allocate (outside the subnet's range or in its excluded addresses).

Networks placed on the **same PF** are separated from one another by their access VLAN: give each co-located network a distinct `segmentationId` to isolate it. FLAT networks leave the port untagged, so several FLAT networks on the same PF land in a single shared L2 broadcast domain. Networks on different PFs are already isolated by hardware.

## Architecture

### Per-network SR-IOV attachment

The Kube-OVN network manager (`internal/kubevim/network/kubeovn/`) owns every network. For a VLAN or FLAT network it creates a Kube-OVN `Vlan` on the provider network, the Kube-OVN subnet and its kube-ovn NAD when the network has subnets, and, when the network has `sriov` settings, an ovs-cni NAD that SR-IOV interfaces attach through. Every object carries the network ID label; the network's settings are stored in an annotation on the `Vlan`. Kube-vim never creates provider networks or VF pools: operators set them up with the node, since VF pools need a host reboot.

**NAD structure**

```
NetworkAttachmentDefinition
  name: net-<network id>-sriov
  namespace: <kube-vim namespace>
  annotations:
    k8s.v1.cni.cncf.io/resourceName: <sriov.pool>
  labels:
    app.kubernetes.io/managed-by: kube-nfv
    network.kubevim.kubenfv.io/network-id: <network id>
    network.kubevim.kubenfv.io/nic-type: sriov
  spec.config: |
    {
      "cniVersion": "0.3.1",
      "type": "ovs",
      "name": "net-<network id>-sriov",
      "vlan": <segmentationId>,     // omitted for FLAT
      "mtu": <mtu>,                 // omitted when unset
      "socket_file": "<socketFile>" // from config; omitted when empty
    }
```

The OVS `bridge` is intentionally **not** set in the config. ovs-cni auto-discovers it from the VF's PF (`FindBridgeByInterface`), which is the only correct behaviour on multi-PF hosts where each PF is attached to its own bridge; a single static bridge name could not serve more than one PF. This requires the PF uplink to already be a port on its OVS bridge (see Prerequisites).

**ovs-cni configuration**

| Field | Source | Notes |
|---|---|---|
| `vlan` | `segmentationId` | Access VLAN on the OVS port; omitted for FLAT networks |
| `mtu` | `mtu` | Omitted when the network sets none |
| `socket_file` | `network.sriov.socketFile` config | OVSDB socket; defaults to `unix:/var/run/openvswitch/db.sock` |
| `bridge` | (unset) | Auto-discovered by ovs-cni from the VF's PF |

The `vfio`/`netdevice` driver mode is set by the VF pool (`SriovNetworkNodePolicy.deviceType`), not by the NAD. ovs-cni handles both: for `vfio-pci` VFs it runs in userspace mode (attaches the representor, configures the VF via PF netlink, skips IPAM); for `netdevice` VFs it additionally moves the VF netdev into the pod.

### Compute layer

When `CreateCompute` requests an SR-IOV interface, kube-vim skips subnet and IPAM resolution and emits a KubeVirt VM spec with an SR-IOV interface binding (`interface.sriov: {}`). No OVN annotations are written: the VF data path is offloaded to the NIC and is **not** attached to any OVN logical switch. The underlying OVS bridge may itself be a kube-OVN ProviderNetwork, but that registration alone does not place the VF port on the overlay; see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation).

The KubeVirt binding is unchanged by the move to ovs-cni: KubeVirt discovers the VF PCI address from the Multus `network-status` `device-info`, independent of the CNI type, and passes it through to QEMU.

If the interface has a `macAddress`, kube-vim sets it on the KubeVirt interface; KubeVirt passes it to Multus in the network selection request, and ovs-cni applies it at VF setup time. SR-IOV interfaces take no NIC `model`: the guest sees the VF itself.

### Bridge provisioning and overlay isolation

ovs-cni attaches representors but never creates bridges. The per-PF OVS bridges, each with its PF uplink attached, must be provisioned out of band by the cluster operator. Common mechanisms:

- **kube-OVN ProviderNetwork** (with hardware offload enabled): creates a bridge per PF, moves the PF uplink onto it, enables `hw-offload`, and registers an OVS bridge mapping. The natural choice when kube-OVN is already the cluster CNI.
- **sriov-network-operator `manageSoftwareBridges`**: the operator creates and manages the OVS bridges directly.
- **Manual / out-of-band**: bridges created by external tooling.

kube-vim is agnostic to which is used; it only requires that each PF's bridge exists with the uplink attached, so ovs-cni's auto-discovery can resolve it.

**Overlay isolation.** When these bridges are kube-OVN ProviderNetworks, kube-OVN *knows* them (they appear in the OVS bridge mappings), but knowing a bridge is not the same as connecting it to the overlay. A provider bridge stays isolated from the OVN overlay (`br-int`) **as long as no OVN logical switch has a localnet port on that provider**, i.e. as long as no kube-OVN `Vlan` + provider-bound `Subnet` references it.

A network with both a subnet and `sriov` settings creates such a `Subnet`, so OVN adds a localnet patch between `br-int` and the provider bridge, tagged with the network's VLAN. Bridged and SR-IOV interfaces of the network then share the segment, which is the point. What is not yet verified (KB spec N14) is the price:

- whether VF-to-uplink traffic stays hardware-offloaded while the localnet port exists;
- whether traffic of other VLANs, or of SR-IOV-only networks on the same uplink, stays out of `br-int`;
- whether high-bandwidth SR-IOV traffic (including broadcast and unknown unicast) loads `ovs-vswitchd` or `ovn-controller` enough to hurt pod networking and Kubernetes services on the node.

Until that is verified, a network without subnets (SR-IOV interfaces only) is the safe choice for line-rate data planes: it creates no localnet port, so the only attachments on the bridge are VF representors (added by ovs-cni) and the PF uplink. If the check fails, networks with `sriov` settings will stop accepting subnets.

## Prerequisites

### Cluster-level components

| Component | Notes |
|---|---|
| sriov-network-operator | Manages VF pools and the SR-IOV device plugin; PFs configured in `switchdev` (`eSwitchMode: switchdev`) |
| Multus CNI | Multi-NIC support; usually bundled with the SR-IOV operator |
| ovs-cni | Installed on each worker node (`ovs` binary in the CNI bin dir). Attaches the VF representor to OVS |
| Open vSwitch | One OVS bridge **per PF**, with the PF uplink already attached as a port (so ovs-cni's auto-discovery resolves it) |
| KubeVirt | SR-IOV binding available: GA in recent releases (e.g. v1.8.4, no gate); older releases need the `SRIOV` feature gate |

> **Note:** the data path is OVS hardware offload: `switchdev` PFs + per-PF OVS bridges with uplinks attached are mandatory. ovs-cni does **not** create bridges; it only attaches representors to existing ones. Provisioning the bridges/switchdev is the cluster operator's responsibility; see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation) for the supported mechanisms and the isolation boundary. A future `NodeNetworkProfile` (Phase 2) will let kube-vim own this declaratively.

### KubeVirt SR-IOV feature gate (older KubeVirt only)

Recent KubeVirt releases (e.g. v1.8.4) ship SR-IOV as GA and need no gate. On older releases:

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

There is deliberately no bridge setting: the bridge is auto-discovered per VF from its PF (see [NAD structure](#per-network-sr-iov-attachment)).

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

VMs need a `vfio-pci` pool (`deviceType: vfio-pci`): KubeVirt passes the VF through to the guest. The full resource name to put in a network's `sriov.pool` is `<prefix>/<resourceName>`. The prefix is determined by the SR-IOV device plugin configuration: the default with sriov-network-operator is `openshift.io`, giving `openshift.io/sriov_netdevice`. Some deployments override this prefix, so always verify the actual resource names advertised on your nodes:

```
kubectl get node <node> -o json \
  | jq '.status.allocatable | with_entries(select(.key | contains("sriov")))'
```

## Operator runbook

### Create a network for SR-IOV interfaces

```bash
curl -X POST http://kube-vim-gateway/network/v1/networks \
  -H 'Content-Type: application/json' \
  -d '{
    "network": {
      "networkName": "data-net-1",
      "networkType": "NETWORK_TYPE_VLAN",
      "providerNetwork": "mx5p0",
      "segmentationId": 100,
      "sriov": {"pool": "openshift.io/mlx_vf_pf0_vfio"}
    }
  }'
```

Verify the NAD was created:

```
kubectl get net-attach-def -n kube-vim -l network.kubevim.kubenfv.io/network-id=<network id> -o yaml
```

Expect the `-sriov` NAD's `spec.config` to contain `"type":"ovs"` and `"vlan":100`, and annotation `k8s.v1.cni.cncf.io/resourceName: openshift.io/mlx_vf_pf0_vfio`.

### Create a VM with an SR-IOV interface

```json
{
  "compute": { ... },
  "networkInterfaces": [
    {
      "networkId": "<network id>",
      "typeVirtualNic": "TYPE_VIRTUAL_NIC_SRIOV",
      "ipAssignment": "IP_ASSIGNMENT_NONE"
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
ovs-appctl dpctl/dump-flows -m                      # look for offloaded:yes dp:tc; without -m the offloaded field is not printed
```

### Troubleshooting

| Symptom | Likely cause | Check |
|---|---|---|
| VM pod stuck in `Pending` | No VFs available on the node | `kubectl describe pod <virt-launcher>` → `Insufficient <resourceName>` |
| VM starts but VF not visible inside guest | Older KubeVirt without the SRIOV feature gate | Inspect `KubeVirt` CR `featureGates` and the KubeVirt version |
| VF up but no traffic / representor not on bridge | ovs-cni couldn't reach OVSDB, or the PF isn't a bridge port (auto-discovery failed) | `ovs-vsctl list-ports <bridge>`; check `socketFile`; verify the PF uplink is attached to its bridge |
| Traffic works but not offloaded | PF not in switchdev, or `hw-offload` disabled on OVS | `ovs-vsctl get Open_vSwitch . other_config:hw-offload`; check PF `eSwitchMode` |
| VF visible but incorrect VLAN tagging | `segmentationId` mismatch on the OVS port | Delete and recreate the network |
| VF on the wrong uplink | `sriov.pool` is a pool of another PF than the provider network's | Recreate the network with the pool of the right PF |
| Not-found error on network query | NAD missing `managed-by: kube-nfv` label | Label may have been removed out of band |

## Limitations

- **No admin API for VF pools or bridges.** `SriovNetworkNodePolicy` CRs, switchdev config, and the per-PF OVS bridges must be created out of band. See Phase 2 below.
- **OVS bridges must pre-exist.** ovs-cni attaches representors but never creates bridges; auto-discovery also requires the PF uplink to already be a bridge port.
- **No addresses for SR-IOV interfaces.** Kube-vim assigns none (`IP_ASSIGNMENT_NONE`); IPAM is handled inside the VM or by an external DHCP server on the VLAN.
- **No rate limiting.** ovs-cni has no `min_tx_rate`/`max_tx_rate`. Use OVS QoS out of band if required.
- **One VF per network per VM.** Multiple SR-IOV interfaces on the same VM require separate networks.
- **No pool check.** Kube-vim does not verify that `sriov.pool` belongs to the provider network's PF.
- **Bridged and SR-IOV interfaces on one network are unverified** (see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation)).

## Phase 2: NodeNetworkProfile admin API

A `NodeNetworkProfile` CR will allow cluster operators to express the full node NIC topology declaratively through kube-vim, without writing sriov-network-operator CRs by hand:

- **Ports**: physical NIC ports and their roles.
- **Bonds / LAG**: hardware or kernel bonding; members, mode, LACP. Reconciled via NMState-operator.
- **VF pool partitioning**: per-PF named pools with non-overlapping VF ranges by device type (`netdevice` / `vfio-pci`). Reconciled into `SriovNetworkNodePolicy` objects.
- **kube-OVN ProviderNetwork wiring**: declaratively bind a kube-OVN ProviderNetwork uplink to a specific host interface, bond, or VF netdevice (automating what operators wire by hand today; see [Bridge provisioning and overlay isolation](#bridge-provisioning-and-overlay-isolation)).
- **OVS-DPDK / vhost-user**: bootstrap userspace OVS + DPDK, hugepages, and vhost-user socket paths for line-rate NFV data planes.

The current tenant path is forward-compatible: it consumes any resource name the device plugin exposes, regardless of whether the VF pool was provisioned by hand, by raw `SriovNetworkNodePolicy`, or by a future `NodeNetworkProfile`.
