package kubevirt

import (
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
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
        imageManager: imageManager,
        networkManager: networkManager,
        cfg: cfg,
    }, nil
}

func (m *manager) AllocateComputeResource(req *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error){
    if req == nil {
        return nil, fmt.Errorf("request can't be empty")
    }
    // Get the software image related to the allocate compute request
    if req.ComputeFlavourId == nil {
        return nil, fmt.Errorf("computeFlavourId can't be empty")
    }
    _, err := m.flavourManager.GetFlavour(req.ComputeFlavourId)
    if err != nil {
        return nil, fmt.Errorf("failed to retrive flavour with id \"%s\": %w", req.ComputeFlavourId.GetValue(), err)
    }


    return nil, nil
}
