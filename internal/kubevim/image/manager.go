package image

import (
	"context"
	"fmt"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config"
)

const (
	K8sSourceLabel = "image.kubevim.kubenfv.io/source"
	K8sIsUploadLabel  = "image.kubevim.kubenfv.io/uploaded"

	K8sSourceUrlAnnotation = "image.kubevim.kubenfv.io/source-url"
)

type Manager interface {
	GetImage(context.Context, *nfv.Identifier) (*nfv.SoftwareImageInformation, error)
	ListImages(context.Context) ([]*nfv.SoftwareImageInformation, error)
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
		return Unknown, fmt.Errorf("unknown source type \"%s\": %w", sourceTypeStr, common.UnsupportedErr)
	}
}
