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

	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/gateway"
	"github.com/kube-nfv/kube-vim/internal/gateway"
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
	flag.StringVar(&opts.configPath, "config", "/etc/kube-vim-gateway/config.yaml", "kube-vim gateway configuration file path")

	//Init Viper defaults
	viper.SetDefault("service.logLevel", "info")
	viper.SetDefault("service.server.port", 51155)

	viper.SetDefault("kubevim.ip", "127.0.0.1")
	viper.SetDefault("kubevim.port", 50051)
	viper.SetDefault("kubevim.tls.insecureSkipVerify", true)
}

func main() {
	flag.Parse()
	viper.SetConfigFile(opts.configPath)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Can't read kube-vim gateway configuration from path %s. Error: %v", opts.configPath, err)
	}
	var config config.Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Failed to parse kube-vim gateway configuration from path %s. Error %v", opts.configPath, err)
	}

	// Initialize the logger
	logger, err := common.InitLogger(*config.Service.LogLevel)
	if err != nil {
		log.Fatalf("failed to initialize zap logger: %v", err)
	}
	defer logger.Sync() // Ensure all logs are flushed before the application exits

	cfgStr, err := json.Marshal(config)
	if err == nil {
		logger.Debug("Gateway configuration loaded", zap.String("config", string(cfgStr)))
	}

	gw, err := gateway.NewKubeVimGateway(&config, logger.Named("Gateway"))
	if err != nil {
		log.Fatal("failed to initialize kube-vim gateway", zap.Error(err))
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
		if err := gw.Start(ctx); err != nil {
			log.Fatalf("failed to start kubevim gateway server. %v", err)
		}
	}()
	wg.Wait()
	logger.Info("Exiting cleanly...")
	os.Exit(0)
}
