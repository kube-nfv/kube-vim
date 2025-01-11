package kubevimgateway

import (
	"flag"
	"log"

	config "github.com/DiMalovanyy/kube-vim/internal/config/gateway"
	"github.com/spf13/viper"
)

type CmdLineOpts struct {
	confgPath string
}

var (
	opts CmdLineOpts
)

func init() {
	// Parse CmdLine flags
	flag.StringVar(&opts.confgPath, "config", "/etc/kube-vim-gateway/config.yaml", "kube-vim gateway configuration file path")

	//Init Viper defaults
	viper.SetDefault("service.logLevel", "info")
	viper.SetDefault("service.server.port", 51155)

    viper.SetDefault("kubevim.ip", "127.0.0.1")
    viper.SetDefault("kubevim.port", 50051)
    viper.SetDefault("kubevim.tls.insecureSkipVerify", true)
}

func main() {
	flag.Parse()
	viper.SetConfigFile(opts.confgPath)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Can't read kube-vim gateway configuration from path %s. Error: %v", opts.confgPath, err)
	}
    var config config.Config
    if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Failed to parse kube-vim gateway configuration from path %s. Error %v", opts.confgPath, err)
    }

}
