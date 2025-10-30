package kubevirt

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/misc"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"kubevirt.io/api/instancetype/v1beta1"
	kubevirt "kubevirt.io/client-go/kubevirt"
)

const (
	CreateFlavourRqTimeout = time.Second * 5
	// Note(dmalovan): Name Annotations used from kubevirt.io/api/v1 but it lack of
	// Id annotations which is important to know.
	KubevirtInstanceTypeIdAnnotation = "kubevirt.io/instancetype-id"
	KubevirtPreferenceIdAnnotation   = "kubevirt.io/preference-id"
	KubevirtFlavourSource            = "kubevirt.io"
)

type manager struct {
	kubevirtClient *kubevirt.Clientset

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewFlavourManager(k8sConfig *rest.Config, cfg *config.K8sConfig) (*manager, error) {
	c, err := kubevirt.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubevirt client: %w", err)
	}
	return &manager{
		kubevirtClient: c,
		cfg:            cfg,
	}, nil
}

func (m *manager) CreateFlavour(ctx context.Context, nfvFlavour *vivnfm.VirtualComputeFlavour) (*nfvcommon.Identifier, error) {
	if nfvFlavour == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour", Reason: "cannot be nil"}
	}
	var flavourId string
	if nfvFlavour.FlavourId != nil && nfvFlavour.FlavourId.Value != "" {
		flavourId = nfvFlavour.FlavourId.Value
	} else {
		newId, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("generate flavour UUID: %w", err)
		}
		flavourId = newId.String()
	}
	instType, instPref, err := kubeVirtInstanceTypePreferencesFromNfvFlavour(flavourId, nfvFlavour)
	if err != nil {
		return nil, fmt.Errorf("convert flavour to kubevirt resources: %w", err)
	}

	createCtx, cancel := context.WithTimeout(ctx, CreateFlavourRqTimeout)
	defer cancel()

	_, err = m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(*m.cfg.Namespace).Create(createCtx, instType, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create VirtualMachineInstancetype '%s' for flavour '%s': %w", instType.Name, flavourId, err)
	}

	_, err = m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(*m.cfg.Namespace).Create(createCtx, instPref, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create VirtualMachinePreference '%s' for flavour '%s': %w", instPref.Name, flavourId, err)
	}

	return &nfvcommon.Identifier{
		Value: flavourId,
	}, nil
}

func (m *manager) GetFlavour(ctx context.Context, id *nfvcommon.Identifier) (*vivnfm.VirtualComputeFlavour, error) {
	if id == nil || id.GetValue() == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour id", Reason: "required"}
	}
	flavourIdSelector := fmt.Sprintf("%s=%s", flavour.K8sFlavourIdLabel, id.GetValue())
	instTypeList, err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(*m.cfg.Namespace).List(ctx, v1.ListOptions{
		LabelSelector: flavourIdSelector,
	})
	if err != nil || instTypeList == nil {
		return nil, fmt.Errorf("list VirtualMachineInstancetypes for flavour %s: %w", id.GetValue(), err)
	}
	if len(instTypeList.Items) == 0 {
		return nil, &apperrors.ErrNotFound{Entity: "flavour", Identifier: id.GetValue()}
	}
	if len(instTypeList.Items) > 1 {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour id", Reason: fmt.Sprintf("multiple flavours found with id '%s'", id.GetValue())}
	}
	instType := &instTypeList.Items[0]
	if !misc.IsObjectManagedByKubeNfv(instType) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachineInstancetype", ObjectName: instType.Name, ObjectId: string(instType.GetUID())}
	}

	instPrefList, err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(*m.cfg.Namespace).List(ctx, v1.ListOptions{
		LabelSelector: flavourIdSelector,
	})
	// It's totaly possible that instancePreference won't exists in the cluster
	var instPref *v1beta1.VirtualMachinePreference
	if err == nil && instPrefList != nil && len(instPrefList.Items) == 1 {
		instPref = &instPrefList.Items[0]
		if !misc.IsObjectManagedByKubeNfv(instPref) {
			return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachinePreference", ObjectName: instPref.Name, ObjectId: string(instPref.GetUID())}
		}
	}
	nfvFlavour, err := nfvFlavourFromKubeVirtInstanceTypePreferences(id.GetValue(), instType, instPref)
	if err != nil {
		return nil, fmt.Errorf("convert kubevirt resources to flavour '%s' (instancetype: %s): %w", id.GetValue(), instType.Name, err)
	}
	return nfvFlavour, nil
}

func (m *manager) GetFlavours(ctx context.Context) ([]*vivnfm.VirtualComputeFlavour, error) {
	instTypeList, err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(*m.cfg.Namespace).List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil || instTypeList == nil {
		return nil, fmt.Errorf("list VirtualMachineInstancetypes: %w", err)
	}

	instPrefList, err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(*m.cfg.Namespace).List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil || instPrefList == nil {
		return nil, fmt.Errorf("list VirtualMachinePreferences: %w", err)
	}

	res := make([]*vivnfm.VirtualComputeFlavour, 0, len(instTypeList.Items))
	for idx := range instTypeList.Items {
		instTypeRef := &instTypeList.Items[idx]
		flavourId, ok := instTypeRef.Labels[flavour.K8sFlavourIdLabel]
		if !ok {
			return nil, &apperrors.ErrInvalidArgument{Field: fmt.Sprintf("VirtualMachineInstancetype '%s'", instTypeRef.Name), Reason: "missing flavour id label"}
		}
		var instPref *v1beta1.VirtualMachinePreference
	preferenceLoop:
		for pIdx := range instPrefList.Items {
			instPrefRef := &instPrefList.Items[pIdx]
			prefFlavourId, ok := instPrefRef.Labels[flavour.K8sFlavourIdLabel]
			if !ok {
				// VirtualMachinePreference has no FlavourID label. Might be exception situation.
				continue preferenceLoop
			}
			if flavourId == prefFlavourId {
				instPref = instPrefRef
				break preferenceLoop
			}
		}
		// instPref can be nil
		nfvFlavour, err := nfvFlavourFromKubeVirtInstanceTypePreferences(flavourId, instTypeRef, instPref)
		if err != nil {
			return nil, fmt.Errorf("convert kubevirt resources to flavour '%s' (instancetype: %s): %w", flavourId, instTypeRef.Name, err)
		}
		res = append(res, nfvFlavour)
	}
	return res, nil
}

func (m *manager) DeleteFlavour(ctx context.Context, id *nfvcommon.Identifier) error {
	_, err := m.GetFlavour(ctx, id)
	if err != nil {
		return fmt.Errorf("verify flavour '%s' exists: %w", id.Value, err)
	}
	instTypeName := flavourNameFromId(id.Value)
	if err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachineInstancetypes(*m.cfg.Namespace).Delete(ctx, instTypeName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete VirtualMachineInstancetype '%s' for flavour '%s': %w", instTypeName, id.Value, err)
	}
	instPrefName := flavourPreferenceNameFromId(id.Value)
	if err := m.kubevirtClient.InstancetypeV1beta1().VirtualMachinePreferences(*m.cfg.Namespace).Delete(ctx, instPrefName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete VirtualMachinePreference '%s' for flavour '%s': %w", instPrefName, id.Value, err)
	}
	return nil
}
