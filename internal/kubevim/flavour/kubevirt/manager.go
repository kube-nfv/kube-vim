package kubevirt

import (
	"context"
	"fmt"
	"time"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/google/uuid"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	kubevirt "kubevirt.io/client-go/generated/kubevirt/clientset/versioned"
)

const (
	CreateFlavourRqTimeout = time.Second * 5
	KubeVimFlavourIdLabel  = "kubevim.kubenfv.io/flavour-id"
)

type manager struct {
	kubevirtClient *kubevirt.Clientset

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewFlavourManager(k8sConfig *rest.Config, cfg *config.K8sConfig) (*manager, error) {
	c, err := kubevirt.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to create kube-virt k8s client: %w", err)
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
			return nil, fmt.Errorf("Failed to generate UUID for flavour: %w", err)
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

func (m *manager) GetFlavour(*nfv.Identifier) (*nfv.VirtualComputeFlavour, error) {

	return nil, nil
}

func (m *manager) GetFlavours() ([]*nfv.VirtualComputeFlavour, error) {

	return nil, nil
}

func (m *manager) DeleteFlavour(*nfv.Identifier) error {

	return nil
}
