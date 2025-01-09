package flavour

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	K8sFlavourIdLabel     = "flavour.kubevim.kubenfv.io/id"
	K8sFlavourSourceLabel = "flavour.kubevim.kubenfv.io/source"
)

type Manager interface {
	CreateFlavour(context.Context, *nfv.VirtualComputeFlavour) (*nfv.Identifier, error)
	GetFlavour(context.Context, *nfv.Identifier) (*nfv.VirtualComputeFlavour, error)
	// TODO: Add Filter
	GetFlavours() ([]*nfv.VirtualComputeFlavour, error)
	DeleteFlavour(*nfv.Identifier) error
}
