# Software Images

Status: accepted
Owner: kube-vim
Scope: `internal/kubevim/image`

## Problem

The v1 image API takes a source as a oneof of kinds (http, s3, registry, upload, and
the Glance and local catalogs). Every kind ends up as a disk image in a PVC that compute
clones boot disks from, but each kind has its own request fields, credentials and
input-only data.

## Design

One backend, several frontends:

- **Backend: CDI** (`image/cdi`). Owns the Kubernetes side: a `VolumeImportSource` and a
  `DataVolume` per image (both named `image-<id>`, both labelled with the image ID), the
  image status, lookups by ID through the cache index, and the stored image.
- **Frontends: sources** (`image/source/<kind>`). Each implements `image.Source` for one
  `ImageSource` member: it turns the request's source into a CDI `ImportSourceType` and
  returns the source as stored and returned to clients, without input-only fields. A
  frontend rejects fields it does not handle with `ErrUnsupported`.

The composition root builds the enabled sources as a map keyed by the member name
(`image.SourceKind`, e.g. `http`); a request for a kind without an entry is rejected with
`UNSUPPORTED_FIELD`. Today only `http` exists.

Image-level fields that are not source-specific (`importPolicy`, `firmware`, `diskBus`,
`minDisk`, `minRam`) are checked by the backend; all are rejected until implemented.

### Stored image

The consumer's fields (name, provider, version, metadata, the stored source, import
policy) are kept as protojson in the `image.kubevim.kubenfv.io/image` annotation on the
`VolumeImportSource` and returned as given. Outputs come from the `DataVolume`:

| DataVolume phase | Image status |
|---|---|
| `Succeeded` | `AVAILABLE` (`size` is the PVC request) |
| `Failed` | `FAILED` (`statusDetail` from the `Running` condition) |
| `ImportScheduled`, `ImportInProgress` | `IMPORTING` (`progress` from CDI) |
| `UploadScheduled`, `UploadReady` | `UPLOAD_PENDING` |
| anything else | `PENDING` |

The metadata key `kubevim.kubenfv.io/pvc` names the PVC compute clones from.

## Not designed yet

- Catalog sources (Glance, local) also list entries that are not imported. Expected shape:
  an optional catalog interface on a frontend whose entries the backend merges into
  `ListImages`.
- `IMPORT_POLICY_ON_FIRST_USE` is a backend change: import when the first compute
  resource uses the image.
- Image size: every image gets a 10Gi PVC.
