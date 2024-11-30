package flavour

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

type FlavourMetadata map[string]interface{}

type Manager interface {
	CreateFlavour(context.Context, *nfv.VirtualComputeFlavour) (*nfv.Identifier, error)
	GetFlavour(context.Context, *nfv.Identifier) (*nfv.VirtualComputeFlavour, FlavourMetadata, error)
	// TODO: Add Filter
	GetFlavours() ([]*nfv.VirtualComputeFlavour, error)
	DeleteFlavour(*nfv.Identifier) error
}
