package http

import (
	"context"
	"fmt"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// Kind is the ImageSource member this source handles.
const Kind = "http"

// source imports images over HTTP or HTTPS.
type source struct{}

func New() *source {
	return &source{}
}

func (s *source) ImportSource(_ context.Context, src *imagev1.ImageSource) (*v1beta1.ImportSourceType, error) {
	http := src.GetHttp()
	switch {
	case http.GetBasicAuth() != nil, http.GetCredentialsRef() != "":
		return nil, fmt.Errorf("source.http credentials: %w", apperrors.ErrUnsupported)
	case http.GetCaBundle() != "":
		return nil, fmt.Errorf("source.http.caBundle: %w", apperrors.ErrUnsupported)
	}
	return &v1beta1.ImportSourceType{
		HTTP: &v1beta1.DataVolumeSourceHTTP{URL: http.GetUrl(), ExtraHeaders: http.GetExtraHeaders()},
	}, nil
}

func (s *source) Stored(src *imagev1.ImageSource) *imagev1.ImageSource {
	return &imagev1.ImageSource{Source: &imagev1.ImageSource_Http{
		Http: &imagev1.HttpSource{Url: src.GetHttp().GetUrl()},
	}}
}
