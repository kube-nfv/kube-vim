package oidc

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/auth"
)

const issuerRequestTimeout = 30 * time.Second

// authenticator validates JWT access tokens against an OIDC issuer's JWKS.
type authenticator struct {
	verifier *oidc.IDTokenVerifier
}

// NewAuthenticator checks cfg.Auth against the server's transport and runs OIDC discovery on the issuer.
func NewAuthenticator(ctx context.Context, cfg *config.ServerConfig) (auth.Authenticator, error) {
	authCfg := cfg.Auth
	if authCfg.Issuer == nil || *authCfg.Issuer == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "service.server.auth.issuer", Reason: "required when auth is enabled"}
	}
	if authCfg.Audience == nil || *authCfg.Audience == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "service.server.auth.audience", Reason: "required when auth is enabled"}
	}
	insecure := authCfg.AllowInsecureTransport != nil && *authCfg.AllowInsecureTransport
	if !insecure && !common.IsServerTlsConfigured(cfg.Tls) {
		return nil, &apperrors.ErrInvalidArgument{Field: "service.server.auth", Reason: "requires service.server.tls; set allowInsecureTransport to run without it"}
	}
	issuerURL, err := url.Parse(*authCfg.Issuer)
	if err != nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "service.server.auth.issuer", Reason: err.Error()}
	}
	if issuerURL.Scheme != "https" && !(insecure && issuerURL.Scheme == "http") {
		return nil, &apperrors.ErrInvalidArgument{Field: "service.server.auth.issuer", Reason: "must be an https URL; set allowInsecureTransport to allow http"}
	}

	client, err := issuerHTTPClient(authCfg.IssuerCA)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), *authCfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer '%s': %w", *authCfg.Issuer, err)
	}
	algs := []string{oidc.RS256}
	if authCfg.SigningAlgorithms != nil && len(*authCfg.SigningAlgorithms) > 0 {
		algs = *authCfg.SigningAlgorithms
	}
	return &authenticator{
		verifier: provider.Verifier(&oidc.Config{ClientID: *authCfg.Audience, SupportedSigningAlgs: algs}),
	}, nil
}

func (a *authenticator) Verify(ctx context.Context, rawToken string) (*auth.Principal, error) {
	token, err := a.verifier.Verify(ctx, rawToken)
	if err != nil {
		reason := "invalid token"
		var expired *oidc.TokenExpiredError
		if errors.As(err, &expired) {
			reason = "token expired"
		}
		return nil, &apperrors.ErrUnauthenticated{Reason: reason, Err: err}
	}
	var claims struct {
		ClientID string `json:"client_id"`
		Azp      string `json:"azp"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, &apperrors.ErrUnauthenticated{Reason: "invalid token", Err: err}
	}
	return &auth.Principal{
		ClientID: cmp.Or(claims.ClientID, claims.Azp, token.Subject),
		Subject:  token.Subject,
		Issuer:   token.Issuer,
	}, nil
}

// issuerHTTPClient returns the client for discovery and JWKS requests, trusting caPath if set.
func issuerHTTPClient(caPath *string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caPath != nil && *caPath != "" {
		pem, err := os.ReadFile(*caPath)
		if err != nil {
			return nil, fmt.Errorf("read issuer CA '%s': %w", *caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, &apperrors.ErrInvalidArgument{Field: "service.server.auth.issuerCA", Reason: fmt.Sprintf("no PEM certificate in '%s'", *caPath)}
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, Timeout: issuerRequestTimeout}, nil
}
