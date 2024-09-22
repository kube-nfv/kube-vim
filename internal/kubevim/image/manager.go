package image

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

type Manager interface {
	GetImage(*nfv.Identifier) (*nfv.SoftwareImageInformation, error)
	GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error)
	UploadImage(context.Context, *nfv.Identifier, string /*location*/) error
}
