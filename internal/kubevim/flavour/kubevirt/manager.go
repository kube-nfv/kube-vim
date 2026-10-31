package kubevirt

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"kubevirt.io/api/instancetype/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CreateFlavourRqTimeout = time.Second * 5
)

// IdIndexes are the cache indexes the manager looks flavours up by.
var IdIndexes = []k8s.LabelIndex{
	{Obj: &v1beta1.VirtualMachineInstancetype{}, Label: flavour.K8sFlavourIdLabel},
}

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

func (m *manager) CreateFlavour(ctx context.Context, nfvFlavour *flavourv1.Flavour) (*flavourv1.Flavour, error) {
	if err := checkSupported(nfvFlavour); err != nil {
		return nil, err
	}
	flavourId := uuid.NewString()
	instType, instPref, err := kubeVirtObjects(flavourId, nfvFlavour)
	if err != nil {
		return nil, fmt.Errorf("convert flavour to kubevirt resources: %w", err)
	}

	// Created objects must land in the configured namespace, otherwise the
	// namespace-scoped Get/List/Delete lookups would never find them.
	instType.Namespace = *m.cfg.Namespace
	instPref.Namespace = *m.cfg.Namespace

	createCtx, cancel := context.WithTimeout(ctx, CreateFlavourRqTimeout)
	defer cancel()

	if err := m.client.Create(createCtx, instType); err != nil {
		return nil, fmt.Errorf("create VirtualMachineInstancetype '%s' for flavour '%s': %w", instType.Name, flavourId, err)
	}
	if err := m.client.Create(createCtx, instPref); err != nil {
		if delErr := m.client.Delete(ctx, instType); delErr != nil {
			err = fmt.Errorf("%w (rollback of VirtualMachineInstancetype '%s' failed: %v)", err, instType.Name, delErr)
		}
		return nil, fmt.Errorf("create VirtualMachinePreference '%s' for flavour '%s': %w", instPref.Name, flavourId, err)
	}
	return flavourFromInstancetype(instType)
}

func (m *manager) GetFlavour(ctx context.Context, flavourId string) (*flavourv1.Flavour, error) {
	instType, err := m.getInstancetype(ctx, flavourId)
	if err != nil {
		return nil, err
	}
	return flavourFromInstancetype(instType)
}

func (m *manager) ListFlavours(ctx context.Context) ([]*flavourv1.Flavour, error) {
	instTypeList := &v1beta1.VirtualMachineInstancetypeList{}
	if err := m.client.List(ctx, instTypeList, client.InNamespace(*m.cfg.Namespace),
		client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
		return nil, fmt.Errorf("list VirtualMachineInstancetypes: %w", err)
	}
	res := make([]*flavourv1.Flavour, 0, len(instTypeList.Items))
	for idx := range instTypeList.Items {
		f, err := flavourFromInstancetype(&instTypeList.Items[idx])
		if err != nil {
			return nil, err
		}
		res = append(res, f)
	}
	return res, nil
}

func (m *manager) DeleteFlavour(ctx context.Context, flavourId string) error {
	if _, err := m.getInstancetype(ctx, flavourId); err != nil {
		return err
	}
	ns := client.InNamespace(*m.cfg.Namespace)
	// Delete only kube-nfv-owned objects carrying this flavour id, so a same-named
	// object we do not own is never touched.
	sel := client.MatchingLabels{
		common.K8sManagedByLabel:  common.KubeNfvName,
		flavour.K8sFlavourIdLabel: flavourId,
	}
	if err := m.client.DeleteAllOf(ctx, &v1beta1.VirtualMachineInstancetype{}, ns, sel); err != nil {
		return fmt.Errorf("delete VirtualMachineInstancetype for flavour '%s': %w", flavourId, err)
	}
	if err := m.client.DeleteAllOf(ctx, &v1beta1.VirtualMachinePreference{}, ns, sel); err != nil {
		return fmt.Errorf("delete VirtualMachinePreference for flavour '%s': %w", flavourId, err)
	}
	return nil
}

// getInstancetype returns the kube-nfv-owned instancetype of flavour flavourId.
func (m *manager) getInstancetype(ctx context.Context, flavourId string) (*v1beta1.VirtualMachineInstancetype, error) {
	instTypeList := &v1beta1.VirtualMachineInstancetypeList{}
	if err := m.client.List(ctx, instTypeList, client.InNamespace(*m.cfg.Namespace),
		client.MatchingFields{flavour.K8sFlavourIdLabel: flavourId}); err != nil {
		return nil, fmt.Errorf("list VirtualMachineInstancetypes for flavour '%s': %w", flavourId, err)
	}
	switch len(instTypeList.Items) {
	case 0:
		return nil, &apperrors.ErrNotFound{Entity: "flavour", Identifier: flavourId}
	case 1:
	default:
		return nil, fmt.Errorf("%d VirtualMachineInstancetypes carry flavour id '%s': %w", len(instTypeList.Items), flavourId, apperrors.ErrInternal)
	}
	instType := &instTypeList.Items[0]
	if !misc.IsObjectManagedByKubeNfv(instType) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachineInstancetype", ObjectName: instType.Name, ObjectId: string(instType.GetUID())}
	}
	return instType, nil
}
