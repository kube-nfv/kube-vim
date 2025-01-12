package common

var (
	K8sManagedByLabel       = "app.kubernetes.io/managed-by"
	KubeNfvName             = "kube-nfv"
	KubeNfvDefaultNamespace = "kube-nfv"
	MgmtNetworkName         = "mgmt-net"
)

func IsServerTlsConfigured(cfg *TlsServerConfig) bool {
	return cfg != nil &&
		cfg.Cert != nil && *cfg.Cert != "" &&
		cfg.Key != nil && *cfg.Key != ""
}
