# Kubernetes Client Caching

## Overview

Kube-vim accesses Kubernetes through a single shared controller-runtime
`cluster.Cluster`. Reads are served from a watch-synced in-memory cache, writes go
straight to the apiserver, and a small number of strongly-consistent reads use an
uncached reader. All domain managers receive their client by injection instead of
building their own typed clientsets.

## Background

### The problem

Every domain manager used to build its own typed clientset from a shared
`*rest.Config` and call the apiserver live on every request:

- Duplicated clients and transports (the kubevirt, k8s and net-attach clientsets were
  each constructed more than once), each with its own connection pool and rate
  limiter. `rest.Config` was left untuned.
- Read amplification: list-all-then-filter-by-UID lookups, unscoped cluster-wide
  Lists, and an N+1 fan-out in `ListComputeResources` (one List plus a VMI Get and a
  pod List per VM) that also ran on every Prometheus scrape via the telemetry
  collectors.
- Managers held concrete `*Clientset` fields, so they could not be exercised with
  fakes in unit tests.

### Why a cache is compatible with a stateless VIM

Kube-vim is stateless: all resource state lives in Kubernetes objects. A read cache
does not change that. It is a watch-synced replica of apiserver state, not an
authoritative store. Writes still go to the apiserver and Kubernetes objects remain
the source of truth; the cache only serves reads faster and offloads the apiserver.

## Design

### Shared cluster

`internal/k8s` builds the shared infrastructure once, in the composition root
(`internal/kubevim/manager.go`):

- `BuildScheme()` registers every API group kube-vim touches: core and storage
  (client-go builtins), kubevirt core and instancetype, CDI, kube-ovn and
  net-attach.
- `NewCluster()` builds a controller-runtime `cluster.Cluster`. It exposes:
  - `GetClient()`: a cache-backed reader plus a direct writer. Managers use this for
    Get/List (cache) and Create/Update/Patch/Delete (apiserver).
  - `GetAPIReader()`: an uncached reader that always hits the apiserver. Used only
    where a strongly-consistent read is required.
  - `GetCache()`: started once in `Start`, gated by `WaitForCacheSync` before the
    northbound server begins serving.

The `rest.Config` is tuned once (raised QPS/Burst, a `kube-vim` user agent). No global
request timeout is set, because that would cap the cache's long-lived watch
connections; per-call context deadlines are used instead.

### Per-type cache scope

The cache is scoped to the configured namespace and restricted to kube-nfv-owned
objects, with two deliberate exceptions:

| Type | Cached | Selector / scope | Why |
|---|---|---|---|
| VirtualMachine, VirtualMachineInstance | yes | namespace + managed-by | owned |
| VirtualMachineInstancetype, VirtualMachinePreference | yes | namespace + managed-by | owned |
| Vpc, Vlan, Subnet (kube-ovn, cluster-scoped) | yes | managed-by, cluster-wide | owned |
| NetworkAttachmentDefinition | yes | namespace + managed-by | owned |
| DataVolume, VolumeImportSource | yes | namespace + managed-by | owned |
| Pod | yes | namespace only, no label filter | virt-launcher pods carry kubevirt labels, not managed-by; hot path (launcher info and every scrape) |
| StorageClass (cluster-scoped) | no | read via apiReader | not owned and read rarely; keeps a cluster-wide StorageClass watch off the cache |

### Read-after-write policy

Cache reads are eventually consistent: a write reaches the cache only when the watch
delivers it back, a short moment later. Two situations need a stronger guarantee and
use `GetAPIReader()`:

- **Awaiting a just-created VMI.** After creating a VM, the compute manager polls the
  uncached reader until the VMI exists (`waitForVmi`), rather than watching or reading
  a cold cache.
- **Management-network reconciliation.** `EnsureManagementNetwork` reads objects that
  may pre-exist without the managed-by label and then adds it. Those objects are not
  in the managed-by-filtered cache, so all its reads use the uncached reader; label
  updates are applied as merge patches.

Delete preconditions (reading an object to validate it before deleting) read from the
cache and accept the brief eventual-consistency window. The flavour manager avoids the
precondition entirely by deleting via a label-scoped `DeleteAllOf`, which also keeps
the ownership guard without a read.

### Managers

Managers no longer construct clients. Each constructor takes a `client.Client` (and a
`client.Reader` where an uncached read is needed) plus its config. This is also what
makes managers testable against `sigs.k8s.io/controller-runtime/pkg/client/fake`.

The kubevirt typed clientset was removed entirely. It was only needed for the two VM
readiness watches, which are now a poll of the uncached reader. If VM subresources
(start/stop, console, migrate) are ever added, they will need the kubevirt clientset
or its REST client reintroduced, since `client.Client` does CRUD only.

## RBAC

The cache lists and watches every cached type, so each needs `list` and `watch`. All
owned types were already granted `verbs: ["*"]`. The one addition is `watch` on
`pods` in the compute-manager Role (it previously had only `get` and `list`), required
because the pod informer does list plus watch. StorageClass is read through the
uncached reader and needs only `get`/`list`, already granted.

## Operator notes

- Readiness: the process does not serve gRPC until the cache has synced
  (`WaitForCacheSync`).
- Memory: the cache holds only kube-nfv-owned objects (plus namespace pods), so its
  footprint tracks the number of managed resources.
- A missing `watch` grant on any cached type surfaces as the cache failing to sync or
  a List returning a forbidden error; check the Role/ClusterRole first.
