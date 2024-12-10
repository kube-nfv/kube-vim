package image

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	K8sSourceLabel    = "app.kubevim.kubenfv.io/source"
	K8sSourceUrlLabel = "app.kubevim.kubenfv.io/source-url"
)

type Manager interface {
	GetImage(context.Context, *nfv.Identifier) (*nfv.SoftwareImageInformation, error)
	GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error)
	UploadImage(context.Context, *nfv.Identifier, string /*location*/) error
}
