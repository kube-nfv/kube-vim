package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	"github.com/kube-nfv/kube-vim/internal/kubevim"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type CmdLineOpts struct {
	configPath string
}

var (
	opts CmdLineOpts
)

func init() {
	// Parse CmdLine flags
	flag.StringVar(&opts.configPath, "config", "/etc/kube-vim/config.yaml", "kube-vim configuration file path")

	// Init Viper Defaults
	viper.SetDefault("service.logLevel", "info")
	viper.SetDefault("service.server.port", 50051)

	viper.SetDefault("image.storageClass", "default")

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		podNamespace = common.KubeNfvDefaultNamespace
	}
	viper.SetDefault("k8s.namespace", podNamespace)
}

func SetConfigDefaultAfterInit() {
	// Temporary hack to initialize http node in configuration if it is empty like
	// http: {}
	if viper.IsSet("image.http") {
		viper.SetDefault("image.http.initEmpty", true)
	}
}

func main() {
	flag.Parse()
	viper.SetConfigFile(opts.configPath)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Can't read kube-vim configuration from path '%s': %v", opts.configPath, err)
	}
	SetConfigDefaultAfterInit()
	var config config.Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Parse kube-vim configuration from path '%s': %v", opts.configPath, err)
	}

	// Initialize the logger
	logger, err := common.InitLogger(*config.Service.LogLevel)
	if err != nil {
		log.Fatalf("Initialize zap logger: %v", err)
	}
	defer logger.Sync() // Ensure all logs are flushed before the application exits

	cfgStr, err := json.Marshal(config)
	if err == nil {
		logger.Debug("Kube-vim configuration loaded", zap.String("config", string(cfgStr)))
	}

	mgr, err := kubevim.NewKubeVimManager(&config, logger.Named("Kubevim"))
	if err != nil {
		log.Fatalf("Create kubevim manager: %v", err)
	}

	// Create main context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger.Info("Installing signal handlers")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	wg := sync.WaitGroup{}

	wg.Add(2)
	go func() {
		defer wg.Done()
		misc.ShutdownHandler(logger, ctx, sigs, cancel)
	}()
	go func() {
		defer wg.Done()
		mgr.Start(ctx)
	}()

	wg.Wait()
	logger.Info("Exiting cleanly...")
	os.Exit(0)
}
