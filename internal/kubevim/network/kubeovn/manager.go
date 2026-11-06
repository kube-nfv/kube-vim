package kubeovn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"go.uber.org/zap"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IdIndexes are the cache indexes the manager looks networks and subnets up by.
var IdIndexes = []k8s.LabelIndex{
	{Obj: &kubeovnv1.Vpc{}, Label: network.K8sNetworkIdLabel},
	{Obj: &kubeovnv1.Vlan{}, Label: network.K8sNetworkIdLabel},
	{Obj: &kubeovnv1.Subnet{}, Label: network.K8sNetworkIdLabel},
	{Obj: &kubeovnv1.Subnet{}, Label: network.K8sSubnetIdIPv4Label},
	{Obj: &kubeovnv1.Subnet{}, Label: network.K8sSubnetIdIPv6Label},
	{Obj: &netattv1.NetworkAttachmentDefinition{}, Label: network.K8sNetworkIdLabel},
}

// Will manage kube-vim networking for VNF using kube-ovn
type manager struct {
	logger *zap.Logger
	// client serves cache-backed reads and direct writes for kube-vim-owned objects.
	client client.Client
	// apiReader is uncached; used by the management-network reconciliation, which
	// reads objects that may not yet carry the managed-by label (and so are not in
	// the cache) before labelling them.
	apiReader client.Reader
	k8sCfg    *config.K8sConfig
	// sriovSocketFile is the OVSDB socket ovs-cni connects to for SR-IOV interfaces.
	sriovSocketFile string
	// cacheSyncTimeout bounds how long network create waits for the cache to see what it created.
	cacheSyncTimeout time.Duration
}

const defaultCacheSyncTimeout = 2 * time.Second

func NewKubeovnNetworkManager(cl client.Client, apiReader client.Reader, k8sCfg *config.K8sConfig, sriovCfg *config.SriovNetworkConfig, logger *zap.Logger) (*manager, error) {
	if k8sCfg.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config k8s.Namespace", Reason: "can't be nil"}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	var socketFile string
	if sriovCfg != nil && sriovCfg.SocketFile != nil {
		socketFile = *sriovCfg.SocketFile
	}
	return &manager{
		logger:           logger,
		client:           cl,
		apiReader:        apiReader,
		k8sCfg:           k8sCfg,
		sriovSocketFile:  socketFile,
		cacheSyncTimeout: defaultCacheSyncTimeout,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, n *networkv1.Network) (*networkv1.Network, error) {
	if err := checkSupported(n); err != nil {
		return nil, err
	}
	networkId := uuid.NewString()
	name := formatNetworkName(networkId)
	labels := networkLabels(networkId)
	annotations, err := networkAnnotations(n)
	if err != nil {
		return nil, err
	}

	var created []client.Object
	create := func(obj client.Object) error {
		if err := m.client.Create(ctx, obj); err != nil {
			return fmt.Errorf("create %T '%s' for network '%s': %w", obj, obj.GetName(), networkId, err)
		}
		created = append(created, obj)
		return nil
	}
	fail := func(err error) (*networkv1.Network, error) {
		if rbErr := m.rollback(ctx, created); rbErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback network '%s': %w", networkId, rbErr))
		}
		return nil, err
	}

	var primary client.Object
	if vpc, vlan := primaryObject(name, n, labels, annotations); vpc != nil {
		primary = vpc
	} else {
		primary = vlan
	}
	if err := create(primary); err != nil {
		return fail(err)
	}
	var sub *kubeovnv1.Subnet
	if l3 := n.GetLayer3Attributes(); len(l3) > 0 {
		nad := bridgeNetAttach(name, *m.k8sCfg.Namespace, labels)
		if err := create(nad); err != nil {
			return fail(err)
		}
		subnetIds := make([]string, len(l3))
		for idx := range l3 {
			subnetIds[idx] = uuid.NewString()
		}
		sub, err = kubeovnSubnet(name, n, l3, subnetIds, labels, formatNetAttachKubeOvnProvider(nad.Name, nad.Namespace))
		if err != nil {
			return fail(err)
		}
		if err := create(sub); err != nil {
			return fail(err)
		}
	}
	if n.GetSriov() != nil {
		nad, err := sriovNetAttach(formatSriovNetAttachName(name), *m.k8sCfg.Namespace, n, labels, m.sriovSocketFile)
		if err != nil {
			return fail(err)
		}
		if err := create(nad); err != nil {
			return fail(err)
		}
	}
	// The next query (e.g. RO's status refresh right after create) must not report the network as missing.
	if err := k8s.WaitForCached(ctx, m.client, m.cacheSyncTimeout, created...); err != nil {
		return fail(fmt.Errorf("wait for network '%s' in cache: %w", networkId, err))
	}
	return networkFromObjects(primary, sub)
}

// rollback deletes objects created by a failed network create, by name and in reverse order.
func (m *manager) rollback(ctx context.Context, objs []client.Object) error {
	ctx = context.WithoutCancel(ctx)
	var errs []error
	for i := len(objs) - 1; i >= 0; i-- {
		if err := m.client.Delete(ctx, objs[i]); err != nil && !k8s_errors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete %T '%s': %w", objs[i], objs[i].GetName(), err))
		}
	}
	return errors.Join(errs...)
}

// networkObjects are the kube-vim-owned objects backing one network.
type networkObjects struct {
	// primary is the network's Vpc or Vlan.
	primary client.Object
	// subnet is the network's Kube-OVN subnet; nil when it has no subnets.
	subnet *kubeovnv1.Subnet
	nads   []netattv1.NetworkAttachmentDefinition
}

func (m *manager) getNetworkObjects(ctx context.Context, networkId string) (*networkObjects, error) {
	byId := client.MatchingFields{network.K8sNetworkIdLabel: networkId}
	res := &networkObjects{}
	vpcs := &kubeovnv1.VpcList{}
	if err := m.client.List(ctx, vpcs, byId); err != nil {
		return nil, fmt.Errorf("list kube-ovn vpcs of network '%s': %w", networkId, err)
	}
	vlans := &kubeovnv1.VlanList{}
	if err := m.client.List(ctx, vlans, byId); err != nil {
		return nil, fmt.Errorf("list kube-ovn vlans of network '%s': %w", networkId, err)
	}
	switch {
	case len(vpcs.Items)+len(vlans.Items) == 0:
		return nil, &apperrors.ErrNotFound{Entity: "network", Identifier: networkId}
	case len(vpcs.Items)+len(vlans.Items) > 1:
		return nil, fmt.Errorf("%d vpcs and vlans carry network id '%s': %w", len(vpcs.Items)+len(vlans.Items), networkId, apperrors.ErrInternal)
	case len(vpcs.Items) == 1:
		res.primary = &vpcs.Items[0]
	default:
		res.primary = &vlans.Items[0]
	}
	subnets := &kubeovnv1.SubnetList{}
	if err := m.client.List(ctx, subnets, byId); err != nil {
		return nil, fmt.Errorf("list kube-ovn subnets of network '%s': %w", networkId, err)
	}
	switch len(subnets.Items) {
	case 0:
	case 1:
		res.subnet = &subnets.Items[0]
	default:
		return nil, fmt.Errorf("%d kube-ovn subnets carry network id '%s': %w", len(subnets.Items), networkId, apperrors.ErrInternal)
	}
	nads := &netattv1.NetworkAttachmentDefinitionList{}
	if err := m.client.List(ctx, nads, client.InNamespace(*m.k8sCfg.Namespace), byId); err != nil {
		return nil, fmt.Errorf("list network attachment definitions of network '%s': %w", networkId, err)
	}
	res.nads = nads.Items
	return res, nil
}

func (m *manager) GetNetwork(ctx context.Context, networkId string) (*networkv1.Network, error) {
	objs, err := m.getNetworkObjects(ctx, networkId)
	if err != nil {
		return nil, err
	}
	return networkFromObjects(objs.primary, objs.subnet)
}

func (m *manager) ListNetworks(ctx context.Context) ([]*networkv1.Network, error) {
	managed := client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}
	vpcs := &kubeovnv1.VpcList{}
	if err := m.client.List(ctx, vpcs, managed); err != nil {
		return nil, fmt.Errorf("list kube-ovn vpcs: %w", err)
	}
	vlans := &kubeovnv1.VlanList{}
	if err := m.client.List(ctx, vlans, managed); err != nil {
		return nil, fmt.Errorf("list kube-ovn vlans: %w", err)
	}
	subnets := &kubeovnv1.SubnetList{}
	if err := m.client.List(ctx, subnets, managed); err != nil {
		return nil, fmt.Errorf("list kube-ovn subnets: %w", err)
	}
	subnetByNetwork := make(map[string]*kubeovnv1.Subnet, len(subnets.Items))
	for idx := range subnets.Items {
		subnetByNetwork[subnets.Items[idx].Labels[network.K8sNetworkIdLabel]] = &subnets.Items[idx]
	}
	primaries := make([]client.Object, 0, len(vpcs.Items)+len(vlans.Items))
	for idx := range vpcs.Items {
		primaries = append(primaries, &vpcs.Items[idx])
	}
	for idx := range vlans.Items {
		primaries = append(primaries, &vlans.Items[idx])
	}
	res := make([]*networkv1.Network, 0, len(primaries))
	for _, primary := range primaries {
		n, err := networkFromObjects(primary, subnetByNetwork[primary.GetLabels()[network.K8sNetworkIdLabel]])
		if err != nil {
			return nil, err
		}
		res = append(res, n)
	}
	return res, nil
}

// Delete the network and every object backing it. The management network cannot be deleted.
func (m *manager) DeleteNetwork(ctx context.Context, networkId string) error {
	objs, err := m.getNetworkObjects(ctx, networkId)
	if err != nil {
		return err
	}
	if objs.primary.GetLabels()[network.K8sManagementNetworkLabel] == "true" {
		return &apperrors.ErrInvalidArgument{Field: "networkId", Reason: "the management network cannot be deleted"}
	}
	var toDelete []client.Object
	for idx := range objs.nads {
		toDelete = append(toDelete, &objs.nads[idx])
	}
	if objs.subnet != nil {
		toDelete = append(toDelete, objs.subnet)
	}
	// Lookups go through the Vpc or Vlan, so it goes last and a failed delete can be retried.
	toDelete = append(toDelete, objs.primary)
	for _, obj := range toDelete {
		if err := m.client.Delete(ctx, obj); err != nil && !k8s_errors.IsNotFound(err) {
			return fmt.Errorf("delete %T '%s' of network '%s': %w", obj, obj.GetName(), networkId, err)
		}
	}
	return nil
}

func (m *manager) GetSubnet(ctx context.Context, subnetId string) (*networkv1.Subnet, error) {
	for _, v := range []string{network.K8sSubnetIdIPv4Label, network.K8sSubnetIdIPv6Label} {
		subnets := &kubeovnv1.SubnetList{}
		if err := m.client.List(ctx, subnets, client.MatchingFields{v: subnetId}); err != nil {
			return nil, fmt.Errorf("list kube-ovn subnets for subnet '%s': %w", subnetId, err)
		}
		switch len(subnets.Items) {
		case 0:
			continue
		case 1:
			sub := &subnets.Items[0]
			for _, version := range subnetVersions(sub) {
				if sub.Labels[subnetIdLabel(version)] == subnetId {
					return subnetFromKubeovn(sub, version)
				}
			}
		}
		return nil, fmt.Errorf("%d kube-ovn subnets carry subnet id '%s': %w", len(subnets.Items), subnetId, apperrors.ErrInternal)
	}
	return nil, &apperrors.ErrNotFound{Entity: "subnet", Identifier: subnetId}
}

func (m *manager) Attachment(ctx context.Context, networkId string, nicType string) (*network.Attachment, error) {
	objs, err := m.getNetworkObjects(ctx, networkId)
	if err != nil {
		return nil, err
	}
	res := &network.Attachment{}
	for _, nad := range objs.nads {
		if nad.Labels[network.K8sNicTypeLabel] == nicType {
			res.NetAttachName = nad.Name
		}
	}
	if res.NetAttachName == "" {
		reason := "has no subnets; it takes only SRIOV interfaces"
		if nicType == network.NicTypeSriov {
			reason = "has no sriov settings"
		}
		return nil, &apperrors.ErrInvalidArgument{Field: fmt.Sprintf("network '%s'", networkId), Reason: reason}
	}
	if objs.subnet != nil {
		res.Cidrs = strings.Split(objs.subnet.Spec.CIDRBlock, ",")
		if nicType == network.NicTypeBridge {
			res.LogicalSwitch = objs.subnet.Name
		}
	}
	return res, nil
}

func (m *manager) ListSubnets(ctx context.Context) ([]*networkv1.Subnet, error) {
	subnets := &kubeovnv1.SubnetList{}
	if err := m.client.List(ctx, subnets, client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
		return nil, fmt.Errorf("list kube-ovn subnets: %w", err)
	}
	var res []*networkv1.Subnet
	for idx := range subnets.Items {
		sub := &subnets.Items[idx]
		for _, v := range subnetVersions(sub) {
			s, err := subnetFromKubeovn(sub, v)
			if err != nil {
				return nil, err
			}
			res = append(res, s)
		}
	}
	return res, nil
}
