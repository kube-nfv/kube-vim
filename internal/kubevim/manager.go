package kubevim

import (
	"context"
	"fmt"

	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	kubevirt_compute "github.com/kube-nfv/kube-vim/internal/kubevim/compute/kubevirt"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/kubevirt"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	cdiimmage "github.com/kube-nfv/kube-vim/internal/kubevim/image/cdi"
	//"github.com/kube-nfv/kube-vim/internal/kubevim/image/glance"
	//http_im "github.com/kube-nfv/kube-vim/internal/kubevim/image/http"
	//"github.com/kube-nfv/kube-vim/internal/kubevim/image/local"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network/composite"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network/kubeovn"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network/sriov"
	"github.com/kube-nfv/kube-vim/internal/kubevim/server"
	"github.com/kube-nfv/kube-vim/internal/kubevim/telemetry"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

const (
	// k8sClientQPS/Burst raise client-go's conservative defaults (5/10) for the
	// shared cluster client; watch traffic is served by the cache, not per-call.
	k8sClientQPS   = 50
	k8sClientBurst = 100
	k8sUserAgent   = "kube-vim"
)

// Main kubevim object. It is stand as a mediator between different kubevim components like
// compute, network, storage, images, flavours, etc.
// kubevimManager also responsible for start ETSI MANO vi-vnfm, or-vi gRPC services
type kubevimManager struct {
	logger *zap.Logger
	cfg    *config.Config

	imageMgr     image.Manager
	networkMgr   network.Manager
	flavourMgr   flavour.Manager
	computeMgr   compute.Manager
	telemetryMgr *telemetry.Manager

	// cluster hosts the shared scheme, cache-backed client and APIReader used by
	// all managers. Its cache must be started (Start) and synced before serving.
	cluster cluster.Cluster

	nbServer *server.NorthboundServer
}

func NewKubeVimManager(cfg *config.Config, logger *zap.Logger) (*kubevimManager, error) {
	var err error
	if cfg == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config", Reason: "cannot be nil"}
	}

	var k8sConfig *rest.Config
	if cfg.K8s.Config != nil && *cfg.K8s.Config != "" {
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", *cfg.K8s.Config)
		if err != nil {
			return nil, fmt.Errorf("get k8s config from '%s': %w", *cfg.K8s.Config, err)
		}
	} else {
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("get k8s inClusterConfig: %w", err)
		}
	}
	// Tune the shared config. Note: no global rest.Config.Timeout is set; it would
	// cap the cache's long-lived watch connections. Per-call deadlines are used instead.
	k8sConfig.QPS = k8sClientQPS
	k8sConfig.Burst = k8sClientBurst
	k8sConfig.UserAgent = k8sUserAgent

	scheme, err := k8s.BuildScheme()
	if err != nil {
		return nil, fmt.Errorf("build k8s scheme: %w", err)
	}
	if cfg.K8s == nil || cfg.K8s.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "k8s.namespace", Reason: "cannot be nil"}
	}
	cl, err := k8s.NewCluster(k8sConfig, *cfg.K8s.Namespace, scheme)
	if err != nil {
		return nil, fmt.Errorf("build shared k8s cluster: %w", err)
	}

	mgr := &kubevimManager{
		logger:  logger,
		cfg:     cfg,
		cluster: cl,
	}
	if err := mgr.initImageManager(cfg.Image, cfg.K8s); err != nil {
		return nil, fmt.Errorf("configure image manager: %w", err)
	}
	if err := mgr.initNetworkManager(cfg.K8s); err != nil {
		return nil, fmt.Errorf("initialize network manager: %w", err)
	}
	if err := mgr.initFlavourManager(cfg.K8s); err != nil {
		return nil, fmt.Errorf("initialize flavour manager: %w", err)
	}
	if err := mgr.initComputeManager(cfg.K8s, cfg.Compute); err != nil {
		return nil, fmt.Errorf("initialize compute manager: %w", err)
	}
	if err := mgr.initTelemetryManager(cfg.Monitoring); err != nil {
		return nil, fmt.Errorf("initialize telemetry manager: %w", err)
	}
	if err := mgr.initNorthboundServer(cfg.Service.Server); err != nil {
		return nil, fmt.Errorf("configure northbound server: %w", err)
	}
	return mgr, nil
}

func (m *kubevimManager) Start(ctx context.Context) {
	errCh := make(chan error, 1) // Buffered to prevent goroutine leaks
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start the shared cache and block until it has synced before serving, so
	// managers never read from a cold cache.
	go func() {
		if err := m.cluster.Start(ctx); err != nil {
			errCh <- fmt.Errorf("start k8s cluster cache: %w", err)
		}
	}()
	if !m.cluster.GetCache().WaitForCacheSync(ctx) {
		m.logger.Error("k8s cache failed to sync; kube-vim manager will terminate")
		return
	}

	if m.cfg != nil && m.cfg.Network != nil {
		if err := m.networkMgr.EnsureManagementNetwork(ctx, m.cfg.Network.ManagementNetwork); err != nil {
			m.logger.Error("Failed to ensure kube-vim-managed management network", zap.Error(err))
			return
		}
	}

	go func() {
		if err := m.nbServer.Start(ctx); err != nil {
			errCh <- fmt.Errorf("start Northbound server: %w", err)
		}
	}()
	go func() {
		if err := m.telemetryMgr.Start(ctx); err != nil {
			errCh <- fmt.Errorf("start telemetry server: %w", err)
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

func (m *kubevimManager) initImageManager(cfg *config.ImageConfig, k8sCfg *config.K8sConfig) error {
	if cfg == nil {
		return &apperrors.ErrInvalidArgument{Field: "imageConfig", Reason: "cannot be nil"}
	}
	var err error
	m.imageMgr, err = cdiimmage.NewCDIImageManager(m.cluster.GetClient(), m.cluster.GetAPIReader(), cfg, k8sCfg)
	if err != nil {
		return fmt.Errorf("initialize kubevirt cdi image manager: %w", err)
	}
	return nil
	/*
		cdiCtrl, err := image.NewCdiController(k8sConfig)
		if err != nil {
			return fmt.Errorf("initialize kubevirt cdi controller: %w", err)
		}
		if cfg.Http != nil {
			m.imageMgr, err = http_im.NewHttpImageManager(cdiCtrl, cfg.Http)
			if err != nil {
				return fmt.Errorf("initialize HTTP image manager: %w", err)
			}
			return nil
		}
		if cfg.Local != nil {
			m.imageMgr, err = local.NewLocalImageManager(cfg.Local)
			if err != nil {
				return fmt.Errorf("initialize Local image manager: %w", err)
			}
			return nil
		}
		if cfg.Glance != nil {
			m.imageMgr, err = glance.NewGlanceImageManager(cfg.Glance)
			if err != nil {
				return fmt.Errorf("initialize Glance image manager: %w", err)
			}
			return nil
		}
		return &apperrors.ErrInvalidArgument{Field: "imageConfig", Reason: "no valid image manager configuration found (http, local, or glance required)"}
	*/
}

func (m *kubevimManager) initNetworkManager(k8sCfg *config.K8sConfig) error {
	ovnMgr, err := kubeovn.NewKubeovnNetworkManager(m.cluster.GetClient(), m.cluster.GetAPIReader(), k8sCfg, m.logger.Named("network.ovn"))
	if err != nil {
		return fmt.Errorf("create kubeovn network manager: %w", err)
	}
	var sriovCfg *config.SriovNetworkConfig
	if m.cfg.Network != nil {
		sriovCfg = m.cfg.Network.Sriov
	}
	sriovMgr, err := sriov.NewSriovNetworkManager(m.cluster.GetClient(), k8sCfg, sriovCfg, m.logger.Named("network.sriov"))
	if err != nil {
		return fmt.Errorf("create sriov network manager: %w", err)
	}
	m.networkMgr = composite.NewManager(ovnMgr, sriovMgr)
	return nil
}

func (m *kubevimManager) initFlavourManager(cfg *config.K8sConfig) error {
	var err error
	m.flavourMgr, err = kubevirt_flavour.NewFlavourManager(m.cluster.GetClient(), m.cluster.GetAPIReader(), cfg)
	if err != nil {
		return fmt.Errorf("create kubevirt flavour manager: %w", err)
	}
	return nil
}

func (m *kubevimManager) initComputeManager(cfg *config.K8sConfig, computeCfg *config.ComputeConfig) error {
	var err error
	m.computeMgr, err = kubevirt_compute.NewComputeManager(m.cluster.GetClient(), m.cluster.GetAPIReader(), cfg, computeCfg, m.flavourMgr, m.imageMgr, m.networkMgr)
	if err != nil {
		return fmt.Errorf("create kubevirt compute manager: %w", err)
	}
	return nil
}

func (m *kubevimManager) initTelemetryManager(cfg *config.MonitoringConfig) error {
	var err error
	m.telemetryMgr, err = telemetry.NewManager(cfg, m.logger.Named("Telemetry"), m.computeMgr, m.networkMgr)
	if err != nil {
		return fmt.Errorf("create telemetry manager: %w", err)
	}
	return nil
}

func (m *kubevimManager) initNorthboundServer(cfg *config.ServerConfig) error {
	if cfg == nil {
		return &apperrors.ErrInvalidArgument{Field: "ServiceConfig", Reason: "cannot be nil"}
	}
	var err error
	m.nbServer, err = server.NewNorthboundServer(cfg, m.logger.Named("NorthboundServer"), m.telemetryMgr.MeterProvider(), m.imageMgr, m.networkMgr, m.flavourMgr, m.computeMgr)
	if err != nil {
		return fmt.Errorf("initialize NorthboundServer: %w", err)
	}
	return nil
}
