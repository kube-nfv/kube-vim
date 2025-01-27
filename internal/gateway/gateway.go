package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/gateway"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

type kubeVimGateway struct {
	logger *zap.Logger

	cfg *config.Config
}

func NewKubeVimGateway(cfg *config.Config, logger *zap.Logger) (*kubeVimGateway, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config can't be empty")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is uninitialized")
	}
	return &kubeVimGateway{
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (g *kubeVimGateway) Start(ctx context.Context) error {
	connAddr := fmt.Sprintf("%s:%d", *g.cfg.Kubevim.Ip, *g.cfg.Kubevim.Port)
	opts := []grpc.DialOption{}
	// TODO: Add TLS verification
	if *g.cfg.Kubevim.Tls.InsecureSkipVerify {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		return fmt.Errorf("kubevim secure connection is not supported yet: %w", common.UnsupportedErr)
	}
	// TODO: Add backoff if needed
	conn, err := grpc.NewClient(connAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to establish connection with kubevim server: %w", err)
	}
	defer conn.Close()
	g.logger.Info("successfully connected to the kubevim gRPC endpoint", zap.String("Endpoint", connAddr))

	gwmux := runtime.NewServeMux(
		runtime.SetQueryParameterParser(&queryParameterParser{}),
	)
	if err = nfv.RegisterViVnfmHandler(ctx, gwmux, conn); err != nil {
		return fmt.Errorf("failed to register viVnfm gateway handler: %w", err)
	}
	servAddr := fmt.Sprintf(":%d", *g.cfg.Service.Server.Port)
	server := &http.Server{
		Addr:    servAddr,
		Handler: LogMiddlewareHandler(gwmux, g.logger),
	}

	errCh := make(chan error, 1) // buffered channel to avoid goroutine leak
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		if common.IsServerTlsConfigured(g.cfg.Service.Server.Tls) {
			if err := server.ListenAndServeTLS(*g.cfg.Service.Server.Tls.Cert, *g.cfg.Service.Server.Tls.Key); err != nil {
				errCh <- fmt.Errorf("failed to start TLS kube-vim Gateway server: %w", err)
			}
		} else {
			g.logger.Warn("No TLS configuration specified. Kubevim Gateway server will launch unsecure!")
			if err := server.ListenAndServe(); err != nil {
				errCh <- fmt.Errorf("failed to start kube-vim Gateway server: %w", err)
			}
		}
	}()
	g.logger.Info("kubevim gateway server successfully started", zap.String("ListeningIP", servAddr))
	select {
	case <-ctx.Done():
		g.logger.Info("kubevim gateway terminated due to the context cancellation", zap.Error(ctx.Err()))
	case err := <-errCh:
		g.logger.Error("kubevim gateway received unrecoverable error. Control loop will terminate", zap.Error(err))
		cancel()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	if err = server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to gracefully shutdown kubevim gateway server: %w", err)
	}
	g.logger.Info("kubevim gateway server shutdown completed")
	return nil
}

func waitForConnectionReady(ctx context.Context, conn *grpc.ClientConn) error {
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		// Wait for state changes or context timeout
		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err() // Context timeout or cancellation
		}
	}
}
