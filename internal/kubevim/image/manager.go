package image

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	K8sSourceLabel    = "app.kubevim.kubenfv.io/image-source"
    K8sSourceUrlLabel = "app.kubevim.kubenfv.io/image-source-url"
)

type SourceType string

const (
	HTTP  SourceType = "http"
	HTTPS            = "https"
)

type Manager interface {
	GetImage(context.Context, *nfv.Identifier) (*nfv.SoftwareImageInformation, error)
	GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error)
}
