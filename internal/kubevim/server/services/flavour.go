package services

import (
	"context"
	"fmt"

	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"google.golang.org/protobuf/types/known/emptypb"
)

type FlavourService struct {
	flavourv1.UnimplementedFlavourServiceServer

	Mgr flavour.Manager
}

func (s *FlavourService) CreateFlavour(ctx context.Context, req *flavourv1.CreateFlavourRequest) (*flavourv1.Flavour, error) {
	return s.Mgr.CreateFlavour(ctx, req.GetFlavour())
}

func (s *FlavourService) GetFlavour(ctx context.Context, req *flavourv1.GetFlavourRequest) (*flavourv1.Flavour, error) {
	return s.Mgr.GetFlavour(ctx, req.GetFlavourId())
}

func (s *FlavourService) ListFlavours(ctx context.Context, req *flavourv1.ListFlavoursRequest) (*flavourv1.ListFlavoursResponse, error) {
	all, err := s.Mgr.ListFlavours(ctx)
	if err != nil {
		return nil, fmt.Errorf("list flavours: %w", err)
	}
	res, err := filterList(all, req, flavourQuantityAttrs...)
	if err != nil {
		return nil, err
	}
	return &flavourv1.ListFlavoursResponse{Flavours: res}, nil
}

func (s *FlavourService) DeleteFlavour(ctx context.Context, req *flavourv1.DeleteFlavourRequest) (*emptypb.Empty, error) {
	if err := s.Mgr.DeleteFlavour(ctx, req.GetFlavourId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
