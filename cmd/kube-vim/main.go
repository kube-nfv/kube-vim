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

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type CmdLineOpts struct {
	confgPath string
}

var (
	opts CmdLineOpts
)

func init() {
	// Parse CmdLine flags
	flag.StringVar(&opts.confgPath, "config", "/etc/kube-vim/config.yaml", "kube-vim configuration file path")
}

func main() {
	flag.Parse()
	viper.SetConfigFile(opts.confgPath)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Can't read kube-vim configuration from path %s. Error: %v", opts.confgPath, err)
	}
	config.InitDefaultAfterReading()
	var config config.Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Failed to parse kube-vim configuration from path %s. Error %v", opts.confgPath, err)
	}

	// Initialize the logger
	// TODO(dmalovan): Add log level configuration based on the config value
	baseLoggerCfg := zap.NewProductionConfig()
	baseLoggerCfg.DisableStacktrace = true
	logger, err := baseLoggerCfg.Build()
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer logger.Sync() // Ensure all logs are flushed before the application exits

	cfgStr, err := json.Marshal(config)
	if err == nil {
		logger.Info("", zap.String("config", string(cfgStr)))
	}

	mgr, err := kubevim.NewKubeVimManager(&config, logger.Named("Kubevim"))
	if err != nil {
		log.Fatalf("Can't create kubevim manager: %v", err)
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
		shutdownHandler(logger, ctx, sigs, cancel)
		wg.Done()
	}()
	go func() {
		mgr.Start(ctx)
		wg.Done()
	}()

	wg.Wait()
	logger.Info("Exiting cleanly...")
	os.Exit(0)
}

func shutdownHandler(log *zap.Logger, ctx context.Context, sigs chan os.Signal, cancel context.CancelFunc) {
	// Wait for the context do be Done or for the signal to come in to shutdown.
	select {
	case <-ctx.Done():
		log.Info("Stopping shutdownHandler...")
	case <-sigs:
		// Call cancel on the context to close everything down.
		cancel()
		log.Info("shutdownHandler sent cancel signal...")
	}

	// Unregister to get default OS nuke behaviour in case we don't exit cleanly
	signal.Stop(sigs)
}
