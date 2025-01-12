package kubevim

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config/kubevim"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/compute"
	kubevirt_compute "github.com/DiMalovanyy/kube-vim/internal/kubevim/compute/kubevirt"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour/kubevirt"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image/glance"
	http_im "github.com/DiMalovanyy/kube-vim/internal/kubevim/image/http"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image/local"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network/kubeovn"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/server"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Main kubevim object. It is stand as a mediator between different kubevim components like
// compute, network, storage, images, flavours, etc.
// kubevimManager also responsible for start ETSI MANO vi-vnfm, or-vi gRPC services
type kubevimManager struct {
	logger *zap.Logger

	imageMgr   image.Manager
	networkMgr network.Manager
	flavourMgr flavour.Manager
	computeMgr compute.Manager

	nbServer *server.NorthboundServer
}

func NewKubeVimManager(cfg *config.Config, logger *zap.Logger) (*kubevimManager, error) {
	var err error
	if cfg == nil {
		return nil, fmt.Errorf("Config can't be empty")
	}

	var k8sConfig *rest.Config
	if cfg.K8s.Config != nil && *cfg.K8s.Config != "" {
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", *cfg.K8s.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config from %s: %w", *cfg.K8s.Config, err)
		}
	} else {
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("faile to get k8s inClusterConfig: %w", err)
		}
	}

	mgr := &kubevimManager{
		logger: logger,
	}
	if err := mgr.initImageManager(k8sConfig, cfg.Image); err != nil {
		return nil, fmt.Errorf("Failed to configure image manager: %w", err)
	}
	if err := mgr.initNetworkManager(k8sConfig); err != nil {
		return nil, fmt.Errorf("failed to initialize network manager: %w", err)
	}
	if err := mgr.initFlavourManager(k8sConfig, cfg.K8s); err != nil {
		return nil, fmt.Errorf("failed to initialize flavour manager: %w", err)
	}
	if err := mgr.initComputeManager(k8sConfig, cfg.K8s); err != nil {
		return nil, fmt.Errorf("failed to initialize compute manager: %w", err)
	}
	if err := mgr.initNorthboundServer(cfg.Service.Server); err != nil {
		return nil, fmt.Errorf("Failed to configure northbound server: %w", err)
	}
	return mgr, nil
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
		select {
		case err := <-errCh:
			m.logger.Error("Kubevim manager received unrecoverable error. Control loop will terminate", zap.Error(err))
			cancel()
		}
	}()
	select {
	case <-ctx.Done():
		m.logger.Info("Kubevim manager terminated due to context cancellation", zap.Error(ctx.Err()))
	case err := <-errCh:
		m.logger.Error("Kubevim manager stopped due to a server error", zap.Error(err))
	}
	m.logger.Info("Kubevim manager shutdown completed")
}

func (m *kubevimManager) initImageManager(k8sConfig *rest.Config, cfg *config.ImageConfig) error {
	if cfg == nil {
		return fmt.Errorf("imageConfig can't be empty")
	}
	cdiCtrl, err := image.NewCdiController(k8sConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize kubevirt cdi controller: %w", err)
	}
	if cfg.Http != nil {
		var err error
		m.imageMgr, err = http_im.NewHttpImageManager(cdiCtrl, cfg.Http)
		if err != nil {
			return fmt.Errorf("failed to initialize Htpp image manager: %w", err)
		}
		return nil
	}
	if cfg.Local != nil {
		var err error
		m.imageMgr, err = local.NewLocalImageManager(cfg.Local)
		if err != nil {
			return fmt.Errorf("failed to initialize Local image manager: %w", err)
		}
		return nil
	}
	if cfg.Glance != nil {
		var err error
		m.imageMgr, err = glance.NewGlanceImageManager(cfg.Glance)
		if err != nil {
			return fmt.Errorf("failed to initialize Glance image manager: %w", err)
		}
		return nil
	}
	return fmt.Errorf("can't find propper image manager configuration")
}

func (m *kubevimManager) initNetworkManager(k8sConfig *rest.Config) error {
	if k8sConfig == nil {
		return fmt.Errorf("k8sConfig can't be empty")
	}
	var err error
	m.networkMgr, err = kubeovn.NewKubeovnNetworkManager(k8sConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubeovn network manager: %w", err)
	}
	return nil
}

func (m *kubevimManager) initFlavourManager(k8sConfig *rest.Config, cfg *config.K8sConfig) error {
	if k8sConfig == nil {
		return fmt.Errorf("k8sConfig can't be empty")
	}
	var err error
	m.flavourMgr, err = kubevirt_flavour.NewFlavourManager(k8sConfig, cfg)
	if err != nil {
		return fmt.Errorf("failed to create kubevirt flavour manager: %w", err)
	}
	return nil
}

func (m *kubevimManager) initComputeManager(k8sConfig *rest.Config, cfg *config.K8sConfig) error {
	if k8sConfig == nil {
		return fmt.Errorf("k8sConfig can't be empty")
	}
	var err error
	m.computeMgr, err = kubevirt_compute.NewComputeManager(k8sConfig, cfg, m.flavourMgr, m.imageMgr, m.networkMgr)
	if err != nil {
		return fmt.Errorf("failed to create kubevirt compute manager: %w", err)
	}
	return nil
}

func (m *kubevimManager) initNorthboundServer(cfg *config.ServerConfig) error {
	if cfg == nil {
		return fmt.Errorf("ServiceConfig can't be empty")
	}
	var err error
	m.nbServer, err = server.NewNorthboundServer(cfg, m.logger.Named("NorthboundServer"), m.imageMgr, m.networkMgr, m.flavourMgr, m.computeMgr)
	if err != nil {
		return fmt.Errorf("Failed to initialize NorthboundServer: %w", err)
	}
	return nil
}

func initMgmtNetwork(netMgr network.Manager) error {

	return nil
}
