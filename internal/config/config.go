package config

type Config struct {
    Service *ServiceConfig
    K8s *K8sConfig
    Image *ImageConfig
}

type ServiceConfig struct {
    Ip string
    Port string
    LogLevel string
}

type K8sConfig struct {
    Config string
}


type ImageConfig struct {
    Glance *GlanceConfig
}

type GlanceConfig struct {
    Identity *OpenstackIdentityConfig
    Region string
}

type OpenstackIdentityConfig struct {
    Endpoint string
    Username string
    Password string
}
