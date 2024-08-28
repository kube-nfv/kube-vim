package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/DiMalovanyy/kube-vim/internal/config"
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

    // Set Default configuration options
    viper.SetDefault("logLevel", "Info")
}

func main() {
    flag.Parse()
    viper.SetConfigFile(opts.confgPath)
    if err := viper.ReadInConfig(); err != nil {
        log.Fatalf("Can't read kube-vim configuration from path %s. Error: %v", opts.confgPath, err)
    }
    var config config.Config
    if err := viper.Unmarshal(&config); err != nil {
        log.Fatalf("Failed to parse kube-vim configuration from path %s. Error %v", opts.confgPath, err)
    }

    // Initialize the logger
    logger, err := zap.NewProduction()
    if err != nil {
        log.Fatalf("Can't initialize zap logger: %v", err)
    }
    defer logger.Sync() // Ensure all logs are flushed before the application exits
    baseLogger := logger.Sugar()

    // Create main context
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    baseLogger.Info("Installing signal handlers")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		shutdownHandler(*baseLogger.Desugar(), ctx, sigs, cancel)
		wg.Done()
	}()


    wg.Wait()
    baseLogger.Info("Exiting cleanly...")
    os.Exit(0)
}

func shutdownHandler(log zap.Logger, ctx context.Context, sigs chan os.Signal, cancel context.CancelFunc) {
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

