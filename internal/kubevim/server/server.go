package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/kubevim/server/grpc/vivnfm"
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
	imageMgr image.Manager,
	networkManager network.Manager,
	flavourManager flavour.Manager,
	computeManager compute.Manager) (*NorthboundServer, error) {
	// TODO: Add Security
	opts := []grpc.ServerOption{
		grpc.ConnectionTimeout(ConnectionTimeout),
		grpc.ChainUnaryInterceptor(
			// Error conversion interceptor (first to convert errors)
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
				return errorConversionInterceptor(ctx, req, info, handler)
			},
			// Logging interceptor (second to log converted errors)
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
				return loggingInterceptor(ctx, req, info, handler, log)
			},
		),
	}
	if cfg.Tls != nil {
		creds, err := credentials.NewServerTLSFromFile(*cfg.Tls.Cert, *cfg.Tls.Key)
		if err != nil {
			return nil, fmt.Errorf("initialize server TLS credentials from file: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	} else {
		log.Warn("No TLS configuration specified. Kubevim gRPC server will launch unsecure!")
	}
	server := grpc.NewServer(opts...)
	nfv.RegisterViVnfmServer(server, &vivnfm.ViVnfmServer{
		ImageMgr:   imageMgr,
		NetworkMgr: networkManager,
		FlavourMgr: flavourManager,
		ComputeMgr: computeManager,
	})
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

func loggingInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler, log *zap.Logger) (resp any, err error) {
	// Retrieve the client IP address
	var clientIP string
	if p, ok := peer.FromContext(ctx); ok {
		if addr, ok := p.Addr.(*net.TCPAddr); ok {
			clientIP = addr.IP.String()
		}
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
