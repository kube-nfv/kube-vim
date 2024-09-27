package kubevirt

import (
	"context"
	"fmt"
	"time"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/google/uuid"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"kubevirt.io/api/instancetype/v1beta1"
	kubevirt "kubevirt.io/client-go/generated/kubevirt/clientset/versioned"
)

const (
    CreateFlavourRqTimeout = time.Second * 5
    KubeVimFlavourIdLabel = "kubevim.kubenfv.io/flavour-id"
)


type manager struct {
    kubevirtClient *kubevirt.Clientset
}

func NewFlavourManager(k8sConfig *rest.Config) (*manager, error) {
    c, err := kubevirt.NewForConfig(k8sConfig)
    if err != nil {
        return nil, fmt.Errorf("Failed to create kube-virt k8s client: %w", err)
    }
    return &manager{
        kubevirtClient: c,
    }, nil
}

func (m *manager) CreateFlavour(ctx context.Context, nfvFlavour *nfv.VirtualComputeFlavour) (*nfv.Identifier, error) {
    instType, instPref, err := kubeVirtInstanceTypePreferencesFromNfvFlavour(nfvFlavour)
    if err != nil {
        return nil, fmt.Errorf("failed to convert nfv object to the kube-virt resources: %w", err)
    }
    var flavourId string
    if nfvFlavour.FlavourId != nil && nfvFlavour.FlavourId.Value != "" {
        flavourId = nfvFlavour.FlavourId.Value
    } else {
        newId, err := uuid.NewRandom()
        if err != nil {
            return nil, fmt.Errorf("Failed to generate UUID for flavour: %w", err)
        }
        flavourId = newId.String()
    }

    instType.Labels[KubeVimFlavourIdLabel] = flavourId
    instPref.Labels[KubeVimFlavourIdLabel] = flavourId

    createCtx, cancel := context.WithTimeout(ctx, CreateFlavourRqTimeout)
    defer cancel()

    _, err = m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(config.KubeNfvDefaultNamespace).Create(createCtx, instType, v1.CreateOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to create kube-virt VirtualMachineInstanceType: %w", err)
    }

    _, err = m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(config.KubeNfvDefaultNamespace).Create(createCtx, instPref, v1.CreateOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to create kube-virt VirtualMachinePreference: %w", err)
    }
    
    return &nfv.Identifier{
        Value: flavourId,
    }, nil
}

func (m *manager) GetFlavours() ([]*nfv.VirtualComputeFlavour, error) {

    return nil, nil
}

func (m *manager) DeleteFlavour(*nfv.Identifier) error {

    return nil
}


func kubeVirtInstanceTypePreferencesFromNfvFlavour(nfvFlavour *nfv.VirtualComputeFlavour) (*v1beta1.VirtualMachineInstancetype, *v1beta1.VirtualMachinePreference, error) {
    if nfvFlavour == nil {
        return nil, nil, fmt.Errorf("flavour can't be empty")
    }
    vmInstTypeSpec := v1beta1.VirtualMachineInstancetypeSpec{}
    if nfvFlavour.VirtualCpu == nil {
        return nil, nil, fmt.Errorf("virtual cpu can't be empty")
    }
    if nfvFlavour.VirtualMemory == nil {
        return nil, nil, fmt.Errorf("virtual memory can't be empty")
    }
    if nfvFlavour.VirtualCpu.NumVirtualCpu == 0 {
        return nil, nil, fmt.Errorf("virtualCpu.NumVirtualCpu can't be 0")
    }
    // TODO: Add CPUPinning, NUMA, etc.
    vmInstTypeSpec.CPU = v1beta1.CPUInstancetype{
        Guest: nfvFlavour.VirtualCpu.NumVirtualCpu,
    }
    if nfvFlavour.VirtualMemory.VirtualMemSize == 0 {
        return nil, nil, fmt.Errorf("virtual memory size can't be 0")
    }
    memQ, err := resource.ParseQuantity(fmt.Sprintf("%fM", nfvFlavour.VirtualMemory.VirtualMemSize))
    if err != nil {
        return nil, nil, fmt.Errorf("failed to convert %fM to the k8s Quantity: %w", nfvFlavour.VirtualMemory.VirtualMemSize, err)
    }
    vmInstTypeSpec.Memory = v1beta1.MemoryInstancetype{
        Guest: memQ,
    }
    return &v1beta1.VirtualMachineInstancetype{
        ObjectMeta: v1.ObjectMeta{
            Labels: config.KubeNfvManagedbyLabel,
        },
        Spec: vmInstTypeSpec,
    }, &v1beta1.VirtualMachinePreference{
        ObjectMeta: v1.ObjectMeta{},
    }, nil
}
