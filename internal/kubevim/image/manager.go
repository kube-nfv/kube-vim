package image

import (
	"context"
	"fmt"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
)

const (
	K8sImageIdLabel  = "image.kubevim.kubenfv.io/id"
	K8sSourceLabel   = "image.kubevim.kubenfv.io/source"
	K8sIsUploadLabel = "image.kubevim.kubenfv.io/uploaded"

	K8sSourceUrlAnnotation = "image.kubevim.kubenfv.io/source-url"
)

type Manager interface {
	GetImage(context.Context, *nfvcommon.Identifier) (*vivnfm.SoftwareImageInformation, error)
	ListImages(context.Context) ([]*vivnfm.SoftwareImageInformation, error)
}

type sourceType string

const (
	HTTP    sourceType = "http"
	HTTPS              = "https"
	Unknown            = ""
)

func SourceTypeFromString(sourceTypeStr string) (sourceType, error) {
	switch sourceTypeStr {
	case string(HTTPS):
		return HTTPS, nil
	case string(HTTP):
		return HTTP, nil
	default:
		return Unknown, fmt.Errorf("unknown source type '%s': %w", sourceTypeStr, apperrors.ErrUnsupported)
	}
}
