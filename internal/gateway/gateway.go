package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/gateway"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

type kubeVimGateway struct {
	logger *zap.Logger

	cfg *config.Config
}

func NewKubeVimGateway(cfg *config.Config, logger *zap.Logger) (*kubeVimGateway, error) {
	if cfg == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config", Reason: "cannot be nil"}
	}
	if logger == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "logger", Reason: "cannot be nil"}
	}
	return &kubeVimGateway{
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (g *kubeVimGateway) Start(ctx context.Context) error {
	connAddr := *g.cfg.Kubevim.Url
	opts := []grpc.DialOption{
		// Add connection backoff configuration
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.DefaultConfig,
			MinConnectTimeout: 5 * time.Second,
		}),
	}
	// TODO: Add TLS verification
	if *g.cfg.Kubevim.Tls.InsecureSkipVerify {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		return fmt.Errorf("kubevim secure connection not supported yet: %w", apperrors.ErrUnsupported)
	}

	conn, err := grpc.NewClient(connAddr, opts...)
	if err != nil {
		return fmt.Errorf("establish connection with kubevim server '%s': %w", connAddr, err)
	}
	defer conn.Close()

	// Wait for connection to be ready with timeout
	// connCtx, connCancel := context.WithTimeout(ctx, 10*time.Second)
	// defer connCancel()
	// if err := waitForConnectionReady(connCtx, conn); err != nil {
	// 	return fmt.Errorf("kubevim server '%s' not ready: %w", connAddr, err)
	// }
	g.logger.Info("successfully connected to the kubevim gRPC endpoint", zap.String("endpoint", connAddr))

	gwmux := runtime.NewServeMux(
		runtime.SetQueryParameterParser(&queryParameterParser{}),
	)
	if err = vivnfm.RegisterViVnfmHandler(ctx, gwmux, conn); err != nil {
		return fmt.Errorf("register viVnfm gateway handler: %w", err)
	}
	servAddr := fmt.Sprintf(":%d", *g.cfg.Service.Server.Port)
	server := &http.Server{
		Addr:         servAddr,
		Handler:      LogMiddlewareHandler(gwmux, g.logger),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1) // buffered channel to avoid goroutine leak
	serverCtx, serverCancel := context.WithCancel(ctx)
	defer serverCancel()

	useTLS := common.IsServerTlsConfigured(g.cfg.Service.Server.Tls)
	go func() {
		var serveErr error
		if useTLS {
			serveErr = server.ListenAndServeTLS(*g.cfg.Service.Server.Tls.Cert, *g.cfg.Service.Server.Tls.Key)
		} else {
			g.logger.Warn("No TLS configuration specified. Kubevim Gateway server will launch unsecure!")
			serveErr = server.ListenAndServe()
		}
		// Only send error if it's not due to server shutdown
		if serveErr != nil && serveErr != http.ErrServerClosed {
			if useTLS {
				errCh <- fmt.Errorf("start TLS kube-vim Gateway server on '%s': %w", servAddr, serveErr)
			} else {
				errCh <- fmt.Errorf("start kube-vim Gateway server on '%s': %w", servAddr, serveErr)
			}
		}
	}()

	g.logger.Info("kubevim gateway server successfully started", zap.String("ListeningIP", servAddr))

	// Wait for either context cancellation or a fatal server error
	select {
	case <-serverCtx.Done():
		g.logger.Info("kubevim gateway terminated due to the context cancellation", zap.Error(serverCtx.Err()))
	case err := <-errCh:
		g.logger.Error("kubevim gateway received unrecoverable error. Control loop will terminate", zap.Error(err))
		serverCancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second*2)
	defer shutdownCancel()
	if err = server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("gracefully shutdown kubevim gateway server: %w", err)
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
		if state == connectivity.TransientFailure || state == connectivity.Shutdown {
			return fmt.Errorf("connection failed with state: %s", state)
		}
		// Wait for state changes or context timeout
		if !conn.WaitForStateChange(ctx, state) {
			return fmt.Errorf("connection timeout: %w", ctx.Err())
		}
	}
}
