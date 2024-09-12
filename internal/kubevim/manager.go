package kubevim

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image/glance"
	"github.com/DiMalovanyy/kube-vim/internal/server"
	"go.uber.org/zap"
)

// Main kubevim object. It is stand as a mediator between different kubevim components like
// compute, network, storage, images, flavours, etc.
// kubevimManager also responsible for start ETSI MANO vi-vnfm, or-vi gRPC services
type kubevimManager struct {
    logger *zap.Logger

    imageMgr image.Manager
    nbServer *server.NorthboundServer
}

func NewKubeVimManager(cfg *config.Config, logger *zap.Logger) (*kubevimManager, error) {
    if cfg == nil {
        return nil, fmt.Errorf("Config can't be empty")
    }
    mgr := &kubevimManager{
        logger: logger,
    }
    if err := mgr.initImageManager(cfg.Image); err != nil {
        return nil, fmt.Errorf("Failed to configure image manager: %w", err)
    }
    if err := mgr.initNorthboundServer(cfg.Service); err != nil {
        return nil, fmt.Errorf("Failed to configure northbound server: %w", err)
    }
    return nil, nil
}

func (m *kubevimManager) Start(ctx context.Context) {
    errCh := make(chan error, 1) // Buffered to prevent goroutine leaks
    ctx, cancel := context.WithCancel(ctx)
    defer cancel()
    go func() {
        if err := m.nbServer.Start(ctx); err != nil {
            errCh <- fmt.Errorf("Failed to start Northbound server: %w", err)
        }
    }()
    go func() {
        err := <-errCh
        m.logger.Error("Kubevim manager received unrecoverable error. Control loop will terminate", zap.Error(err))
        cancel()
    }()
    select {
    case <-ctx.Done():
        m.logger.Info("Kubevim manager terminated due to context cancellation", zap.Error(ctx.Err()))
    case err := <-errCh:
        m.logger.Error("Kubevim manager stopped due to a server error", zap.Error(err))
    }
    m.logger.Info("Kubevim manager shutdown completed")
}

func (m *kubevimManager) initImageManager(cfg *config.ImageConfig) error {
    if cfg == nil {
        return fmt.Errorf("ImageConfig can't be empty")
    }
    if cfg.Glance != nil {
        var err error
        m.imageMgr, err = glance.NewGlanceImageManager(cfg.Glance)
        if err != nil {
            fmt.Errorf("Failed to initialize Glance image manager: %w", err)
        }
        return nil
    } else {
        return fmt.Errorf("Can't find propper image manager configuration")
    }
}

func (m *kubevimManager) initNorthboundServer(cfg *config.ServiceConfig) error {
    if cfg == nil {
        return fmt.Errorf("ServiceConfig can't be empty")
    }
    var err error
    m.nbServer, err = server.NewNorthboundServer(cfg, m.logger.Named("NorthboundServer"), m.imageMgr)
    if err != nil {
        return fmt.Errorf("Failed to initialize NorthboundServer: %w", err)
    }
    return nil
}
