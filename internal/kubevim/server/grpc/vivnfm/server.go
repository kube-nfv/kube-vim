package vivnfm

import (
	"context"
	"errors"
	"fmt"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	filter "github.com/kube-nfv/query-filter"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
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
	res, err := s.ImageMgr.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query images: %w", err)
	}
	filtered, err := filter.FilterList(res, req.ImageQueryFilter.GetValue())
	if err != nil {
		return nil, fmt.Errorf("failed to filter queried images: %w", err)
	}
	return &nfv.QueryImagesResponse{
		SoftwareImagesInformation: filtered,
	}, nil
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

func (s *ViVnfmServer) QueryVirtualisedComputeResource(ctx context.Context, req *nfv.QueryComputeRequest) (*nfv.QueryComputeResponse, error) {
	res, err := s.ComputeMgr.ListComputeResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query compute resources: %w", err)
	}
	filtered, err := filter.FilterList(res, req.QueryComputeFilter.GetValue())
	if err != nil {
		return nil, fmt.Errorf("failed to filter queried compute resources: %w", err)
	}
	return &nfv.QueryComputeResponse{
		QueryResult: filtered,
	}, nil
}

func (s *ViVnfmServer) TerminateVirtualisedComputeResource(ctx context.Context, req *nfv.TerminateComputeRequest) (*nfv.TerminateComputeResponse, error) {
	err := s.ComputeMgr.DeleteComputeResource(ctx, compute.GetComputeByUid(req.GetComputeId()))
	if err != nil {
		return nil, fmt.Errorf("failed to delete virtualised compute resource with id \"%s\": %w", req.ComputeId.GetValue(), err)
	}
	return &nfv.TerminateComputeResponse{
		ComputeId: req.ComputeId,
	}, nil
}

func (s *ViVnfmServer) CreateComputeFlavour(ctx context.Context, req *nfv.CreateComputeFlavourRequest) (*nfv.CreateComputeFlavourResponse, error) {
	res, err := s.FlavourMgr.CreateFlavour(ctx, req.Flavour)
	return &nfv.CreateComputeFlavourResponse{
		FlavourId: res,
	}, err
}

// TODO: Change this to use Filter instead of identifier
func (s *ViVnfmServer) QueryComputeFlavour(ctx context.Context, req *nfv.QueryComputeFlavourRequest) (*nfv.QueryComputeFlavourResponse, error) {
	res, err := s.FlavourMgr.GetFlavours(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavours: %w", err)
	}
	filtered, err := filter.FilterList(res, req.QueryComputeFlavourFilter.GetValue())
	if err != nil {
		return nil, fmt.Errorf("failed to filter queried flavours: %w", err)
	}
	return &nfv.QueryComputeFlavourResponse{
		Flavours: filtered,
	}, nil
}

func (s *ViVnfmServer) DeleteComputeFlavour(ctx context.Context, req *nfv.DeleteComputeFlavourRequest) (*nfv.DeleteComputeFlavourResponse, error) {
	if err := s.FlavourMgr.DeleteFlavour(ctx, req.ComputeFlavourId); err != nil {
		return nil, fmt.Errorf("failed to delete flavour with id \"%s\": %w", req.ComputeFlavourId.Value, err)
	}
	return &nfv.DeleteComputeFlavourResponse{}, nil
}

func (s *ViVnfmServer) AllocateVirtualisedNetworkResource(ctx context.Context, req *nfv.AllocateNetworkRequest) (*nfv.AllocateNetworkResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "allocateNetworkRequest can't be empty")
	}
	if req.NetworkResourceName == nil || *req.NetworkResourceName == "" {
		return nil, status.Error(codes.InvalidArgument, "networkResourceName can't be empty")
	}
	switch req.NetworkResourceType {
	case nfv.NetworkResourceType_NETWORK:
		if req.TypeNetworkData == nil {
			return nil, status.Error(codes.InvalidArgument, "field typeNetworkData can't be empty with Network resource type")
		}
		net, err := s.NetworkMgr.CreateNetwork(ctx, *req.NetworkResourceName, req.TypeNetworkData)
		return &nfv.AllocateNetworkResponse{
			NetworkData: net,
		}, err
	case nfv.NetworkResourceType_SUBNET:
		if req.TypeSubnetData == nil {
			return nil, status.Error(codes.InvalidArgument, "field TypeSubnetData can't be empty with Subnet resource type")
		}
		subnet, err := s.NetworkMgr.CreateSubnet(ctx, *req.NetworkResourceName, req.TypeSubnetData)
		return &nfv.AllocateNetworkResponse{
			SubnetData: subnet,
		}, err
	default:
		return nil, status.Errorf(codes.Unimplemented, "unsupported NetworkResourceType: %s", req.NetworkResourceType.String())
	}
}
func (s *ViVnfmServer) QueryVirtualisedNetworkResource(ctx context.Context, req *nfv.QueryNetworkRequest) (*nfv.QueryNetworkResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "queryNetworkRequest can't be empty")
	}
	switch req.NetworkResourceType {
	case nfv.NetworkResourceType_NETWORK:
		netLst, err := s.NetworkMgr.ListNetworks(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list networks: %w", err)
		}
		filtered, err := filter.FilterList(netLst, req.QueryNetworkFilter.GetValue())
		if err != nil {
			return nil, fmt.Errorf("failed to filter networks: %w", err)
		}
		return &nfv.QueryNetworkResponse{
			QueryNetworkResult: filtered,
		}, nil
	case nfv.NetworkResourceType_SUBNET:
		subLst, err := s.NetworkMgr.ListSubnets(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subnets: %w", err)
		}
		filtered, err := filter.FilterList(subLst, req.QueryNetworkFilter.GetValue())
		if err != nil {
			return nil, fmt.Errorf("failed to fileter subnets: %w", err)
		}
		return &nfv.QueryNetworkResponse{
			QuerySubnetResult: filtered,
		}, nil
	default:
		return nil, status.Errorf(codes.Unimplemented, "unsupported NetworkResourceType: %s", req.NetworkResourceType.String())
	}
}

func (s *ViVnfmServer) TerminateVirtualisedNetworkResource(ctx context.Context, req *nfv.TerminateNetworkRequest) (*nfv.TerminateNetworkResponse, error) {
	err := s.NetworkMgr.DeleteNetwork(ctx, network.GetNetworkByUid(req.NetworkResourceId))
	if err == nil {
		return &nfv.TerminateNetworkResponse{
			NetworkResourceId: req.NetworkResourceId,
		}, nil
	}
	if !errors.Is(err, common.NotFoundErr) && !k8s_errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to delete network with id \"%s\": %w", req.NetworkResourceId.GetValue(), err)
	}
	err = s.NetworkMgr.DeleteSubnet(ctx, network.GetSubnetByUid(req.NetworkResourceId))
	if err == nil {
		return &nfv.TerminateNetworkResponse{
			NetworkResourceId: req.NetworkResourceId,
		}, nil
	}
	if errors.Is(err, common.NotFoundErr) || k8s_errors.IsNotFound(err) {
		return nil, fmt.Errorf("network resource with id \"%s\" not match either network nor subnet: %w", req.NetworkResourceId.GetValue(), err)
	} else {
		return nil, fmt.Errorf("failed to delete subent with id \"%s\": %w", req.NetworkResourceId.GetValue(), err)
	}
}
