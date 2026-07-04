# CI/CD and Release Management

Status: accepted (2026-07-03)
Owner: kube-vim
Scope: GitHub Actions, container/chart publishing, branch protection

## TL;DR

Three GitHub Actions workflows drive validation and releases, all delegating to
the `make` targets so CI mirrors local development:

- **`ci`** — runs on every PR and push to `main`: `build` (+ generated/formatted
  code is committed), `test`, and `helm-lint`. A no-op `ci` gate job aggregates
  them and is the single required status check on `main`.
- **`release`** — runs on a `v*` tag: builds and pushes both images to
  `ghcr.io/kube-nfv`, then cuts a GitHub Release with binaries.
- **`chart-release`** — runs on a `chart/v*` tag: packages the Helm chart and
  pushes it as an OCI artifact to `ghcr.io/kube-nfv/charts`.

The app and the chart release **independently**. The only link between them is
the chart's `appVersion`, bumped by hand in a reviewed PR.

## Problem

The repo had no CI: nothing ran on PRs, images and charts were built by hand,
and there was no release automation. Nothing stopped a broken change or a direct
push to `main`, and "releases come from git tags" was a convention with no
tooling behind it.

## Decisions

### `make` targets are the single source of truth

Workflows call `make build`, `make test`, `make docker-build`, etc. rather than
re-implementing build logic in YAML. What passes locally passes in CI, and there
is one place to change build behaviour.

### App and chart release independently

An app release and a chart release answer different questions ("what code runs"
vs "how it is packaged and deployed") and change at different cadences. Coupling
them into one tag would force a chart bump on every app release and vice versa.

- App release: tag `v1.2.3` → images `ghcr.io/kube-nfv/{kube-vim,gateway}:1.2.3`
  (+ `:latest`), binaries, and a GitHub Release. The chart is untouched.
- Chart release: tag `chart/v0.1.0` → `helm package` + OCI push. No image build.

Neither workflow triggers the other.

### `appVersion` is the chart↔app link, set manually

`Chart.yaml`'s `appVersion` names the app release the chart deploys by default.
The image tag in the templates falls back to it:

```
tag := .Values.<svc>.image.tag | default .Values.global.image.tag | default .Chart.AppVersion
```

`global.image.tag` is empty by default, so bumping `appVersion` repoints the
default image with no package-time mutation, while users can still override the
tag. `appVersion` is bumped by hand in a reviewed PR, deliberately: a chart
should only claim an app version it has actually been rendered/tested against,
and a chart must never reference an image tag that has not been published yet.
Chart releases therefore **follow** app releases.

### Branch protection on `main`

`main` requires a PR (direct pushes blocked), the `ci` check to pass and be
up to date, linear history, and conversation resolution; force-pushes and
deletion are blocked. Only members of the `maintainers` team may bypass the PR
requirement and push directly to `main`. Configured via the GitHub API (not a
committed file).

## Operator runbook

### Cut an app release

1. Merge the code to `main`.
2. Tag and push: `git tag v1.2.3 && git push origin v1.2.3`.
3. The `release` workflow publishes the images, binaries, and GitHub Release.

### Cut a chart release

1. In a PR, bump `Chart.yaml` `version` (packaging version, always) and, if
   retargeting the app, `appVersion` to an **already-published** app tag.
2. Merge to `main`.
3. Tag and push: `git tag chart/v0.1.0 && git push origin chart/v0.1.0`.
   The `chart-release` workflow packages and pushes the chart to
   `oci://ghcr.io/kube-nfv/charts`.

### Ship a new app version via Helm

App and chart are decoupled, so this is two steps: cut the app release, then a
chart release whose `appVersion` points at it.

## Known gaps / follow-ups

- **Lint is not yet in CI.** golangci-lint is pinned to a version that cannot
  parse Go 1.25; migrating to v2 surfaces a backlog of findings. The `lint` job
  is present but commented out in `ci.yaml`; re-add it to the `ci` gate once the
  migration and cleanup land (tracked in #23).
- **Single-arch images.** Images are built for the runner's architecture
  (amd64). Multi-arch (amd64/arm64) and e2e via `make kind-install` are planned
  follow-ups.
