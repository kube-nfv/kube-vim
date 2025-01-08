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
	viper.SetDefault("service.port", 50051)

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

type Config struct {
	Service *ServiceConfig
	K8s     *K8sConfig
	Image   *ImageConfig
    Network *NetworkConfig
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
	Http   *HttpImageConfig
}

type NetworkConfig struct {
    Mgmt *MgmtNetworkConfig
}

type GlanceConfig struct {
	Identity *OpenstackIdentityConfig
	Region   string
}

type LocalImageConfig struct {
	Location string
}

// TODO(dmalovan): add support for the https
type HttpImageConfig struct {
	//Hack: Not accessible field to initialize http even if empty container specified in yaml
	initEmpty    bool
	StorageClass string
}

type OpenstackIdentityConfig struct {
	Endpoint string
	Username string
	Password string
}

type MgmtNetworkConfig struct {
    Underlay *UnderlayNetworkConfig
}

type UnderlayNetworkConfig struct {
    ProviderNetwork *ProviderNetworkConfig
    Vlan            *VlanConfig
    Subnet          *SubnetConfig
}

type ProviderNetworkConfig struct {
    DefaultInterface string
}

type VlanConfig struct {
    Id uint16
}

type SubnetConfig struct {
    Protocol  string
    CidrBlock string
    Gateway  string
}
