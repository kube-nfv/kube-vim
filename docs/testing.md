# Unit Testing

Status: accepted (2026-07-31)
Owner: kube-vim
Scope: unit-test framework, mocking strategy, what is and isn't tested

## TL;DR

Unit tests use the Go stdlib `testing` package with table-driven subtests and
[`testify`](https://github.com/stretchr/testify) (`require`/`assert`) for
assertions. They run via `make test` (`go test -count=1 ./...`), which the CI
`test` job invokes on every PR.

Today tests cover **pure, client-free logic only** — the `nfv⇄k8s` conversion
helpers in each domain's `utils.go`, format/marshal functions, and error
mapping. Domain managers (which call Kubernetes clientsets) are deliberately
left untested until the planned move to controller-runtime clients makes them
cleanly mockable.

## Problem

The repo had almost no test coverage. We needed a testing approach that:

- matches how the code is actually structured (plain client-go clientsets, not
  controller-runtime reconcilers), and
- doesn't create throwaway scaffolding, given a planned migration to
  controller-runtime with cached clients.

## Decisions

### stdlib `testing` + testify, not Ginkgo/Gomega/envtest

The heavyweight Kubernetes controller test stack (envtest spinning up a real
apiserver, Ginkgo/Gomega BDD) exists for **controller-runtime reconcilers**.
kube-vim is not one — it is a stateless gRPC service over plain client-go
clientsets. Adopting that stack would be a mismatch, so tests use the Go
standard `testing` package with table-driven subtests, plus `testify` for terse
`require` (fatal preconditions) / `assert` (soft checks) assertions.

Tests are **white-box** (same package as the code under test, `<file>_test.go`
beside it) so unexported converters can be exercised directly.

### Scope today: pure functions only

The highest-value, lowest-cost, most stable surface is the deterministic logic
with no Kubernetes client dependency:

- `nfv⇄k8s` conversions and validation in each domain's `utils.go`
  (`network/kubeovn`, `network/sriov`, `flavour/kubevirt`, `compute/kubevirt`),
- format/marshal helpers (e.g. SR-IOV CNI config rendering),
- error → gRPC/k8s mapping (`internal/errors`).

These functions survive an internals refactor, so the tests are durable.

### Managers are intentionally untested (for now)

Domain managers hold **concrete `*Clientset` fields** built from `*rest.Config`
inside their `NewXxx` constructors — they are not injectable, so a fake client
cannot be substituted without a refactor.

Rather than refactor every manager to an injectable clientset `Interface` now,
we wait for the planned migration to **controller-runtime + cached clients** and
then test managers against `sigs.k8s.io/controller-runtime/pkg/client/fake`.
Building fake-clientset plumbing against the current concrete clients would be
thrown away by that migration, so it is explicitly avoided.

The most complex untested logic is the network/IPAM resolution in
`compute/kubevirt/manager.go` (`initNetworks`, `getNetworkIpam`,
`getSubnetIpam`) — flagged for care until it becomes testable.

## How to add a test

- Put it in `<file>_test.go` next to the code, `package <same>` (white-box).
- Prefer a table-driven `t.Run` per case; use `require` for setup that must
  succeed and `assert` for the actual expectations.
- Constructing Kubernetes objects that must pass the ownership/instantiation
  guards:
  - `misc.IsObjectInstantiated` needs `UID`, `ResourceVersion`, and a non-zero
    `CreationTimestamp`.
  - `misc.IsObjectManagedByKubeNfv` needs the
    `app.kubernetes.io/managed-by: kube-nfv` label.
  - See `managedMeta(...)` in `internal/kubevim/network/kubeovn/utils_test.go`
    for a reusable helper.
- Run one package: `go test ./internal/kubevim/network/kubeovn/ -run TestName -v`.

## Known gaps / follow-ups

- **Managers, gateway wiring, and server adapters are untested** — pending the
  controller-runtime migration for the manager layer.
- **No coverage gate in CI.** `make test` runs but coverage is not measured or
  enforced.
