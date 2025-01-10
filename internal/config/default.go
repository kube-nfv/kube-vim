package config

import (
	"os"

	"github.com/spf13/viper"
)

var (
	K8sManagedByLabel       = "app.kubernetes.io/managed-by"
	KubeNfvName             = "kube-nfv"
	KubeNfvDefaultNamespace = "kube-nfv"
	MgmtNetworkName         = "mgmt-net"
)

func init() {
	// Set config defaults
	viper.SetDefault("service.logLevel", "Info")
	viper.SetDefault("service.server.port", 50051)

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		podNamespace = KubeNfvDefaultNamespace
	}
	viper.SetDefault("k8s.namespace", podNamespace)
}

func InitDefaultAfterReading() {
	if viper.IsSet("image.http") {
		viper.SetDefault("image.http.initEmpty", true)
	}
}
