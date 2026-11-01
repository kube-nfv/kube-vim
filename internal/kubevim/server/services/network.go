package services

import (
	"context"
	"fmt"

	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"google.golang.org/protobuf/types/known/emptypb"
)

type NetworkService struct {
	networkv1.UnimplementedNetworkServiceServer

	Mgr network.Manager
}

func (s *NetworkService) CreateNetwork(ctx context.Context, req *networkv1.CreateNetworkRequest) (*networkv1.Network, error) {
	return s.Mgr.CreateNetwork(ctx, req.GetNetwork())
}

func (s *NetworkService) GetNetwork(ctx context.Context, req *networkv1.GetNetworkRequest) (*networkv1.Network, error) {
	return s.Mgr.GetNetwork(ctx, req.GetNetworkId())
}

func (s *NetworkService) ListNetworks(ctx context.Context, req *networkv1.ListNetworksRequest) (*networkv1.ListNetworksResponse, error) {
	all, err := s.Mgr.ListNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	res, err := filterList(all, req)
	if err != nil {
		return nil, err
	}
	return &networkv1.ListNetworksResponse{Networks: res}, nil
}

func (s *NetworkService) DeleteNetwork(ctx context.Context, req *networkv1.DeleteNetworkRequest) (*emptypb.Empty, error) {
	if err := s.Mgr.DeleteNetwork(ctx, req.GetNetworkId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *NetworkService) GetSubnet(ctx context.Context, req *networkv1.GetSubnetRequest) (*networkv1.Subnet, error) {
	return s.Mgr.GetSubnet(ctx, req.GetSubnetId())
}

func (s *NetworkService) ListSubnets(ctx context.Context, req *networkv1.ListSubnetsRequest) (*networkv1.ListSubnetsResponse, error) {
	all, err := s.Mgr.ListSubnets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subnets: %w", err)
	}
	res, err := filterList(all, req)
	if err != nil {
		return nil, err
	}
	return &networkv1.ListSubnetsResponse{Subnets: res}, nil
}
