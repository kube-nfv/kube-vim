package kubevirt

import (
	"context"
	"fmt"
	"time"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
	"github.com/google/uuid"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	kubevirt "kubevirt.io/client-go/generated/kubevirt/clientset/versioned"
	"kubevirt.io/api/instancetype/v1beta1"
)

const (
	CreateFlavourRqTimeout = time.Second * 5
	KubeVimFlavourIdLabel  = "kubevim.kubenfv.io/flavour-id"
    KubeVirtFlavourMetadataKeyName = "kube-virt"
)

type KubeVirtFlavourMetadata struct {
    VirtualMachineInstanceTypeName string
    VirtualMachinePreferencesName  string
}

type manager struct {
	kubevirtClient *kubevirt.Clientset

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewFlavourManager(k8sConfig *rest.Config, cfg *config.K8sConfig) (*manager, error) {
	c, err := kubevirt.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-virt k8s client: %w", err)
	}
	return &manager{
		kubevirtClient: c,
		cfg:            cfg,
	}, nil
}

func (m *manager) CreateFlavour(ctx context.Context, nfvFlavour *nfv.VirtualComputeFlavour) (*nfv.Identifier, error) {
	var flavourId string
	if nfvFlavour.FlavourId != nil && nfvFlavour.FlavourId.Value != "" {
		flavourId = nfvFlavour.FlavourId.Value
	} else {
		newId, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("failed to generate UUID for flavour: %w", err)
		}
		flavourId = newId.String()
	}
	instType, instPref, err := kubeVirtInstanceTypePreferencesFromNfvFlavour(flavourId, nfvFlavour)
	if err != nil {
		return nil, fmt.Errorf("failed to convert nfv object to the kube-virt resources: %w", err)
	}

	createCtx, cancel := context.WithTimeout(ctx, CreateFlavourRqTimeout)
	defer cancel()

	_, err = m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(m.cfg.Namespace).Create(createCtx, instType, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-virt VirtualMachineInstanceType: %w", err)
	}

	_, err = m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(m.cfg.Namespace).Create(createCtx, instPref, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-virt VirtualMachinePreferences: %w", err)
	}

	return &nfv.Identifier{
		Value: flavourId,
	}, nil
}

func (m *manager) GetFlavour(ctx context.Context, id *nfv.Identifier) (*nfv.VirtualComputeFlavour, flavour.FlavourMetadata, error) {
    if id == nil {
        return nil, nil, fmt.Errorf("id can't be nil")
    }
    flavourIdSelector := fmt.Sprintf("%s=%s", KubeVimFlavourIdLabel, id.GetValue())
    instTypeList, err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(m.cfg.Namespace).List(ctx, v1.ListOptions{
        LabelSelector: flavourIdSelector,
    })
    if err != nil || instTypeList == nil {
        return nil, nil, fmt.Errorf("failed to get VirtualMachineInstanceType objects from the kube-virt: %w", err)
    }
    if len(instTypeList.Items) == 0 {
        return nil, nil, fmt.Errorf("no flavours found specified by the id \"%s\"", id.GetValue())
    }
    if len(instTypeList.Items) > 1 {
        return nil, nil, fmt.Errorf("more than one flavour found specified by the id \"%s\"", id.GetValue())
    }
    instType := &instTypeList.Items[0]
    kubeVirtFlavourMeta := &KubeVirtFlavourMetadata{
        VirtualMachineInstanceTypeName: instType.Name,
    }
    instPrefList, err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(m.cfg.Namespace).List(ctx, v1.ListOptions{
        LabelSelector: flavourIdSelector,
    })
    // It's totaly possible that instancePreference won't exists in the cluster
    var instPref *v1beta1.VirtualMachinePreference
    if err != nil && instPrefList != nil && len(instPrefList.Items) == 1 {
        instPref = &instPrefList.Items[0]
        kubeVirtFlavourMeta.VirtualMachinePreferencesName = instPref.Name
    }
    nfvFlavour, err := nfvFlavourFromKubeVirtInstanceTypePreferences(id.GetValue(), instType, instPref)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to convert kube-virt instance type and preferences to the kube-nfv resources: %w", err)
    }
    return nfvFlavour, flavour.FlavourMetadata{
        KubeVirtFlavourMetadataKeyName: kubeVirtFlavourMeta,
    }, nil
}

func (m *manager) GetFlavours() ([]*nfv.VirtualComputeFlavour, error) {

	return nil, config.NotImplementedErr
}

func (m *manager) DeleteFlavour(*nfv.Identifier) error {

	return config.NotImplementedErr
}
