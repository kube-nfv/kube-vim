package common

import "fmt"

var (
	K8sManagedByLabel       = "app.kubernetes.io/managed-by"
	KubeNfvName             = "kube-nfv"
	KubeNfvDefaultNamespace = "kube-nfv"
	MgmtNetworkName         = "mgmt-net"

	ManagedByKubeNfvSelector = fmt.Sprintf("%s=%s", K8sManagedByLabel, KubeNfvName)
)

func IsServerTlsConfigured(cfg *TlsServerConfig) bool {
	return cfg != nil &&
		cfg.Cert != nil && *cfg.Cert != "" &&
		cfg.Key != nil && *cfg.Key != ""
}
