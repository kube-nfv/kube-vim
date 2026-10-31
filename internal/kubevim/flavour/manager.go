package flavour

import (
	"context"

	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
)

const (
	K8sFlavourIdLabel = "flavour.kubevim.kubenfv.io/id"
	// K8sFlavourAnnotation holds the flavour as the consumer created it (protojson, without its id).
	K8sFlavourAnnotation = "flavour.kubevim.kubenfv.io/flavour"

	// Metadata keys kube-vim adds to a flavour: the KubeVirt objects compute uses to run it.
	InstancetypeMetadataKey = "kubevim.kubenfv.io/instancetype"
	PreferenceMetadataKey   = "kubevim.kubenfv.io/preference"
)

//go:generate go run go.uber.org/mock/mockgen -source=manager.go -destination=mock/mock_manager.go -package=mock

type Manager interface {
	CreateFlavour(context.Context, *flavourv1.Flavour) (*flavourv1.Flavour, error)
	GetFlavour(ctx context.Context, flavourId string) (*flavourv1.Flavour, error)
	ListFlavours(context.Context) ([]*flavourv1.Flavour, error)
	DeleteFlavour(ctx context.Context, flavourId string) error
}
