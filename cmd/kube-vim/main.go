package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"go.uber.org/zap"
)

type CmdLineOpts struct {
    ip string
    port uint16
    kubeConfigFile string
    imageRegistryPath string
}


func init() {
    // Parse CmdLine flags
}

func main() {
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

