package admin

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AdminServer struct {
	admin.UnimplementedAdminServer

	ImageMgr image.Manager
}

func (s *AdminServer) DownloadImage(ctx context.Context, req *admin.DownloadImageRequest) (*admin.DownloadImageResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DownloadImage not implemented")
}

func (s *AdminServer) GetImageDownloadStatus(ctx context.Context, req *admin.GetImageDownloadStatusRequest) (*admin.GetImageDownloadStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetImageDownloadStatus not implemented")
}

func (s *AdminServer) SetupImageUploadProxy(ctx context.Context, req *admin.SetupImageUploadProxyRequest) (*admin.SetupImageUploadProxyResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SetupImageUploadProxy not implemented")
}
