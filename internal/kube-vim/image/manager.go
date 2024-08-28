package image

import (
	"context"

	"github.com/DiMalovanyy/kube-vim-api/pb/nfv"
)

type Manager interface {
    GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error)
    GetImage(*nfv.Identifier) (*nfv.SoftwareImageInformation, error)
    UploadImage(context.Context, *nfv.Identifier, string /*location*/) error
}
