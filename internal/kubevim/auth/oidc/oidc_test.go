package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const audience = "kube-vim"

// fakeIDP serves OIDC discovery and a JWKS whose keys can be added at runtime.
type fakeIDP struct {
	srv    *httptest.Server
	caPath string

	mu   sync.Mutex
	keys []jose.JSONWebKey
}

func newFakeIDP(t *testing.T, tls bool) *fakeIDP {
	t.Helper()
	idp := &fakeIDP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": idp.srv.URL, "jwks_uri": idp.srv.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		idp.mu.Lock()
		defer idp.mu.Unlock()
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: idp.keys})
	})
	if tls {
		idp.srv = httptest.NewTLSServer(mux)
		idp.caPath = filepath.Join(t.TempDir(), "ca.crt")
		ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: idp.srv.Certificate().Raw})
		require.NoError(t, os.WriteFile(idp.caPath, ca, 0o600))
	} else {
		idp.srv = httptest.NewServer(mux)
	}
	t.Cleanup(idp.srv.Close)
	return idp
}

// addKey generates an RSA key, publishes its public half under kid and returns the private key.
func (i *fakeIDP) addKey(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	i.mu.Lock()
	defer i.mu.Unlock()
	i.keys = append(i.keys, jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"})
	return key
}

func (i *fakeIDP) claims(extra map[string]any) map[string]any {
	c := map[string]any{"iss": i.srv.URL, "aud": audience, "sub": "service-account-osm", "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func sign(t *testing.T, alg jose.SignatureAlgorithm, key any, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: jose.JSONWebKey{Key: key, KeyID: kid}}, (&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	jws, err := signer.Sign(payload)
	require.NoError(t, err)
	raw, err := jws.CompactSerialize()
	require.NoError(t, err)
	return raw
}

func ptr[T any](v T) *T { return &v }

func serverConfig(idp *fakeIDP) *config.ServerConfig {
	return &config.ServerConfig{
		Tls: &common.TlsServerConfig{Cert: ptr("tls.crt"), Key: ptr("tls.key")},
		Auth: &config.AuthConfig{
			Enabled:  ptr(true),
			Issuer:   ptr(idp.srv.URL),
			Audience: ptr(audience),
			IssuerCA: ptr(idp.caPath),
		},
	}
}

func TestVerify(t *testing.T) {
	idp := newFakeIDP(t, true)
	key := idp.addKey(t, "k1")
	authn, err := NewAuthenticator(context.Background(), serverConfig(idp))
	require.NoError(t, err)

	unpublished, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tests := []struct {
		name       string
		token      func() string
		wantClient string
		wantReason string
	}{
		{name: "client_id claim", wantClient: "osm-ro", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(map[string]any{"client_id": "osm-ro", "azp": "other"}))
		}},
		{name: "azp claim", wantClient: "osm-azp", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(map[string]any{"azp": "osm-azp"}))
		}},
		{name: "sub fallback", wantClient: "service-account-osm", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(nil))
		}},
		{name: "audience in a list", wantClient: "service-account-osm", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(map[string]any{"aud": []string{"account", audience}}))
		}},
		{name: "rotated key is fetched", wantClient: "service-account-osm", token: func() string {
			return sign(t, jose.RS256, idp.addKey(t, "k2"), "k2", idp.claims(nil))
		}},
		{name: "expired", wantReason: "token expired", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}))
		}},
		{name: "wrong audience", wantReason: "invalid token", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(map[string]any{"aud": "other"}))
		}},
		{name: "wrong issuer", wantReason: "invalid token", token: func() string {
			return sign(t, jose.RS256, key, "k1", idp.claims(map[string]any{"iss": "https://evil.example"}))
		}},
		{name: "bad signature", wantReason: "invalid token", token: func() string {
			return sign(t, jose.RS256, unpublished, "k1", idp.claims(nil))
		}},
		{name: "unsupported algorithm", wantReason: "invalid token", token: func() string {
			return sign(t, jose.ES256, ecKey, "k1", idp.claims(nil))
		}},
		{name: "malformed", wantReason: "invalid token", token: func() string { return "not.a.jwt" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := authn.Verify(context.Background(), tt.token())
			if tt.wantReason != "" {
				var unauth *apperrors.ErrUnauthenticated
				require.True(t, errors.As(err, &unauth), "got %v", err)
				assert.Equal(t, tt.wantReason, unauth.Reason)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantClient, p.ClientID)
			assert.Equal(t, "service-account-osm", p.Subject)
			assert.Equal(t, idp.srv.URL, p.Issuer)
		})
	}
}

func TestNewAuthenticatorConfig(t *testing.T) {
	tlsIDP := newFakeIDP(t, true)
	plainIDP := newFakeIDP(t, false)
	badCA := filepath.Join(t.TempDir(), "bad.crt")
	require.NoError(t, os.WriteFile(badCA, []byte("not a cert"), 0o600))

	tests := []struct {
		name    string
		mutate  func(*config.ServerConfig)
		idp     *fakeIDP
		wantErr bool
	}{
		{name: "valid", idp: tlsIDP, mutate: func(*config.ServerConfig) {}},
		{name: "missing issuer", idp: tlsIDP, wantErr: true, mutate: func(c *config.ServerConfig) { c.Auth.Issuer = nil }},
		{name: "missing audience", idp: tlsIDP, wantErr: true, mutate: func(c *config.ServerConfig) { c.Auth.Audience = ptr("") }},
		{name: "no server TLS", idp: tlsIDP, wantErr: true, mutate: func(c *config.ServerConfig) { c.Tls = nil }},
		{name: "no server TLS allowed", idp: tlsIDP, mutate: func(c *config.ServerConfig) {
			c.Tls = nil
			c.Auth.AllowInsecureTransport = ptr(true)
		}},
		{name: "http issuer", idp: plainIDP, wantErr: true, mutate: func(*config.ServerConfig) {}},
		{name: "http issuer allowed", idp: plainIDP, mutate: func(c *config.ServerConfig) { c.Auth.AllowInsecureTransport = ptr(true) }},
		{name: "issuer not trusted", idp: tlsIDP, wantErr: true, mutate: func(c *config.ServerConfig) { c.Auth.IssuerCA = nil }},
		{name: "bad issuer CA", idp: tlsIDP, wantErr: true, mutate: func(c *config.ServerConfig) { c.Auth.IssuerCA = &badCA }},
		{name: "issuer mismatch", idp: tlsIDP, wantErr: true, mutate: func(c *config.ServerConfig) { c.Auth.Issuer = ptr(tlsIDP.srv.URL + "/") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := serverConfig(tt.idp)
			tt.mutate(cfg)
			_, err := NewAuthenticator(context.Background(), cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
