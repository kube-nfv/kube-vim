package auth

import (
	"context"

	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
)

// Principal is an authenticated API client.
type Principal struct {
	ClientID string
	Subject  string
	Issuer   string
}

// Authenticator validates an OAuth 2.0 access token.
type Authenticator interface {
	// Verify returns the token's client, or an apperrors.ErrUnauthenticated.
	Verify(ctx context.Context, rawToken string) (*Principal, error)
}

// Enabled reports whether auth is switched on in cfg.
func Enabled(cfg *config.AuthConfig) bool {
	return cfg != nil && cfg.Enabled != nil && *cfg.Enabled
}
