package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network"
	"github.com/DiMalovanyy/kube-vim/internal/server/grpc/vivnfm"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
)

const (
	ConnectionTimeout = time.Second * 5
)

type NorthboundServer struct {
	server *grpc.Server

	cfg    *config.ServiceConfig
	logger *zap.Logger
}

func NewNorthboundServer(
	cfg *config.ServiceConfig,
	log *zap.Logger,
	imageMgr image.Manager,
	networkManager network.Manager,
	flavourManager flavour.Manager) (*NorthboundServer, error) {
	// TODO: Add Security
	opts := []grpc.ServerOption{
		grpc.ConnectionTimeout(ConnectionTimeout),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
			// Retrieve the client IP address
			var clientIP string
			if p, ok := peer.FromContext(ctx); ok {
				if addr, ok := p.Addr.(*net.TCPAddr); ok {
					clientIP = addr.IP.String()
				}
			}
			log.Debug("Started request", zap.String("Request", info.FullMethod), zap.String("IP", clientIP))
			start := time.Now()
			resp, err = handler(ctx, req)
			duration := time.Since(start)
			if err != nil {
				log.Error(
					"Failed to complete request",
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
		}),
	}
	server := grpc.NewServer(opts...)
	nfv.RegisterViVnfmServer(server, &vivnfm.ViVnfmServer{
		ImageMgr:   imageMgr,
		NetworkMgr: networkManager,
		FlavourMgr: flavourManager,
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
	listenAddr := fmt.Sprintf("%s:%s", s.cfg.Ip, s.cfg.Port)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("Failed to listend address %s: %w", listenAddr, err)
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
