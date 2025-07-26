package flavour

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	K8sFlavourIdLabel             = "flavour.kubevim.kubenfv.io/id"
	K8sFlavourSourceLabel         = "flavour.kubevim.kubenfv.io/source"
	K8sVolumesAnnotation          = "flavour.kubevim.kubenfv.io/volumes"
	K8sFlavourAttNameAnnotation   = "flavour.kubevim.kubenfv.io/attached-name"
)

type Manager interface {
	CreateFlavour(context.Context, *nfv.VirtualComputeFlavour) (*nfv.Identifier, error)
	GetFlavour(context.Context, *nfv.Identifier) (*nfv.VirtualComputeFlavour, error)
	// TODO: Add Filter
	GetFlavours(context.Context) ([]*nfv.VirtualComputeFlavour, error)
	DeleteFlavour(context.Context, *nfv.Identifier) error
}
