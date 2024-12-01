package kubevirt

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour/kubevirt"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"k8s.io/client-go/rest"
	kubevirt "kubevirt.io/client-go/generated/kubevirt/clientset/versioned"
)

// kubevirt manager for allocation and management of the compute resources.
type manager struct {
	kubevirtClient *kubevirt.Clientset
	flavourManager flavour.Manager
	imageManager   image.Manager
	networkManager network.Manager

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewComputeManager(
	k8sConfig *rest.Config,
	cfg *config.K8sConfig,
	flavourManager flavour.Manager,
	imageManager image.Manager,
	networkManager network.Manager) (*manager, error) {
	c, err := kubevirt.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-virt k8s client: %w", err)
	}
	return &manager{
		kubevirtClient: c,
		flavourManager: flavourManager,
		imageManager:   imageManager,
		networkManager: networkManager,
		cfg:            cfg,
	}, nil
}

func (m *manager) AllocateComputeResource(ctx context.Context, req *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error) {
	if req == nil {
		return nil, fmt.Errorf("request can't be empty")
	}

	// Get request related compute flavour
	if req.ComputeFlavourId == nil || req.ComputeFlavourId.GetValue() == "" {
		return nil, fmt.Errorf("computeFlavourId can't be empty")
	}
	_, flavourMeta, err := m.flavourManager.GetFlavour(ctx, req.ComputeFlavourId)
	if err != nil {
		return nil, fmt.Errorf("failed to retrive flavour with id \"%s\": %w", req.ComputeFlavourId.GetValue(), err)
	}
	if flavourMeta == nil {
		return nil, fmt.Errorf("flavour metadata can't be nil: %w", config.UnsupportedErr)
	}
	kubeVirtMetaIf, ok := flavourMeta[kubevirt_flavour.KubeVirtFlavourMetadataKeyName]
	if !ok {
		return nil, fmt.Errorf("kubevirt compute manager can only works with kubevirt flavour manager: %w", config.UnsupportedErr)
	}
	// TODO(dmalovan): Add the ability to works with different flavours providers/managers (eg. get flavours directly from the nova)
	_, ok = kubeVirtMetaIf.(*kubevirt_flavour.KubeVirtFlavourMetadata)
	if !ok {
		return nil, fmt.Errorf("failed to convert kubevirt flavour metadata. Invaid object type")
	}

	// Get Request related image and place it
	if req.VcImageId == nil || req.VcImageId.GetValue() == "" {
		return nil, fmt.Errorf("vcImageId can't be empty")
	}

	return nil, nil
}
