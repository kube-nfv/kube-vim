package config

import (
	"os"

	"github.com/spf13/viper"
)

var (
	KubeNfvManagedbyLabel = map[string]string{
		"app.kubernetes.io/managed-by": "kube-nfv",
	}
	KubeNfvDefaultNamespace = "kube-nfv"
)

func init() {
	// Set config defaults
	viper.SetDefault("service.logLevel", "Info")
	viper.SetDefault("service.port", 50051)

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		podNamespace = KubeNfvDefaultNamespace
	}
	viper.SetDefault("k8s.namespace", podNamespace)
}

type Config struct {
	Service *ServiceConfig
	K8s     *K8sConfig
	Image   *ImageConfig
}

type ServiceConfig struct {
	Ip       string
	Port     string
	LogLevel string
}

type K8sConfig struct {
	Config    string
	Namespace string
}

type ImageConfig struct {
	Glance *GlanceConfig
	Local  *LocalImageConfig
}

type GlanceConfig struct {
	Identity *OpenstackIdentityConfig
	Region   string
}

type LocalImageConfig struct {
	Location string
}

type OpenstackIdentityConfig struct {
	Endpoint string
	Username string
	Password string
}
