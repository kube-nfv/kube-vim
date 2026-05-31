# Managed Management Network

Status: accepted (2026-05-31)
Owner: kube-vim
Scope: kube-vim core, kube-OVN integration

## TL;DR

Kube-vim can optionally own a single shared "management network" — a kube-OVN
`Vpc` + `Subnet` + `NetworkAttachmentDefinition` provisioned and reconciled at
kube-vim startup. NFVOs (e.g. OSM) point at this network through their own VIM
account configuration; from that point on, every VNF/NS reuses the same
management fabric instead of triggering per-NS VPC creation.

The feature is disabled by default and opt-in via the kube-vim configuration
file.

## Problem

OSM declares every NS's management VL with `mgmt-network: true`. The RO kube-vim
connector translates this into an OVERLAY network request, so kube-vim creates
a fresh `Vpc` + `Subnet` + `NetworkAttachmentDefinition` per NS.

Two consequences are operationally fatal:

1. **`inject_ssh_key` cannot reach the VM.** OSM RO uses `paramiko` from the RO
   pod to SSH into the VM and append the EE pod's public key to
   `authorized_keys`. The VM's management IP lives in the per-NS VPC subnet
   (`192.168.x.y`); the RO pod lives on `ovn-default` (`10.16.0.0/16`). The
   two are isolated VPCs and routing between them is not configured. RO
   retries `max_retries_inject_ssh_key` (20) times, gives up, marks the VDU
   task FAILED, and the NS goes BROKEN at "Stage 2/5: deployment of KDUs, VMs
   and execution environments".

2. **EE → VM SSH cannot reach the VM either.** The same network isolation
   bites the helm-EE pod, which has to SSH into the VM to run day-1/day-2
   primitives. A working workaround is to add a Multus attachment plus an
   explicit `logical_switch` annotation to each EE chart, but it hardcodes a
   specific NAD name and namespace into the chart and fails as soon as more
   than one NS is active concurrently.

Per-NS VPCs also leave heavy debris in the cluster:

* one `Vpc`, `Subnet`, and `NetworkAttachmentDefinition` per NS
* one project namespace per NS, with stale Multus IPAM allocations on cleanup
* manual finalizer patching is sometimes required to clean up after a failed
  instantiation

## Why not alternatives

### Per-NS VPC + RO Multus per NS

Attach the RO pod via Multus to every per-NS VPC at instantiation time.
Rejected because:

* RO Deployment annotations are static; dynamic re-attachment per NS is not
  a supported pattern.
* Even if it worked, it scales linearly with the number of concurrent NSes
  and creates a moving target for the OSM helm chart.

### Reuse `ovn-default`

Every VM has a KubeVirt-managed `PodNetwork` attachment with Masquerade
binding. The wrapping virt-launcher pod gets a real `ovn-default` IP and
KubeVirt NATs inbound SSH/22 to the guest. RO is already on `ovn-default`, so
reachability is solved for free.

The blockers are inside kube-vim, not in the network model:

* `nfvNetworkSubnetFromKubeovnSubnet` (`network/kubeovn/utils.go:244`)
  rejects any object that does not carry the
  `app.kubernetes.io/managed-by: kube-nfv` label. `ovn-default` is owned by
  kube-OVN, not kube-nfv.
* `ovn-cluster` (the default VPC) contains multiple subnets — `join`,
  `ovn-default`, plus any user-created ones. Picking "the management subnet"
  out of that set is ambiguous.

Relaxing the managed-by guard or mutating cluster-system resources to add
labels are both undesirable.

### VPC peering

kube-OVN supports peering between two VPCs at a time. Doable for one shared
management VPC paired with `ovn-cluster`, but the model is brittle to
changes in kube-OVN's peering APIs and adds a moving part that kube-vim
would have to drive. Captured as a possible alternative for environments
where Multus on RO is not acceptable.

## Solution

Kube-vim provisions and owns a single management `Vpc` / `Subnet` /
`NetworkAttachmentDefinition` trio at startup, with all the labels its own
internal lookups already expect. The NFVO (OSM in our case) is told the name
of that network through its existing VIM-account configuration; from then on
every NS's mgmt VL maps to this shared fabric instead of creating fresh
per-NS resources.

```
   ┌──────────────┐  Multus  ┌────────────────────┐
   │   RO pod     │──────────│ osm-mgmt-subnet-0  │
   └──────────────┘          │                    │
                             │  Vpc/osm-mgmt      │
   ┌──────────────┐  Multus  │  Subnet (/24)      │
   │  EE pods     │──────────│  NAD               │
   └──────────────┘          │                    │
                             │                    │
   ┌──────────────┐  Multus  │                    │
   │  VM mgmt cp  │──────────│                    │
   └──────────────┘          └────────────────────┘
```

### Architecture

* Kube-vim creates the trio once at startup if it does not exist. If it
  exists, kube-vim verifies labels and patches any missing ones.
* All labels follow kube-vim's existing conventions
  (`K8sManagedByLabel`, `K8sSubnetNameLabel`, `K8sSubnetNetAttachNameLabel`,
  `K8sNetworkNameLabel`, `K8sNetworkTypeLabel`) so the rest of the kube-vim
  code path treats the management network identically to any other kube-vim
  managed network.
* OSM RO discovers the network via its existing `management_network_name`
  VIM-config knob. The kubevim RO connector's `get_network_list` already
  supports filtering by name; no Python code changes are required.
* On NS termination, `ns_thread._delete_task` short-circuits to `SUPERSEDED`
  when `vim_info.created == False`, so terminating an NS does **not**
  delete the shared management network.

### Configuration

Added under the `network` section of the kube-vim configuration file.

```yaml
network:
  managementNetwork:
    enabled: false                  # default: off; opt-in
    name: osm-mgmt                  # kube-OVN Vpc name
    cidr: 10.240.0.0/24
    gateway: ""                     # optional; default = first IP of cidr
    excludeIps: []                  # optional
    netAttachDefName: ""            # default: {name}-subnet-0-netattach
    netAttachDefNamespace: ""       # default: K8sConfig.namespace
```

### Reconciliation contract

* Startup-only ensure. If kube-vim cannot create or patch the resources, it
  refuses to start. An operator who notices the failure restarts kube-vim
  after fixing the underlying cause.
* Idempotent. Restarting kube-vim with the same configuration is a no-op
  beyond a label-equality check.
* If a resource is deleted at runtime (manual `kubectl delete`, GitOps
  reconciler, etc.), kube-vim does not recreate it until the next restart.
  See *Future work* for the watch-based replacement.

### Required labels and why

`initNetwork` (`internal/kubevim/compute/kubevirt/manager.go:746-768`) reads
the following labels off the `Subnet` object on every VM creation:

| Label                              | Purpose                                                       |
|------------------------------------|---------------------------------------------------------------|
| `app.kubernetes.io/managed-by`     | Ownership guard; kube-vim refuses unmanaged objects.          |
| `network.kubevim.kubenfv.io/subnet-net-attach-name` | Tells kube-vim which `NetworkAttachmentDefinition` corresponds to this subnet. There is no naming convention fallback. |
| `network.kubevim.kubenfv.io/subnet-name` | Used as the `logical_switch` annotation value on the VM pod. Without it, kube-OVN falls back to `ovn-default` and the VM lands on the wrong subnet. |

The `K8sNetworkNameLabel`, `K8sNetworkIdLabel`, and `K8sNetworkTypeLabel`
labels are written too but are informational — `nfvNetworkSubnetFromKubeovnSubnet`
tolerates their absence.

## Operator runbook

1. **Enable the feature in the kube-vim configuration file** (`/etc/kube-vim/config.yaml`):

   ```yaml
   network:
     managementNetwork:
       enabled: true
       name: osm-mgmt
       cidr: 10.240.0.0/24
   ```

   Restart kube-vim. Verify:

   ```
   kubectl get vpc osm-mgmt
   kubectl get subnet osm-mgmt-subnet-0
   kubectl get net-attach-def osm-mgmt-subnet-0-netattach -n kube-nfv
   ```

   All three should be `app.kubernetes.io/managed-by=kube-nfv`.

2. **Attach the OSM RO Deployment to the management network** via two pod
   annotations:

   ```yaml
   k8s.v1.cni.cncf.io/networks: kube-nfv/osm-mgmt-subnet-0-netattach
   osm-mgmt-subnet-0-netattach.kube-nfv.ovn.kubernetes.io/logical_switch: osm-mgmt-subnet-0
   ```

   Both are required. Without the second annotation, kube-OVN falls back to
   `ovn-default` and the RO pod gets a useless second IP from the wrong
   subnet.

3. **Register the management network with OSM** by updating the VIM account:

   ```
   osm vim-update <vim-name> --config '{management_network_name: osm-mgmt}'
   ```

4. **Verify end-to-end** by instantiating any NS that has a VL with
   `mgmt-network: true`. The NS should reach "Stage 4/5: running Day-1
   primitives for VNF" without the `Reaching max tries injecting key`
   failure.

## Failure modes

| Failure                                                   | Operator action                                         |
|-----------------------------------------------------------|---------------------------------------------------------|
| kube-vim refuses to start with mgmt network errors        | Inspect logs; usually a CIDR conflict or RBAC issue.    |
| OSM still creates a new VPC per NS                        | Confirm `management_network_name` is set in VIM config. |
| RO inject_ssh_key still times out                         | Confirm both pod annotations on the RO Deployment.      |
| The shared management network is deleted out of band      | Restart kube-vim to recreate it.                        |

## Future work

* **Controller-runtime reconciler.** Replace the startup-only ensure with
  a controller that watches the trio and recreates it on drift. Brings
  proper leader election, exponential backoff, and predictable HA behaviour.
  Deferred until a second controller-style use case in kube-vim makes the
  platform investment worth it.
* **VPC peering as an alternative to RO Multus.** For environments where
  adding Multus annotations to RO is not acceptable, kube-vim could peer
  the management VPC with `ovn-cluster`. Adds a moving part inside kube-vim
  and is brittle to kube-OVN model changes, but removes the OSM-side wiring.
* **Multi-tenant isolation via NetworkPolicy.** If multiple NFVOs share a
  cluster, NetworkPolicy applied to the management subnet can restrict
  east-west traffic per tenant.
