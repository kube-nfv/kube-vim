package vivnfm

import (
	"context"

	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ViVnfmServer struct {
	nfv.UnimplementedViVnfmServer

	ImageMgr image.Manager
}

func (s *ViVnfmServer) QueryImages(ctx context.Context, req *nfv.QueryImagesRequest) (*nfv.QueryImagesResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method QueryImages not implemented")
}
func (s *ViVnfmServer) QueryImage(ctx context.Context, req *nfv.QueryImageRequest) (*nfv.QueryImageResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method QueryImage not implemented")
}
func (s *ViVnfmServer) AllocateVirtualisedComputeResource(ctx context.Context, req *nfv.AllocateComputeRequest) (*nfv.AllocateComputeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AllocateVirtualisedComputeResource not implemented")
}
func (s *ViVnfmServer) CreateComputeFlavour(ctx context.Context, req *nfv.CreateComputeFlavourRequest) (*nfv.CreateComputeFlavourResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CreateComputeFlavour not implemented")
}
func (s *ViVnfmServer) QueryComputeFlavour(ctx context.Context, req *nfv.QueryComputeFlavourRequest) (*nfv.QueryComputeFlavourResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method QueryComputeFlavour not implemented")
}
func (s *ViVnfmServer) DeleteComputeFlavour(ctx context.Context, req *nfv.DeleteComputeFlavourRequest) (*nfv.DeleteComputeFlavourResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteComputeFlavour not implemented")
}
func (s *ViVnfmServer) AllocateVirtualisedNetworkResource(ctx context.Context, req *nfv.AllocateNetworkRequest) (*nfv.AllocateNetworkResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AllocateVirtualisedNetworkResource not implemented")
}
