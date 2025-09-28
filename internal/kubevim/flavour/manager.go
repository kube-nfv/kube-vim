package flavour

import (
	"context"

	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
)

const (
	K8sFlavourIdLabel             = "flavour.kubevim.kubenfv.io/id"
	K8sFlavourSourceLabel         = "flavour.kubevim.kubenfv.io/source"
	K8sVolumesAnnotation          = "flavour.kubevim.kubenfv.io/volumes"
	K8sFlavourAttNameAnnotation   = "flavour.kubevim.kubenfv.io/attached-name"
)

type Manager interface {
	CreateFlavour(context.Context, *vivnfm.VirtualComputeFlavour) (*nfvcommon.Identifier, error)
	GetFlavour(context.Context, *nfvcommon.Identifier) (*vivnfm.VirtualComputeFlavour, error)
	// TODO: Add Filter
	GetFlavours(context.Context) ([]*vivnfm.VirtualComputeFlavour, error)
	DeleteFlavour(context.Context, *nfvcommon.Identifier) error
}
