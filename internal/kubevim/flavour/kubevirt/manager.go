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
	"kubevirt.io/api/instancetype/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// client serves cache-backed reads and direct writes.
	client client.Client
	// apiReader is uncached (hits the apiserver directly); use only where a
	// strongly-consistent read is required. No flavour path needs it today.
	apiReader client.Reader

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewFlavourManager(cl client.Client, apiReader client.Reader, cfg *config.K8sConfig) (*manager, error) {
	if cfg == nil || cfg.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "k8sConfig.namespace", Reason: "required"}
	}
	return &manager{
		client:    cl,
		apiReader: apiReader,
		cfg:       cfg,
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

	if err := m.client.Create(createCtx, instType); err != nil {
		return nil, fmt.Errorf("create VirtualMachineInstancetype '%s' for flavour '%s': %w", instType.Name, flavourId, err)
	}

	if err := m.client.Create(createCtx, instPref); err != nil {
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
	idSelector := client.MatchingLabels{flavour.K8sFlavourIdLabel: id.GetValue()}
	ns := client.InNamespace(*m.cfg.Namespace)

	instTypeList := &v1beta1.VirtualMachineInstancetypeList{}
	if err := m.client.List(ctx, instTypeList, ns, idSelector); err != nil {
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

	instPrefList := &v1beta1.VirtualMachinePreferenceList{}
	// It's totaly possible that instancePreference won't exists in the cluster
	var instPref *v1beta1.VirtualMachinePreference
	if err := m.client.List(ctx, instPrefList, ns, idSelector); err == nil && len(instPrefList.Items) == 1 {
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
	managed := client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}
	ns := client.InNamespace(*m.cfg.Namespace)

	instTypeList := &v1beta1.VirtualMachineInstancetypeList{}
	if err := m.client.List(ctx, instTypeList, ns, managed); err != nil {
		return nil, fmt.Errorf("list VirtualMachineInstancetypes: %w", err)
	}

	instPrefList := &v1beta1.VirtualMachinePreferenceList{}
	if err := m.client.List(ctx, instPrefList, ns, managed); err != nil {
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
	ns := client.InNamespace(*m.cfg.Namespace)
	// Delete only kube-nfv-owned objects carrying this flavour id, so a same-named
	// object we do not own is never touched. Idempotent: deleting a missing flavour
	// is a no-op (the preference is optional and may not exist).
	sel := client.MatchingLabels{
		common.K8sManagedByLabel:  common.KubeNfvName,
		flavour.K8sFlavourIdLabel: id.GetValue(),
	}
	if err := m.client.DeleteAllOf(ctx, &v1beta1.VirtualMachineInstancetype{}, ns, sel); err != nil {
		return fmt.Errorf("delete VirtualMachineInstancetype for flavour '%s': %w", id.GetValue(), err)
	}
	if err := m.client.DeleteAllOf(ctx, &v1beta1.VirtualMachinePreference{}, ns, sel); err != nil {
		return fmt.Errorf("delete VirtualMachinePreference for flavour '%s': %w", id.GetValue(), err)
	}
	return nil
}
