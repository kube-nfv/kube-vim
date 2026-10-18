package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	admin "github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/auth"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	vivnfmserver "github.com/kube-nfv/kube-vim/internal/kubevim/server/grpc/vivnfm"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
)

const (
	ConnectionTimeout = time.Second * 5
)

type NorthboundServer struct {
	server *grpc.Server

	cfg    *config.ServerConfig
	logger *zap.Logger
}

func NewNorthboundServer(
	cfg *config.ServerConfig,
	log *zap.Logger,
	meterProvider metric.MeterProvider,
	imageMgr image.Manager,
	networkManager network.Manager,
	flavourManager flavour.Manager,
	computeManager compute.Manager,
	authn auth.Authenticator) (*NorthboundServer, error) {
	unary := []grpc.UnaryServerInterceptor{
		// Error conversion interceptor (first to convert errors)
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
			return errorConversionInterceptor(ctx, req, info, handler)
		},
	}
	var stream []grpc.StreamServerInterceptor
	if authn != nil {
		// Auth before logging so requests are logged with their client
		unary = append(unary, auth.UnaryServerInterceptor(authn, log))
		stream = append(stream, streamErrorConversionInterceptor, auth.StreamServerInterceptor(authn, log))
	} else {
		log.Warn("Authentication is disabled. Kubevim gRPC server accepts unauthenticated requests!")
	}
	// Logging interceptor (last to log the handler result)
	unary = append(unary, func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		return loggingInterceptor(ctx, req, info, handler, log)
	})
	opts := []grpc.ServerOption{
		// Emits per-RPC RED metrics through meterProvider (a no-op provider when
		// monitoring is disabled). No trace exporter is configured, so spans are
		// no-ops.
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithMeterProvider(meterProvider))),
		grpc.ConnectionTimeout(ConnectionTimeout),
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	}
	if common.IsServerTlsConfigured(cfg.Tls) {
		creds, err := credentials.NewServerTLSFromFile(*cfg.Tls.Cert, *cfg.Tls.Key)
		if err != nil {
			return nil, fmt.Errorf("initialize server TLS credentials from file: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	} else {
		log.Warn("No TLS configuration specified. Kubevim gRPC server will launch unsecure!")
	}
	server := grpc.NewServer(opts...)
	vivnfm.RegisterViVnfmServer(server, &vivnfmserver.ViVnfmServer{
		ImageMgr:   imageMgr,
		NetworkMgr: networkManager,
		FlavourMgr: flavourManager,
		ComputeMgr: computeManager,
	})
	admin.RegisterAdminServer(server, imageMgr)
	reflection.Register(server)
	return &NorthboundServer{
		server: server,
		cfg:    cfg,
		logger: log,
	}, nil
}

func (s *NorthboundServer) Start(ctx context.Context) error {
	// c.cfg.Ip might be "", which is also fine
	listenAddr := fmt.Sprintf(":%d", *s.cfg.Port)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen on address '%s': %w", listenAddr, err)
	}
	wg := sync.WaitGroup{}
	wg.Add(2)
	errCh := make(chan error, 1)
	go func() {
		if err := s.server.Serve(listener); err != nil {
			errCh <- err
		}
		wg.Done()
	}()
	go func() {
		select {
		case <-ctx.Done():
			s.server.GracefulStop()
		case err = <-errCh:
		}
		wg.Done()
	}()
	s.logger.Info("northbound server successfully started", zap.String("ListeningIP", listenAddr))
	wg.Wait()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	s.logger.Warn("NorthboundServer stopped", zap.Error(err))
	return err
}

// errorConversionInterceptor converts application errors to gRPC status errors
func errorConversionInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	resp, err = handler(ctx, req)
	if err != nil {
		// Convert application errors to gRPC status errors
		if grpcErr := apperrors.ToGRPCError(err); grpcErr != nil {
			return resp, grpcErr
		}
		// If no conversion was applied, return the original error
		return resp, err
	}
	return resp, nil
}

// streamErrorConversionInterceptor converts application errors of streaming RPCs to gRPC status errors
func streamErrorConversionInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := handler(srv, ss); err != nil {
		return apperrors.ToGRPCError(err)
	}
	return nil
}

func loggingInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler, log *zap.Logger) (resp any, err error) {
	// Retrieve the client IP address
	var clientIP string
	if p, ok := peer.FromContext(ctx); ok {
		if addr, ok := p.Addr.(*net.TCPAddr); ok {
			clientIP = addr.IP.String()
		}
	}
	if p := auth.PrincipalFromContext(ctx); p != nil {
		log = log.With(zap.String("Client", p.ClientID))
	}
	log.Info("New incoming request", zap.String("Request", info.FullMethod), zap.String("IP", clientIP))
	start := time.Now()
	resp, err = handler(ctx, req)
	duration := time.Since(start)
	if err != nil {
		log.Error(
			"Request failed",
			zap.String("Request", info.FullMethod),
			zap.String("IP", clientIP),
			zap.Duration("Duration", duration),
			zap.Error(err))
	} else {
		log.Info(
			"Request completed successfully",
			zap.String("Request", info.FullMethod),
			zap.String("IP", clientIP),
			zap.Duration("Duration", duration))
	}
	return
}
