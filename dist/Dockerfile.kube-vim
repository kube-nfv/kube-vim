FROM golang:1.23.0 AS builder
ARG TARGETOS TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.sum ./

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download
RUN apt update && apt install qemu-utils -y

# Copy the go source
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH}" go build -a -o kubevim cmd/kube-vim/main.go


# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
# TODO: Use different stages for prod and dev where different base images will be using
FROM gcr.io/distroless/static:nonroot
# FROM debian:bullseye
WORKDIR /
COPY --chown=root:root --from=builder /workspace/kubevim .
COPY --chown=root:root --from=builder /usr/bin/qemu-img /usr/bin/qemu-img
ENTRYPOINT ["/kubevim"]
USER 65532:65532
