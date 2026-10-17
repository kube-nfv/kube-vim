package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	common "github.com/kube-nfv/kube-vim/internal/config"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selfSigned returns a self-signed certificate for dnsName and its PEM encoding.
func selfSigned(t *testing.T, dnsName string) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// tlsServer accepts TLS connections with cert and completes the handshake.
func tlsServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}})
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()
	return ln.Addr().String()
}

func TestKubevimTransportCredentials(t *testing.T) {
	serverCert, serverPEM := selfSigned(t, "kube-vim")
	_, otherPEM := selfSigned(t, "kube-vim")
	addr := tlsServer(t, serverCert)

	enabled := true
	disabled := false
	serverName := "kube-vim"

	tests := []struct {
		name      string
		cfg       *common.TlsClientConfig
		protocol  string
		handshake bool
		wantErr   bool
	}{
		{name: "nil config is plaintext", cfg: nil, protocol: "insecure"},
		{name: "disabled is plaintext", cfg: &common.TlsClientConfig{Enabled: &disabled, InsecureSkipVerify: &enabled}, protocol: "insecure"},
		{name: "trusted CA", cfg: &common.TlsClientConfig{Enabled: &enabled, ServerName: &serverName, RootCAs: &[]string{writeFile(t, serverPEM)}}, protocol: "tls", handshake: true},
		{name: "untrusted CA", cfg: &common.TlsClientConfig{Enabled: &enabled, ServerName: &serverName, RootCAs: &[]string{writeFile(t, otherPEM)}}, protocol: "tls", handshake: true, wantErr: true},
		{name: "skip verify", cfg: &common.TlsClientConfig{Enabled: &enabled, InsecureSkipVerify: &enabled}, protocol: "tls", handshake: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := kubevimTransportCredentials(tt.cfg)
			require.NoError(t, err)
			assert.Equal(t, tt.protocol, creds.Info().SecurityProtocol)
			if !tt.handshake {
				return
			}
			conn, err := net.Dial("tcp", addr)
			require.NoError(t, err)
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _, err = creds.ClientHandshake(ctx, "kube-vim:50051", conn)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKubevimTransportCredentialsBadRootCA(t *testing.T) {
	enabled := true

	_, err := kubevimTransportCredentials(&common.TlsClientConfig{Enabled: &enabled, RootCAs: &[]string{filepath.Join(t.TempDir(), "missing.crt")}})
	assert.ErrorIs(t, err, os.ErrNotExist)

	_, err = kubevimTransportCredentials(&common.TlsClientConfig{Enabled: &enabled, RootCAs: &[]string{writeFile(t, []byte("not a cert"))}})
	var invalid *apperrors.ErrInvalidArgument
	assert.ErrorAs(t, err, &invalid)
}
