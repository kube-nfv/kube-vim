# TLS

Status: implemented (server TLS on vim and gateway, gateway-to-vim client TLS)

## Problem

Both servers could already serve TLS from a cert/key, but the gateway's gRPC client to
vim only spoke plaintext: `kubevim.tls.insecureSkipVerify: true` meant plaintext, `false`
failed with `ErrUnsupported`, and `rootCAs` was never read. The chart had no way to mount
certificates and dropped any `kubevim` block in the gateway config.

## Decisions

- TLS stays opt-in. In a single cluster, vim, gateway and their clients talk over
  ClusterIP, and plaintext is the default.
- The gateway client switches on `kubevim.tls.enabled` (default `false`), not on the
  presence of other TLS fields. Existing configs keep plaintext; `insecureSkipVerify` now
  means TLS without certificate checks.
- The chart takes existing `kubernetes.io/tls` Secrets (the cert-manager layout) rather
  than issuing certificates, so it has no cert-manager dependency.
- A server's `tls` block is used only when both `cert` and `key` are set
  (`common.IsServerTlsConfigured`).

## Configuration

Gateway client (`configs/common.openapi.yaml`, `tlsClientConfig`):

```yaml
kubevim:
  url: kube-vim:50051
  tls:
    enabled: true
    rootCAs: [/path/ca.crt]    # empty: host root CAs
    serverName: kube-vim       # defaults to the host in url
    insecureSkipVerify: false
```

Servers (`tlsServerConfig`): `service.server.tls.cert` and `.key`.

## Runbook

Chart values:

- `vim.tls.secretName`: Secret with `tls.crt`, `tls.key`, `ca.crt`. Mounted into vim at
  `/etc/kube-vim/tls`. Its `ca.crt` is mounted into the gateway, which then connects with
  `enabled: true`, that CA and `serverName` set to the vim service name. The certificate
  must have the vim service name as a DNS SAN.
- `gateway.tls.secretName`: Secret with `tls.crt`, `tls.key`, mounted into the gateway at
  `/etc/kube-vim-gateway/tls`; the gateway then serves HTTPS. An Ingress in front of it
  needs its backend protocol set to HTTPS.

Neither component can read the key through the API: kube-vim's Role grants Secrets only
`create`, `update` and `delete` (for cloud-init user data), and the gateway runs under its
own service account with no RBAC and no mounted token.

Certificates are read once at startup. After a Secret rotates, restart the pods
(`kubectl rollout restart`).

The chart's TLS values need a gateway image that knows `kubevim.tls.enabled`; older
images ignore it, connect in plaintext and fail against a TLS vim.

Bearer token authentication builds on this; see [Authentication](auth.md).
