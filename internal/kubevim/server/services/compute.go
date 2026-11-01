package services

import (
	"context"
	"fmt"

	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ComputeService struct {
	computev1.UnimplementedComputeServiceServer

	Mgr compute.Manager
}

func (s *ComputeService) CreateCompute(ctx context.Context, req *computev1.CreateComputeRequest) (*computev1.Compute, error) {
	return s.Mgr.CreateCompute(ctx, req)
}

func (s *ComputeService) GetCompute(ctx context.Context, req *computev1.GetComputeRequest) (*computev1.Compute, error) {
	return s.Mgr.GetCompute(ctx, req.GetComputeId())
}

func (s *ComputeService) ListComputes(ctx context.Context, req *computev1.ListComputesRequest) (*computev1.ListComputesResponse, error) {
	all, err := s.Mgr.ListComputes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list computes: %w", err)
	}
	res, err := filterList(all, req)
	if err != nil {
		return nil, err
	}
	return &computev1.ListComputesResponse{Computes: res}, nil
}

func (s *ComputeService) DeleteCompute(ctx context.Context, req *computev1.DeleteComputeRequest) (*emptypb.Empty, error) {
	if req.GetDeleteVolumes() {
		return nil, fmt.Errorf("delete_volumes: %w", apperrors.ErrUnsupported)
	}
	if err := s.Mgr.DeleteCompute(ctx, req.GetComputeId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
