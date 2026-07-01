# kube-vim

**A Kubernetes-native, ETSI NFV-MANO compliant Virtual Infrastructure Manager (VIM).**

kube-vim lets ETSI NFV-MANO orchestrators (such as [OSM](https://osm.etsi.org/)) manage
[VNF](https://www.etsi.org/technologies/nfv) workloads on Kubernetes through the standard
**Or-Vi** and **Vi-Vnfm** reference points — without coupling to any specific hardware
vendor, NIC vendor, or Kubernetes distribution.

## Why kube-vim

Existing VIMs either are not ETSI reference-point compatible (OpenVIM) or drag in a full
OpenStack control plane (Tacker). kube-vim is a thin, **stateless** translation layer: it
maps ETSI MANO requests onto Kubernetes Custom Resources provided by best-in-class
third-party projects, and stores no state of its own — everything lives in Kubernetes
objects.

- **Vendor neutral** — no dependency on specific hardware, NIC vendors, or K8s distros.
- **Stateless** — all resource state is held in Kubernetes CRs; ETSI metadata with no CRD
  equivalent is carried in labels and annotations.
- **Standard reference points** — gRPC + Protobuf with REST/OpenAPI bindings, conformant
  to ETSI GS NFV-IFA 005, IFA 006, and SOL 013.

## Architecture

kube-vim ships as two independent Kubernetes Deployments:

| Component | Source | Role |
|---|---|---|
| **vim** | `cmd/kube-vim` | Main gRPC server. Exposes the ETSI Or-Vi / Vi-Vnfm reference points plus an admin API. Translates requests into Kubernetes CRs. |
| **gateway** | `cmd/kube-vim-gateway` | gRPC-Gateway reverse proxy. Exposes REST/JSON bindings (OpenAPI 3.0) generated from the same gRPC schema and forwards to the vim service. |

```
  NFVO / VNFM (e.g. OSM)
        │  REST (SOL 013)            │  gRPC (IFA 005 / 006)
        ▼                            ▼
   ┌─────────┐   gRPC    ┌────────────────────────────┐
   │ gateway │──────────▶│            vim             │
   └─────────┘           │  compute · network ·       │
                         │  flavour · image · admin   │
                         └─────────────┬──────────────┘
                                       │ Kubernetes API
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                ▼              ▼               ▼               ▼
    KubeVirt           CDI         Kube-OVN          Multus      (labels carry
   (VM compute)   (disk images)  (networking)   (multi-NIC)    ETSI metadata)
```

### Bundled dependencies

kube-vim is distributed together with the third-party projects it drives, to guarantee a
compatible set of CRDs:

| Project | Purpose |
|---|---|
| [KubeVirt](https://github.com/kubevirt/kubevirt) | VM-based compute (`VirtualMachine` / `VirtualMachineInstance`) |
| [CDI](https://github.com/kubevirt/containerized-data-importer) | VM disk image provisioning into PVCs |
| [Kube-OVN](https://github.com/kubeovn/kube-ovn) | Network virtualization (overlay / underlay, VLANs, SR-IOV, DPDK) |
| [Multus CNI](https://github.com/k8snetworkplumbingwg/multus-cni) | Multiple network interfaces per workload |

## Features

- **Compute** — allocate, query, and terminate VM-based VNFs via KubeVirt; flavours mapped
  to KubeVirt instancetypes/preferences.
- **Images** — provision VM boot disks from images via CDI DataVolumes.
- **Networking** — overlay and underlay (L2/VLAN) networks and subnets via Kube-OVN, with
  static or dynamic IPAM.
- **SR-IOV** — line-rate data planes with OVS hardware offload (switchdev). See
  [docs/sriov-networks.md](docs/sriov-networks.md).
- **Managed management network** — optionally own a single shared mgmt network so NFVOs
  reuse one fabric instead of creating one VPC per network service. See
  [docs/management-network.md](docs/management-network.md).
- **cloud-init** — pass NoCloud / ConfigDrive user-data via Kubernetes Secret (bypasses
  KubeVirt's 2 KiB inline limit).
- **Monitoring** — opt-in Prometheus `/metrics` endpoint exposing kube-vim's own
  operational metrics plus `kubevim_*_info` correlation metrics that join backend
  (KubeVirt/kube-OVN/SR-IOV) series to ETSI resource IDs. See
  [docs/monitoring.md](docs/monitoring.md).

## Quick start (Kind)

Spin up a local Kind cluster with all dependencies and install kube-vim:

```bash
make kind-install
```

This creates a Kind cluster, installs Kube-OVN, KubeVirt, CDI, and Multus, builds the
container images, loads them into the cluster, and applies the kube-vim manifests. Pass
`DEV=1` to additionally expose a NodePort for local access.

Tear down:

```bash
make kind-delete
```

## Build

Requires **Go 1.25+** and Docker.

```bash
make build          # compile bin/kube-vim and bin/gateway
make test           # go test ./...
make lint           # golangci-lint + yamllint
make docker-build   # build both container images
```

Binaries are written to `bin/`. Build dependency tools are installed under `bin/` on demand.

## Deployment

kube-vim is deployed with the Helm chart in [`dist/chart`](dist/chart):

```bash
helm install kube-vim ./dist/chart -n kube-vim --create-namespace
```

Both components read configuration loaded by [Viper](https://github.com/spf13/viper) from a
file or environment variables; the chart renders it into a ConfigMap. The configuration
schema is defined by OpenAPI 3.0 in [`configs/`](configs):

- `configs/kube-vim-config.openapi.yaml` — vim configuration
- `configs/kube-vim-gw-config.openapi.yaml` — gateway configuration

Key defaults: vim gRPC on `:50051`, gateway REST on `:8080`. See
[`dist/chart/values.yaml`](dist/chart/values.yaml) for the full set of knobs (images,
RBAC, service types, ingress, management/underlay network).

## API & reference points

| Specification | Reference point | Transport |
|---|---|---|
| ETSI GS NFV-IFA 005 | Or-Vi (NFVO → VIM) | gRPC / Protobuf |
| ETSI GS NFV-IFA 006 | Vi-Vnfm (VNFM → VIM) | gRPC / Protobuf |
| ETSI GS NFV-SOL 013 | RESTful protocol for Or-Vi | REST / JSON (via gateway) |

The API definitions, generated Go stubs, OpenAPI schema, and a generated Python client
(used by OSM) live in the separate
[kube-vim-api](https://github.com/kube-nfv/kube-vim-api) repository. An additional **admin
API** (outside the ETSI reference points) handles image management tasks.

## Documentation

- [docs/management-network.md](docs/management-network.md) — managed management network design & runbook
- [docs/sriov-networks.md](docs/sriov-networks.md) — SR-IOV networks design & runbook
- [docs/monitoring.md](docs/monitoring.md) — telemetry / metrics design & runbook

## License

[Apache 2.0](LICENSE).
