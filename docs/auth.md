# Authentication

Status: implemented (OAuth 2.0 bearer JWT, one SOL 013 scope per operation)

## Problem

kube-vim accepted every request that reached the gRPC port or the gateway, admin API
included. ETSI GS NFV-SOL 013 (v5.4.1, clause 8) requires every NFV-MANO API request to be
authorized with OAuth 2.0 and rejects requests without credentials.

What SOL 013 clause 8 asks of kube-vim:

- kube-vim, the API producer, is the OAuth 2.0 resource server. The consumer (OSM RO, a
  VNFM, an NFVO) is the client and gets its token with the client credentials grant from
  an authorization server. SOL 013 does not say which entity that is; an external IdP is
  fine (8.1).
- The client sends `Authorization: Bearer <token>` on every request (RFC 6750). A missing
  or invalid token is rejected with RFC 6750 error codes and `WWW-Authenticate` (8.3.2).
- Invalid or expired token: 401. Insufficient rights: 403 (8.2.2).
- How the producer validates a token is out of SOL 013's scope.
- Plain bearer tokens are deprecated in favour of RFC 8705 certificate-bound tokens over
  mutual TLS (8.2.2, 8.2.7).
- Scopes follow `{apiName}:{apiMajorVersion}:{permission}[:qualifier]*[:readonly]` (8.3.7).
- Everything runs over TLS (clause 4).

## Decisions

- **JWT access tokens, validated locally.** kube-vim finds the issuer's JWKS through OIDC
  discovery (`<issuer>/.well-known/openid-configuration`) and checks signature, `iss`,
  `aud` and `exp`. Any OAuth 2.0 / OIDC provider that issues JWT access tokens works
  (Keycloak, Dex, Okta, Entra ID); there is no provider-specific code.
- **One scope per operation, checked by kube-vim.** Each v1 RPC declares the scope it
  needs (`<api>:v1:<permission>`, e.g. `compute:v1:allocate`) in its
  `openapiv3_operation` security option in kube-vim-api. kube-vim reads it from the proto
  registry at request time and rejects a token without that scope with
  `PermissionDenied` (403); the scopes are never listed in kube-vim. The token's scopes
  come from its `scope` claim (space-separated, RFC 9068). An NFVO is typically granted
  every scope, a VNFM every scope except `image:v1:{add,update,delete}`; the IdP's client
  policy grants them. RPCs that declare no scope (gRPC reflection) need only a valid token.
- **Plain bearer for now.** RFC 8705 certificate-bound tokens need mutual TLS from the
  client through the gateway; OSM RO does not support it yet.
- **kube-vim validates, the gateway passes through.** grpc-gateway forwards the HTTP
  `Authorization` header as `authorization` metadata, maps `Unauthenticated` to 401 and
  `PermissionDenied` to 403, and copies an `Unauthenticated` status message into
  `WWW-Authenticate`. kube-vim makes that message an RFC 6750 challenge, so the gateway
  needs no auth code and direct gRPC clients get the same checks. The gateway forwards the
  caller's token, so kube-vim sees the real client.
- **Secure by default.** With auth on, kube-vim refuses to start without server TLS or
  with an `http://` issuer unless `allowInsecureTransport` is set. The chart refuses to
  render auth without gateway TLS under the same flag. Setting it is reasonable when OSM
  and kube-vim talk over ClusterIP inside one cluster.
- **Discovery at startup.** If the issuer is unreachable, kube-vim fails to start and
  Kubernetes restarts it. Keys are fetched again when a token carries an unknown `kid`, so
  IdP key rotation needs no restart.

Not done yet: RFC 7662 introspection for opaque tokens (Keystone OAuth2), `:readonly`
scopes and roles, an RFC 6750 `insufficient_scope` challenge on 403, scopes from an
`scp` array claim, RFC 8705 certificate-bound tokens, the ETSI GS NFV-SEC 022 NFV access
token profile, configurable clock skew.

## Design

- `internal/kubevim/auth`: the `Authenticator` interface, `Principal`, context helpers,
  the unary and stream interceptors and the scope check (`authorize`, `scope.go`). The
  stream interceptor covers gRPC reflection.
- `internal/kubevim/auth/oidc`: the JWT/JWKS implementation on `coreos/go-oidc`.
  `NewAuthenticator` also runs the startup checks.
- `internal/kubevim/server.NewNorthboundServer` chains error conversion, then auth, then
  logging, so request logs carry the client.
- The client is identified by the first claim present among `client_id` (RFC 9068),
  `azp` and `sub`. It is logged as `Client` on every request; managers do not use it yet.
- `apperrors.ErrUnauthenticated` maps to `codes.Unauthenticated`. Its `Reason` goes to the
  client; its `Err` (the validation detail) is only logged. A missing scope is
  `apperrors.ErrPermissionDenied`, mapped to `codes.PermissionDenied`.

Challenges returned in `WWW-Authenticate`:

| Request | Challenge |
| --- | --- |
| No token, or another scheme | `Bearer realm="kube-vim"` |
| Invalid token | `Bearer realm="kube-vim", error="invalid_token", error_description="invalid token"` |
| Expired token | `... error_description="token expired"` |

## Configuration

kube-vim (`configs/kube-vim-config.openapi.yaml`, `AuthConfig`):

```yaml
service:
  server:
    auth:
      enabled: true
      issuer: https://idp.example/realms/nfv   # must equal the token's iss
      audience: kube-vim                       # must be in the token's aud
      signingAlgorithms: [RS256]               # default RS256
      issuerCA: /etc/kube-vim/idp-ca/ca.crt    # optional, default host roots
      allowInsecureTransport: false
```

## Runbook

Chart values (`vim.auth`): `enabled`, `issuer`, `audience`, `signingAlgorithms`,
`allowInsecureTransport`, and `issuerCASecretName`, a Secret whose `ca.crt` is mounted at
`/etc/kube-vim/idp-ca` and used as `issuerCA`. Turn on TLS first ([TLS](tls.md)).

Keycloak:

1. Create a confidential client for the consumer (for example `osm-ro`) with service
   accounts enabled (client credentials grant).
2. Add an audience mapper that puts `kube-vim` into the access token's `aud`; Keycloak
   otherwise issues `aud: account`.
3. `issuer` is the realm URL, `https://<host>/realms/<realm>`.

Check it through the gateway:

```bash
TOKEN=$(curl -s -d grant_type=client_credentials -u osm-ro:<secret> \
  https://<host>/realms/<realm>/protocol/openid-connect/token | jq -r .access_token)
curl -i https://<gateway>/image/v1/images                                   # 401
curl -i -H "Authorization: Bearer $TOKEN" https://<gateway>/image/v1/images  # 200
```

A 401 with `error="invalid_token"` and the kube-vim log line `Request rejected` carry the
cause (wrong `aud`, `iss` mismatch, expired, unknown key).
