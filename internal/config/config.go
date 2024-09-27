package config

var (
    KubeNfvManagedbyLabel = map[string]string{
        "app.kubernetes.io/managed-by": "kube-nfv",
    }
    KubeNfvDefaultNamespace = "kube-nfv"
)

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
	Config string
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
