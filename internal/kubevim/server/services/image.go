package services

import (
	"context"
	"fmt"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
)

type ImageService struct {
	imagev1.UnimplementedImageServiceServer

	Mgr image.Manager
}

func (s *ImageService) CreateImage(ctx context.Context, req *imagev1.CreateImageRequest) (*imagev1.Image, error) {
	return s.Mgr.CreateImage(ctx, req.GetImage())
}

func (s *ImageService) GetImage(ctx context.Context, req *imagev1.GetImageRequest) (*imagev1.Image, error) {
	return s.Mgr.GetImage(ctx, req.GetImageId())
}

func (s *ImageService) ListImages(ctx context.Context, req *imagev1.ListImagesRequest) (*imagev1.ListImagesResponse, error) {
	all, err := s.Mgr.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	res, err := filterList(all, req, imageQuantityAttrs...)
	if err != nil {
		return nil, err
	}
	return &imagev1.ListImagesResponse{Images: res}, nil
}
