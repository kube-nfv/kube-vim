package image

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	K8sSourceLabel    = "image.kubevim.kubenfv.io/source"
    K8sSourceUrlLabel = "image.kubevim.kubenfv.io/source-url"
    K8sIsUploadLabel  = "image.kubevim.kubenfv.io/uploaded"
)

type Manager interface {
	GetImage(context.Context, *nfv.Identifier) (*nfv.SoftwareImageInformation, error)
	GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error)
}

type sourceType string
const (
	HTTP  sourceType = "http"
	HTTPS            = "https"
    Unknown          = ""
)

func SourceTypeFromString(sourceTypeStr string) (sourceType, error) {
    switch sourceTypeStr {
    case string(HTTPS): return HTTPS, nil
    case string(HTTP): return HTTP, nil
    default: return Unknown, fmt.Errorf("unknown source type \"%s\": %w", sourceTypeStr, config.UnsupportedErr)
    }
}
