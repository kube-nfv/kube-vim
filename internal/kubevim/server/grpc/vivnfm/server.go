package vivnfm

import (
	"context"
	"errors"
	"fmt"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
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
	vivnfm.UnimplementedViVnfmServer

	ImageMgr   image.Manager
	FlavourMgr flavour.Manager
	NetworkMgr network.Manager
	ComputeMgr compute.Manager
}

func (s *ViVnfmServer) QueryImages(ctx context.Context, req *vivnfm.QueryImagesRequest) (*vivnfm.QueryImagesResponse, error) {
	res, err := s.ImageMgr.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("query images: %w", err)
	}
	filtered, err := filter.FilterList(res, req.ImageQueryFilter.GetValue())
	if err != nil {
		return nil, fmt.Errorf("filter queried images: %w", err)
	}
	return &vivnfm.QueryImagesResponse{
		SoftwareImagesInformation: filtered,
	}, nil
}

func (s *ViVnfmServer) QueryImage(ctx context.Context, req *vivnfm.QueryImageRequest) (*vivnfm.QueryImageResponse, error) {
	res, err := s.ImageMgr.GetImage(ctx, req.GetSoftwareImageId())
	return &vivnfm.QueryImageResponse{
		SoftwareImageInformation: res,
	}, err
}

func (s *ViVnfmServer) AllocateVirtualisedComputeResource(ctx context.Context, req *vivnfm.AllocateComputeRequest) (*vivnfm.AllocateComputeResponse, error) {
	res, err := s.ComputeMgr.AllocateComputeResource(ctx, req)
	return &vivnfm.AllocateComputeResponse{
		ComputeData: res,
	}, err
}

func (s *ViVnfmServer) QueryVirtualisedComputeResource(ctx context.Context, req *vivnfm.QueryComputeRequest) (*vivnfm.QueryComputeResponse, error) {
	res, err := s.ComputeMgr.ListComputeResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("query compute resources: %w", err)
	}
	filtered, err := filter.FilterList(res, req.QueryComputeFilter.GetValue())
	if err != nil {
		return nil, fmt.Errorf("filter queried compute resources: %w", err)
	}
	return &vivnfm.QueryComputeResponse{
		QueryResult: filtered,
	}, nil
}

func (s *ViVnfmServer) TerminateVirtualisedComputeResource(ctx context.Context, req *vivnfm.TerminateComputeRequest) (*vivnfm.TerminateComputeResponse, error) {
	err := s.ComputeMgr.DeleteComputeResource(ctx, compute.GetComputeByUid(req.GetComputeId()))
	if err != nil {
		return nil, fmt.Errorf("delete virtualised compute resource '%s': %w", req.ComputeId.GetValue(), err)
	}
	return &vivnfm.TerminateComputeResponse{
		ComputeId: req.ComputeId,
	}, nil
}

func (s *ViVnfmServer) CreateComputeFlavour(ctx context.Context, req *vivnfm.CreateComputeFlavourRequest) (*vivnfm.CreateComputeFlavourResponse, error) {
	res, err := s.FlavourMgr.CreateFlavour(ctx, req.Flavour)
	return &vivnfm.CreateComputeFlavourResponse{
		FlavourId: res,
	}, err
}

// TODO: Change this to use Filter instead of identifier
func (s *ViVnfmServer) QueryComputeFlavour(ctx context.Context, req *vivnfm.QueryComputeFlavourRequest) (*vivnfm.QueryComputeFlavourResponse, error) {
	res, err := s.FlavourMgr.GetFlavours(ctx)
	if err != nil {
		return nil, fmt.Errorf("get flavours: %w", err)
	}
	filtered, err := filter.FilterList(res, req.QueryComputeFlavourFilter.GetValue())
	if err != nil {
		return nil, fmt.Errorf("filter queried flavours: %w", err)
	}
	return &vivnfm.QueryComputeFlavourResponse{
		Flavours: filtered,
	}, nil
}

func (s *ViVnfmServer) DeleteComputeFlavour(ctx context.Context, req *vivnfm.DeleteComputeFlavourRequest) (*vivnfm.DeleteComputeFlavourResponse, error) {
	if err := s.FlavourMgr.DeleteFlavour(ctx, req.ComputeFlavourId); err != nil {
		return nil, fmt.Errorf("delete flavour '%s': %w", req.ComputeFlavourId.Value, err)
	}
	return &vivnfm.DeleteComputeFlavourResponse{}, nil
}

func (s *ViVnfmServer) AllocateVirtualisedNetworkResource(ctx context.Context, req *vivnfm.AllocateNetworkRequest) (*vivnfm.AllocateNetworkResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "allocateNetworkRequest can't be empty")
	}
	if req.NetworkResourceName == nil || *req.NetworkResourceName == "" {
		return nil, status.Error(codes.InvalidArgument, "networkResourceName can't be empty")
	}
	switch req.NetworkResourceType {
	case nfvcommon.NetworkResourceType_NETWORK:
		if req.TypeNetworkData == nil {
			return nil, status.Error(codes.InvalidArgument, "field typeNetworkData can't be empty with Network resource type")
		}
		net, err := s.NetworkMgr.CreateNetwork(ctx, *req.NetworkResourceName, req.TypeNetworkData)
		return &vivnfm.AllocateNetworkResponse{
			NetworkData: net,
		}, err
	case nfvcommon.NetworkResourceType_SUBNET:
		if req.TypeSubnetData == nil {
			return nil, status.Error(codes.InvalidArgument, "field TypeSubnetData can't be empty with Subnet resource type")
		}
		subnet, err := s.NetworkMgr.CreateSubnet(ctx, *req.NetworkResourceName, req.TypeSubnetData)
		return &vivnfm.AllocateNetworkResponse{
			SubnetData: subnet,
		}, err
	default:
		return nil, status.Errorf(codes.Unimplemented, "unsupported NetworkResourceType: %s", req.NetworkResourceType.String())
	}
}
func (s *ViVnfmServer) QueryVirtualisedNetworkResource(ctx context.Context, req *vivnfm.QueryNetworkRequest) (*vivnfm.QueryNetworkResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "queryNetworkRequest can't be empty")
	}
	switch req.NetworkResourceType {
	case nfvcommon.NetworkResourceType_NETWORK:
		netLst, err := s.NetworkMgr.ListNetworks(ctx)
		if err != nil {
			return nil, fmt.Errorf("list networks: %w", err)
		}
		filtered, err := filter.FilterList(netLst, req.QueryNetworkFilter.GetValue())
		if err != nil {
			return nil, fmt.Errorf("filter networks: %w", err)
		}
		return &vivnfm.QueryNetworkResponse{
			QueryNetworkResult: filtered,
		}, nil
	case nfvcommon.NetworkResourceType_SUBNET:
		subLst, err := s.NetworkMgr.ListSubnets(ctx)
		if err != nil {
			return nil, fmt.Errorf("list subnets: %w", err)
		}
		filtered, err := filter.FilterList(subLst, req.QueryNetworkFilter.GetValue())
		if err != nil {
			return nil, fmt.Errorf("filter subnets: %w", err)
		}
		return &vivnfm.QueryNetworkResponse{
			QuerySubnetResult: filtered,
		}, nil
	default:
		return nil, status.Errorf(codes.Unimplemented, "unsupported NetworkResourceType: %s", req.NetworkResourceType.String())
	}
}

func (s *ViVnfmServer) TerminateVirtualisedNetworkResource(ctx context.Context, req *vivnfm.TerminateNetworkRequest) (*vivnfm.TerminateNetworkResponse, error) {
	err := s.NetworkMgr.DeleteNetwork(ctx, network.GetNetworkByUid(req.NetworkResourceId))
	if err == nil {
		return &vivnfm.TerminateNetworkResponse{
			NetworkResourceId: req.NetworkResourceId,
		}, nil
	}
	var networkNotFoundErr *apperrors.ErrNotFound
	if !errors.As(err, &networkNotFoundErr) && !k8s_errors.IsNotFound(err) {
		return nil, fmt.Errorf("delete network '%s': %w", req.NetworkResourceId.GetValue(), err)
	}
	err = s.NetworkMgr.DeleteSubnet(ctx, network.GetSubnetByUid(req.NetworkResourceId))
	if err == nil {
		return &vivnfm.TerminateNetworkResponse{
			NetworkResourceId: req.NetworkResourceId,
		}, nil
	}
	var subnetNotFoundErr *apperrors.ErrNotFound
	if errors.As(err, &subnetNotFoundErr) || k8s_errors.IsNotFound(err) {
		return nil, fmt.Errorf("network resource '%s' not found in networks or subnets: %w", req.NetworkResourceId.GetValue(), err)
	} else {
		return nil, fmt.Errorf("delete subnet '%s': %w", req.NetworkResourceId.GetValue(), err)
	}
}
