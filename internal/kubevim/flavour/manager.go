package flavour

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

type Manager interface {
	CreateFlavour(context.Context, *nfv.VirtualComputeFlavour) (*nfv.Identifier, error)
	// TODO: Add Filter
	GetFlavours() ([]*nfv.VirtualComputeFlavour, error)
	DeleteFlavour(*nfv.Identifier) error
}
