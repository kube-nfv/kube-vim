package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	common "github.com/kube-nfv/kube-vim/internal/config"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// kubevimTransportCredentials returns plaintext credentials unless TLS is enabled in cfg.
func kubevimTransportCredentials(cfg *common.TlsClientConfig) (credentials.TransportCredentials, error) {
	if !common.IsClientTlsEnabled(cfg) {
		return insecure.NewCredentials(), nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.InsecureSkipVerify != nil {
		tlsCfg.InsecureSkipVerify = *cfg.InsecureSkipVerify
	}
	if cfg.ServerName != nil {
		tlsCfg.ServerName = *cfg.ServerName
	}
	if cfg.RootCAs != nil && len(*cfg.RootCAs) > 0 {
		pool := x509.NewCertPool()
		for _, path := range *cfg.RootCAs {
			pem, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read root CA '%s': %w", path, err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, &apperrors.ErrInvalidArgument{Field: "kubevim.tls.rootCAs", Reason: fmt.Sprintf("no PEM certificate in '%s'", path)}
			}
		}
		tlsCfg.RootCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}
