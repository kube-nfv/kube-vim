package vivnfm

import (
	"context"
	"fmt"

	common "github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/compute"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ViVnfmServer struct {
	nfv.UnimplementedViVnfmServer

	ImageMgr   image.Manager
	FlavourMgr flavour.Manager
	NetworkMgr network.Manager
	ComputeMgr compute.Manager
}

// TODO:
//      * Convert well known errors to the gRPC errors

func (s *ViVnfmServer) QueryImages(ctx context.Context, req *nfv.QueryImagesRequest) (*nfv.QueryImagesResponse, error) {
	res, err := s.ImageMgr.GetImages(req.ImageQueryFilter)
	return &nfv.QueryImagesResponse{
		SoftwareImagesInformation: res,
	}, err
}

func (s *ViVnfmServer) QueryImage(ctx context.Context, req *nfv.QueryImageRequest) (*nfv.QueryImageResponse, error) {
	res, err := s.ImageMgr.GetImage(ctx, req.GetSoftwareImageId())
	return &nfv.QueryImageResponse{
		SoftwareImageInformation: res,
	}, err
}
func (s *ViVnfmServer) AllocateVirtualisedComputeResource(ctx context.Context, req *nfv.AllocateComputeRequest) (*nfv.AllocateComputeResponse, error) {
	res, err := s.ComputeMgr.AllocateComputeResource(ctx, req)
	return &nfv.AllocateComputeResponse{
		ComputeData: res,
	}, err
}

func (s *ViVnfmServer) CreateComputeFlavour(ctx context.Context, req *nfv.CreateComputeFlavourRequest) (*nfv.CreateComputeFlavourResponse, error) {
	res, err := s.FlavourMgr.CreateFlavour(ctx, req.Flavour)
	return &nfv.CreateComputeFlavourResponse{
		FlavourId: res,
	}, err
}

// TODO: Change this to use Filter instead of identifier
func (s *ViVnfmServer) QueryComputeFlavour(ctx context.Context, req *nfv.QueryComputeFlavourRequest) (*nfv.QueryComputeFlavourResponse, error) {
	if req.QueryComputeFlavourFilter == nil {
		return nil, fmt.Errorf("filter can't be empty: %w", common.UnsupportedErr)
	}
	res, err := s.FlavourMgr.GetFlavour(ctx, &nfv.Identifier{
		Value: req.QueryComputeFlavourFilter.Value,
	})
	return &nfv.QueryComputeFlavourResponse{
		Flavours: []*nfv.VirtualComputeFlavour{
			res,
		},
	}, err
}

func (s *ViVnfmServer) DeleteComputeFlavour(ctx context.Context, req *nfv.DeleteComputeFlavourRequest) (*nfv.DeleteComputeFlavourResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteComputeFlavour not implemented")
}

func (s *ViVnfmServer) AllocateVirtualisedNetworkResource(ctx context.Context, req *nfv.AllocateNetworkRequest) (*nfv.AllocateNetworkResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "allocateNetworkRequest can't be empty")
	}
	if req.NetworkResourceType == nil {
		return nil, status.Error(codes.InvalidArgument, "networkResourceType can't be empty")
	}
	if req.NetworkResourceName == nil || *req.NetworkResourceName == "" {
		return nil, status.Error(codes.InvalidArgument, "networkResourceName can't be empty")
	}
	switch *req.NetworkResourceType {
	case nfv.AllocateNetworkRequest_NETWORK:
		if req.TypeNetworkData == nil {
			return nil, status.Error(codes.InvalidArgument, "field typeNetworkData can't be empty with Network resource type")
		}
		net, err := s.NetworkMgr.CreateNetwork(ctx, *req.NetworkResourceName, req.TypeNetworkData)
		return &nfv.AllocateNetworkResponse{
			NetworkData: net,
		}, err
	default:
		return nil, status.Errorf(codes.Unimplemented, "unsupported NetworkResourceType: %s", req.NetworkResourceType.String())
	}
}
