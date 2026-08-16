# Error Handling

Status: accepted (2026-08-16)
Owner: kube-vim
Scope: `internal/errors`, gRPC server interceptor, manager/adapter conventions

## TL;DR

Manager and adapter code returns **typed, structural errors** from
`internal/errors` (imported as `apperrors`), never raw `status.Error`. A single
gRPC server interceptor converts whatever bubbles up into a gRPC status code
exactly once, at the northbound edge. Conversion is table-driven through a small
registry of pluggable `ErrorConverter`s (a common one and a Kubernetes one), so a
new error family is added in one place without touching the interceptor.

Two error shapes coexist:

- **Untyped sentinels** (`ErrNotImplemented`, `ErrUnsupported`, `ErrInternal`) for
  cases that carry no extra data, matched with `errors.Is`.
- **Typed structural errors** (`ErrNotFound`, `ErrInvalidArgument`,
  `ErrAlreadyExists`, `ErrPermissionDenied`, and the two K8s-ownership errors) that
  carry fields (entity, identifier, field, reason, ...), matched with `errors.As`.

Errors are wrapped with `fmt.Errorf("...: %w", err)` on the way up so the chain
stays intact for `errors.As` / `errors.Is` at the edge.

## Problem

kube-vim is a gRPC service in front of Kubernetes. Two things had to be true at
once:

1. **Managers must stay transport-agnostic.** Business logic lives in the domain
   managers (`compute`, `network`, `flavour`, `image`); the gRPC server is a thin
   adapter. If managers returned `status.Error(codes.NotFound, ...)` directly they
   would hardcode the transport, and the same code could not be reused behind a
   different edge or asserted cleanly in unit tests.
2. **Callers need meaningful gRPC codes.** An NFVO (OSM) driving the Or-Vi /
   Vi-Vnfm reference points must be able to tell "you asked for something that does
   not exist" (`NotFound`) from "your request is malformed" (`InvalidArgument`) from
   "kube-vim refuses to touch that object" (`PermissionDenied`). Collapsing
   everything to `Internal` is useless to the caller.

So the mapping from a domain error to a gRPC status has to happen somewhere, but
not inside every manager and not repeated in every RPC handler.

## Design

### Structural error types

`internal/errors/errors.go` defines the vocabulary. Untyped sentinels for the
data-less cases:

```go
var (
    ErrNotImplemented = errors.New("not implemented")
    ErrUnsupported    = errors.New("unsupported")
    ErrInternal       = errors.New("internal")
)
```

Typed structs for the cases that benefit from carrying context, each with a
pointer-receiver `Error()`:

| Type | Fields | Example message |
|---|---|---|
| `ErrNotFound` | `Entity`, `Identifier` | `compute 'abc' not found` |
| `ErrInvalidArgument` | `Field`, `Reason` | `invalid network IPAM: must have a subnetId reference` |
| `ErrAlreadyExists` | `Entity`, `Identifier` | `subnet 'x' already exists` |
| `ErrPermissionDenied` | `Resource`, `Reason` | `access denied to x: ...` |
| `ErrK8sObjectNotInstantiated` | `ObjectType`, `Identifier` | `Subnet is not from Kubernetes (likely created manually)` |
| `ErrK8sObjectNotManagedByKubeNfv` | `ObjectType`, `ObjectName`, `ObjectId` | `Subnet 'x' (uid: ...) not managed by kube-nfv` |

The two K8s-ownership errors back the load-bearing "kube-vim refuses objects it
does not own" guard: lookups filter on the managed-by label and raise
`ErrK8sObjectNotManagedByKubeNfv` when an object exists but was not created by
kube-vim.

### The converter registry

`internal/errors/grpc.go` defines a tiny plugin interface and a package-global
registry:

```go
type ErrorConverter interface {
    ConvertToGrpcError(err error) error // returns nil if it does not handle err
}

func RegisterErrorConverter(converter ErrorConverter)
```

Two converters register themselves in their `init()`:

- **`CommonErrorConverter`** (`common.go`) handles the sentinels and the four
  general typed errors.
- **`K8sErrorConverter`** (`k8s.go`) handles the two K8s-ownership typed errors plus
  standard `k8s.io/apimachinery/pkg/api/errors` predicates (`IsNotFound`,
  `IsAlreadyExists`, `IsInvalid`, `IsBadRequest`, `IsForbidden`, `IsUnauthorized`).

Each converter returns `nil` for errors it does not recognize, and the two handle
**disjoint** error sets, so registration order does not matter.

### `ToGRPCError`

`ToGRPCError(err)` is the single entry point:

1. `nil` in, `nil` out.
2. If `err` is **already** a gRPC status (`status.FromError`), return it unchanged.
   This lets a lower layer that genuinely needs a specific status set it directly
   and pass through untouched.
3. Otherwise walk the registered converters; return the first non-nil result. Both
   converters use `errors.As` / `errors.Is`, so a wrapped error deep in a
   `%w` chain is still matched.
4. Anything unrecognized becomes `codes.Internal` (`status.Errorf(codes.Internal,
   "%v", err)`). It never returns nil for a non-nil error.

### Where conversion happens

Conversion runs once, in the gRPC server (`internal/kubevim/server/server.go`).
`errorConversionInterceptor` is chained **first**, so every later interceptor and
the client see a proper status:

```go
grpc.ChainUnaryInterceptor(
    errorConversionInterceptor, // convert app errors -> gRPC status
    loggingInterceptor,         // logs the already-converted error
)
```

The logging interceptor sits second on purpose: it logs the converted status, not
the raw internal error. On the REST side the gateway (`internal/gateway`) is a
grpc-gateway proxy, so the gRPC status maps to an HTTP status through the standard
grpc-gateway rules; no second conversion layer exists.

## Code-to-gRPC mapping

| Error | gRPC code |
|---|---|
| `ErrNotFound` | `NotFound` |
| `ErrInvalidArgument` | `InvalidArgument` |
| `ErrAlreadyExists` | `AlreadyExists` |
| `ErrPermissionDenied` | `PermissionDenied` |
| `ErrNotImplemented` | `Unimplemented` |
| `ErrUnsupported` | `Unimplemented` |
| `ErrInternal` | `Internal` |
| `ErrK8sObjectNotInstantiated` | `InvalidArgument` |
| `ErrK8sObjectNotManagedByKubeNfv` | `PermissionDenied` |
| apimachinery `IsNotFound` | `NotFound` |
| apimachinery `IsAlreadyExists` | `AlreadyExists` |
| apimachinery `IsInvalid` / `IsBadRequest` | `InvalidArgument` |
| apimachinery `IsForbidden` | `PermissionDenied` |
| apimachinery `IsUnauthorized` | `Unauthenticated` |
| anything else | `Internal` |

## Conventions for contributors

- **Managers and adapters return `apperrors`, never `status.Error`.** Keep the
  transport code at the edge. The interceptor owns the mapping.
- **Carry context in the fields, not the message.** Prefer
  `&apperrors.ErrNotFound{Entity: "compute", Identifier: id}` over a hand-formatted
  string, so callers (and tests) can inspect the typed error.
- **Wrap on the way up** with `fmt.Errorf("get subnet '%s': %w", id, err)`. The
  `%w` keeps the chain matchable; add a plain sentence of context, do not restate
  the wrapped error.
- **Match typed errors with a pointer target.** `Error()` has a pointer receiver,
  so assert with `var e *apperrors.ErrNotFound; errors.As(err, &e)`, never
  `&apperrors.ErrNotFound{}` as a target. Match sentinels with `errors.Is`.
- **Add a new error family in one place.** Define the type in `errors.go`, add its
  `errors.As` arm to the matching converter (or write a new `ErrorConverter` and
  register it in `init()`); the interceptor and every manager are unaffected.

## Testing

`internal/errors/grpc_test.go` is table-driven over `(error -> expected code)`,
covering the typed errors, the sentinels, the apimachinery predicates, wrapped
(`%w`) chains, and the "already a gRPC status is preserved" path. Because the
converters are pure functions the mapping is unit-tested without a server; the
adapter tests (`server/grpc/vivnfm`) then assert that a manager error surfaces as
the right status end to end.

## Known gaps / follow-ups

- **`ErrAlreadyExists` is defined and wired but not yet emitted by managers** —
  duplicate creates currently surface as the apimachinery `IsAlreadyExists` path
  instead. Both map to `AlreadyExists`, so the wire behaviour is already correct;
  the typed error is available when a manager wants to raise it explicitly.
- **No structured error details.** Errors map to a code plus a message string;
  they do not attach `google.rpc.ErrorInfo` / status details. Fine for the current
  callers; revisit if a client needs machine-readable reason codes.
