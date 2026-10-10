package kubeovn

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"go.uber.org/zap"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
}

func NewKubeovnNetworkManager(cl client.Client, apiReader client.Reader, k8sCfg *config.K8sConfig, logger *zap.Logger) (*manager, error) {
	if k8sCfg.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config k8s.Namespace", Reason: "can't be nil"}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &manager{
		logger:    logger,
		client:    cl,
		apiReader: apiReader,
		k8sCfg:    k8sCfg,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string, networkData *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	if networkData.NetworkType == nil || *networkData.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY {
		net, err := m.createOverlayNetwork(ctx, name, networkData)
		if err != nil {
			return nil, fmt.Errorf("create overlay network '%s': %w", name, err)
		}
		return net, nil
	}
	if *networkData.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY {
		net, err := m.createUnderlayNetwork(ctx, name, networkData)
		if err != nil {
			return nil, fmt.Errorf("create underlay network '%s': %w", name, err)
		}
		return net, nil
	}
	return nil, fmt.Errorf("unsupported network type '%s': %w", networkData.NetworkType, apperrors.ErrUnsupported)
}

// Instantiates virtual subnets for the just-created network vnet. Returns the allocated subnets, and an error if any allocation failed.
func (m *manager) allocateL3Attributes(ctx context.Context, vnet *vivnfm.VirtualNetwork, l3Attributes []*vivnfm.NetworkSubnetData) ([]misc.IdName, error) {
	networkName := vnet.GetNetworkResourceName()
	subnets := make([]misc.IdName, 0, len(l3Attributes))

	for idx, l3attr := range l3Attributes {
		if l3attr.NetworkId == nil || l3attr.NetworkId.Value == "" {
			l3attr.NetworkId = &nfvcommon.Identifier{
				Value: networkName,
			}
		}
		subnetName := formatSubnetName(networkName, strconv.Itoa(idx))
		subnet, err := m.createSubnet(ctx, subnetName, l3attr, vnet)
		if err != nil {
			return subnets, fmt.Errorf("create subnet from l3 attribute index %d for network '%s': %w", idx, networkName, err)
		}
		subnets = append(subnets, misc.IdName{Id: subnet.ResourceId, Name: subnetName})
	}
	return subnets, nil
}

// Deletes the subnets and network object created by a failed network create. Works from the
// created objects rather than lookups, since the cache may not have observed them yet.
func (m *manager) rollbackNetwork(ctx context.Context, netObj client.Object, subnets []misc.IdName) error {
	ctx = context.WithoutCancel(ctx)
	var errs []error
	for _, subnet := range subnets {
		errs = append(errs, m.deleteSubnetObjects(ctx, subnet.Name))
	}
	if err := m.client.Delete(ctx, netObj); err != nil && !k8s_errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete network object '%s': %w", netObj.GetName(), err))
	}
	return errors.Join(errs...)
}

// Deletes the kubeovn subnet and its multus NetworkAttachmentDefinition by name, ignoring objects that are already gone.
func (m *manager) deleteSubnetObjects(ctx context.Context, subnetName string) error {
	var errs []error
	if err := m.client.Delete(ctx, &kubeovnv1.Subnet{ObjectMeta: v1.ObjectMeta{Name: subnetName}}); err != nil && !k8s_errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete kubeovn subnet '%s': %w", subnetName, err))
	}
	nad := &netattv1.NetworkAttachmentDefinition{ObjectMeta: v1.ObjectMeta{Name: formatNetAttachName(subnetName), Namespace: *m.k8sCfg.Namespace}}
	if err := m.client.Delete(ctx, nad); err != nil && !k8s_errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete multus NetworkAttachmentDefinition for subnet '%s': %w", subnetName, err))
	}
	return errors.Join(errs...)
}

func (m *manager) createOverlayNetwork(ctx context.Context, name string, networkData *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	vpc, err := kubeovnVpcFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("convert nfv VirtualNetworkData to kube-ovn Vpc for network '%s': %w", name, err)
	}
	if err := m.client.Create(ctx, vpc); err != nil {
		return nil, fmt.Errorf("create kube-ovn Vpc k8s object '%s': %w", vpc.Name, err)
	}
	vnet, err := kubeovnVpcToNfvNetwork(vpc, nil)
	if err != nil {
		err = fmt.Errorf("convert kubeovn vpc '%s' (id: %s) to nfv VirtualNetwork: %w", vpc.Name, vpc.GetUID(), err)
		return nil, errors.Join(err, m.rollbackNetwork(ctx, vpc, nil))
	}
	subnets, err := m.allocateL3Attributes(ctx, vnet, networkData.Layer3Attributes)
	if err != nil {
		err = fmt.Errorf("create vpc l3 attributes: %w", err)
		if cleanupErr := m.rollbackNetwork(ctx, vpc, subnets); cleanupErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback network '%s': %w", name, cleanupErr))
		}
		return nil, err
	}

	res, err := kubeovnVpcToNfvNetwork(vpc, misc.Identifiers(subnets))
	if err != nil {
		return nil, fmt.Errorf("convert kubeovn vpc '%s' (id: %s) to nfv VirtualNetwork: %w", vpc.Name, vpc.GetUID(), err)
	}
	return res, nil
}

func (m *manager) createUnderlayNetwork(ctx context.Context, name string, networkData *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	// For now the only way to setup the underlay network is setup the vlan on top of the ProviderNetwork. For untagged network vlan should be 0.
	// TODO: Create special managed CRD to manage underlay networks.
	vlan, err := kubeovnVlanFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("convert VirtualNetworkData to kubeovn vlan for network '%s': %w", name, err)
	}
	if err := m.client.Create(ctx, vlan); err != nil {
		return nil, fmt.Errorf("create kubeovn vlan '%s': %w", vlan.Name, err)
	}
	vnet, err := kubeovnVlanToNfvNetwork(vlan, nil)
	if err != nil {
		err = fmt.Errorf("convert kubeovn vlan '%s' (id: %s) to nfv VirtualNetwork: %w", vlan.Name, vlan.GetUID(), err)
		return nil, errors.Join(err, m.rollbackNetwork(ctx, vlan, nil))
	}
	subnets, err := m.allocateL3Attributes(ctx, vnet, networkData.Layer3Attributes)
	if err != nil {
		err = fmt.Errorf("create vlan l3 attributes: %w", err)
		if cleanupErr := m.rollbackNetwork(ctx, vlan, subnets); cleanupErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback network '%s': %w", name, cleanupErr))
		}
		return nil, err
	}

	res, err := kubeovnVlanToNfvNetwork(vlan, misc.Identifiers(subnets))
	if err != nil {
		return nil, fmt.Errorf("convert kubeovn vlan '%s' (id: %s) to nfv VirtualNetwork: %w", vlan.Name, vlan.GetUID(), err)
	}
	return res, nil
}

// Return the network and all l3 attributes that was aquired.
// Works ONLY with the networks created by kube-vim
func (m *manager) GetNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*vivnfm.VirtualNetwork, error) {
	var notFoundErr *apperrors.ErrNotFound
	if net, err := m.getOverlayNetwork(ctx, opts...); err != nil && !k8s_errors.IsNotFound(err) && !errors.As(err, &notFoundErr) {
		return nil, fmt.Errorf("get overlay network: %w", err)
	} else if err == nil {
		return net, nil
	}
	if net, err := m.getUnderlayNetwork(ctx, opts...); err != nil && !k8s_errors.IsNotFound(err) && !errors.As(err, &notFoundErr) {
		return nil, fmt.Errorf("get underlay network: %w", err)
	} else if err == nil {
		return net, nil
	}
	return nil, &apperrors.ErrNotFound{Entity: "network"}
}

func (m *manager) getOverlayNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*vivnfm.VirtualNetwork, error) {
	cfg := network.ApplyGetNetworkOpts(opts...)
	var vpc *kubeovnv1.Vpc
	if cfg.Name != "" {
		vpc = &kubeovnv1.Vpc{}
		if err := m.client.Get(ctx, client.ObjectKey{Name: cfg.Name}, vpc); err != nil {
			return nil, fmt.Errorf("get kubeovn vpc '%s': %w", cfg.Name, err)
		}
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vpcList := &kubeovnv1.VpcList{}
		if err := m.client.List(ctx, vpcList, client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
			return nil, fmt.Errorf("list kubeovn vpcs for id '%s': %w", cfg.Uid.Value, err)
		}
		uid := misc.IdentifierToUID(cfg.Uid)
		for idx := range vpcList.Items {
			vpcRef := &vpcList.Items[idx]
			if vpcRef.GetUID() == uid {
				vpc = vpcRef
				break
			}
		}
		if vpc == nil {
			return nil, &apperrors.ErrNotFound{Entity: "kubeovn vpc", Identifier: cfg.Uid.GetValue()}
		}
	} else {
		return nil, &apperrors.ErrInvalidArgument{Field: "network identifier", Reason: "either name or uid must be specified"}
	}

	subnetIds := []*nfvcommon.Identifier{}
	for _, subnetName := range vpc.Status.Subnets {
		subnet, err := m.GetSubnet(ctx, network.GetSubnetByName(subnetName))
		if err != nil {
			return nil, fmt.Errorf("get subnet '%s' referenced by vpc '%s' (id: %s): %w", subnetName, vpc.Name, vpc.GetUID(), err)
		}
		subnetIds = append(subnetIds, subnet.ResourceId)
	}

	res, err := kubeovnVpcToNfvNetwork(vpc, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("convert kubeovn vpc '%s' (id: %s) to nfv VirtualNetwork: %w", vpc.Name, vpc.GetUID(), err)
	}
	return res, nil
}

func (m *manager) getUnderlayNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*vivnfm.VirtualNetwork, error) {
	cfg := network.ApplyGetNetworkOpts(opts...)
	var vlan *kubeovnv1.Vlan
	if cfg.Name != "" {
		vlan = &kubeovnv1.Vlan{}
		if err := m.client.Get(ctx, client.ObjectKey{Name: cfg.Name}, vlan); err != nil {
			return nil, fmt.Errorf("get kubeovn vlan '%s': %w", cfg.Name, err)
		}
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vlanList := &kubeovnv1.VlanList{}
		if err := m.client.List(ctx, vlanList, client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
			return nil, fmt.Errorf("list kubeovn vlans for id '%s': %w", cfg.Uid.Value, err)
		}
		uid := misc.IdentifierToUID(cfg.Uid)
		for idx := range vlanList.Items {
			vlanRef := &vlanList.Items[idx]
			if vlanRef.GetUID() == uid {
				vlan = vlanRef
				break
			}
		}
		if vlan == nil {
			return nil, &apperrors.ErrNotFound{Entity: "kubeovn vlan", Identifier: cfg.Uid.GetValue()}
		}
	} else {
		return nil, &apperrors.ErrInvalidArgument{Field: "network identifier", Reason: "either name or uid must be specified"}
	}

	subnetIds := []*nfvcommon.Identifier{}
	for _, subnetName := range vlan.Status.Subnets {
		subnet, err := m.GetSubnet(ctx, network.GetSubnetByName(subnetName))
		if err != nil {
			return nil, fmt.Errorf("get subnet '%s' referenced by vlan '%s' (id: %s): %w", subnetName, vlan.Name, vlan.GetUID(), err)
		}
		subnetIds = append(subnetIds, subnet.ResourceId)
	}
	res, err := kubeovnVlanToNfvNetwork(vlan, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("convert kubeovn vlan '%s' (id: %s) to nfv VirtualNetwork: %w", vlan.Name, vlan.GetUID(), err)
	}
	return res, nil
}

func (m *manager) ListNetworks(ctx context.Context) ([]*vivnfm.VirtualNetwork, error) {
	managed := client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}
	vpcList := &kubeovnv1.VpcList{}
	if err := m.client.List(ctx, vpcList, managed); err != nil {
		return nil, fmt.Errorf("list kubeovn vpcs: %w", err)
	}
	vlanList := &kubeovnv1.VlanList{}
	if err := m.client.List(ctx, vlanList, managed); err != nil {
		return nil, fmt.Errorf("list kubeovn vlans: %w", err)
	}
	// List every managed subnet once and index by name, so each network's
	// Status.Subnets resolves to ids from memory instead of a per-subnet Get.
	subnetList := &kubeovnv1.SubnetList{}
	if err := m.client.List(ctx, subnetList, managed); err != nil {
		return nil, fmt.Errorf("list kubeovn subnets: %w", err)
	}
	subnetIdByName := make(map[string]*nfvcommon.Identifier, len(subnetList.Items))
	for idx := range subnetList.Items {
		subnetIdByName[subnetList.Items[idx].Name] = misc.UIDToIdentifier(subnetList.Items[idx].GetUID())
	}
	resolveSubnetIds := func(subnetNames []string) ([]*nfvcommon.Identifier, error) {
		ids := make([]*nfvcommon.Identifier, 0, len(subnetNames))
		for _, sn := range subnetNames {
			id, ok := subnetIdByName[sn]
			if !ok {
				return nil, &apperrors.ErrNotFound{Entity: "kubeovn subnet", Identifier: sn}
			}
			ids = append(ids, id)
		}
		return ids, nil
	}

	res := make([]*vivnfm.VirtualNetwork, 0, len(vpcList.Items)+len(vlanList.Items))
	for idx := range vpcList.Items {
		vpc := &vpcList.Items[idx]
		ids, err := resolveSubnetIds(vpc.Status.Subnets)
		if err != nil {
			return nil, fmt.Errorf("resolve subnets for vpc '%s' (id: %s): %w", vpc.Name, vpc.GetUID(), err)
		}
		net, err := kubeovnVpcToNfvNetwork(vpc, ids)
		if err != nil {
			return nil, fmt.Errorf("convert kubeovn vpc '%s' (id: %s) to nfv VirtualNetwork: %w", vpc.Name, vpc.GetUID(), err)
		}
		res = append(res, net)
	}
	for idx := range vlanList.Items {
		vlan := &vlanList.Items[idx]
		ids, err := resolveSubnetIds(vlan.Status.Subnets)
		if err != nil {
			return nil, fmt.Errorf("resolve subnets for vlan '%s' (id: %s): %w", vlan.Name, vlan.GetUID(), err)
		}
		net, err := kubeovnVlanToNfvNetwork(vlan, ids)
		if err != nil {
			return nil, fmt.Errorf("convert kubeovn vlan '%s' (id: %s) to nfv VirtualNetwork: %w", vlan.Name, vlan.GetUID(), err)
		}
		res = append(res, net)
	}
	return res, nil
}

// Delete the network and all aquired resource (subnets, NetworkAttachmentDefinitions, etc.)
// It will delete the network ONLY if it was created by the kube-vim.
func (m *manager) DeleteNetwork(ctx context.Context, opts ...network.GetNetworkOpt) error {
	net, err := m.GetNetwork(ctx, opts...)
	if err != nil {
		return fmt.Errorf("get network: %w", err)
	}
	for _, subnetId := range net.SubnetId {
		if err := m.DeleteSubnet(ctx, network.GetSubnetByUid(subnetId)); err != nil {
			return fmt.Errorf("delete network subnet with id '%s': %w", subnetId.Value, err)
		}
	}
	if net.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY {
		vpc := &kubeovnv1.Vpc{ObjectMeta: v1.ObjectMeta{Name: *net.NetworkResourceName}}
		if err = m.client.Delete(ctx, vpc); err != nil {
			return fmt.Errorf("delete kubeovn vpc '%s' (id: %s): %w", *net.NetworkResourceName, net.NetworkResourceId.Value, err)
		}
	} else if net.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY {
		vlan := &kubeovnv1.Vlan{ObjectMeta: v1.ObjectMeta{Name: *net.NetworkResourceName}}
		if err = m.client.Delete(ctx, vlan); err != nil {
			return fmt.Errorf("delete kubeovn vlan '%s' (id: %s): %w", *net.NetworkResourceName, net.NetworkResourceId.Value, err)
		}
	} else {
		return fmt.Errorf("unsupported network type '%s': %w", net.NetworkType, apperrors.ErrUnsupported)
	}
	return nil
}

// Creates the kubeovn subnet from the specified vivnfm.NetworkSubnetData.
// If the subnet creation (or convertion) fails all resources (eg. Subnet, multus netowrkAttachmentDefinitions are cleared)
func (m *manager) CreateSubnet(ctx context.Context, name string, subnetData *vivnfm.NetworkSubnetData) (*vivnfm.NetworkSubnet, error) {
	var vnet *vivnfm.VirtualNetwork
	if netId := subnetData.NetworkId; netId != nil && netId.Value != "" {
		opts := []network.GetNetworkOpt{}
		if misc.IsUUID(netId.Value) {
			opts = append(opts, network.GetNetworkByUid(netId))
		} else {
			opts = append(opts, network.GetNetworkByName(netId.Value))
		}
		var err error
		vnet, err = m.GetNetwork(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("get vpc by id '%s': %w", netId.Value, err)
		}
	}
	return m.createSubnet(ctx, name, subnetData, vnet)
}

// Creates the kubeovn subnet and its NetworkAttachmentDefinition attached to vnet (may be nil).
// Takes the network rather than looking it up, so it is safe for a network created moments ago.
func (m *manager) createSubnet(ctx context.Context, name string, subnetData *vivnfm.NetworkSubnetData, vnet *vivnfm.VirtualNetwork) (*vivnfm.NetworkSubnet, error) {
	subnet, err := kubeovnSubnetFromNfvSubnetData(name, subnetData)
	if err != nil {
		return nil, fmt.Errorf("create kubeovn subnet '%s' from NetworkSubnetData: %w", name, err)
	}

	if vnet != nil && vnet.NetworkResourceName != nil {
		if vnet.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY {
			subnet.Spec.Vpc = *vnet.NetworkResourceName
		}
		if vnet.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY {
			subnet.Spec.Vlan = *vnet.NetworkResourceName
		}
		subnet.Labels[network.K8sNetworkNameLabel] = *vnet.NetworkResourceName
		subnet.Labels[network.K8sNetworkIdLabel] = vnet.NetworkResourceId.Value
		subnet.Labels[network.K8sNetworkTypeLabel] = vnet.NetworkType.String()
	}
	netAttachName := formatNetAttachName(subnet.GetName())
	nad := &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{
			Name:      netAttachName,
			Namespace: *m.k8sCfg.Namespace,
			Labels: map[string]string{
				common.K8sManagedByLabel:   common.KubeNfvName,
				network.K8sSubnetNameLabel: subnet.GetName(),
			},
		},
		Spec: netattv1.NetworkAttachmentDefinitionSpec{
			Config: formatNetAttachConfig(netAttachName, *m.k8sCfg.Namespace),
		},
	}
	if err := m.client.Create(ctx, nad); err != nil {
		return nil, fmt.Errorf("create multus network-attachment-definition for subnet '%s': %w", subnet.GetName(), err)
	}
	subnet.Spec.Provider = "ovn"
	subnet.Labels[network.K8sSubnetNetAttachNameLabel] = netAttachName

	cleanupNetAttach := func() error {
		return m.client.Delete(context.WithoutCancel(ctx), &netattv1.NetworkAttachmentDefinition{
			ObjectMeta: v1.ObjectMeta{Name: netAttachName, Namespace: *m.k8sCfg.Namespace},
		})
	}

	if err := m.client.Create(ctx, subnet); err != nil {
		cleanupNetAttach()
		return nil, fmt.Errorf("create kubeovn subnet '%s': %w", subnet.GetName(), err)
	}

	nfvSubnet, err := nfvNetworkSubnetFromKubeovnSubnet(subnet)
	if err != nil {
		err = fmt.Errorf("convert created kubeovn subnet '%s' (id: %s) to vivnfm.NetworkSubnet: %w", subnet.GetName(), subnet.GetUID(), err)
		return nil, errors.Join(err, m.deleteSubnetObjects(context.WithoutCancel(ctx), subnet.GetName()))
	}
	return nfvSubnet, nil
}

func (m *manager) GetSubnet(ctx context.Context, opts ...network.GetSubnetOpt) (*vivnfm.NetworkSubnet, error) {
	cfg := network.ApplyGetSubnetOpts(opts...)
	if cfg.Name != "" {
		subnet := &kubeovnv1.Subnet{}
		if err := m.client.Get(ctx, client.ObjectKey{Name: cfg.Name}, subnet); err != nil {
			return nil, fmt.Errorf("get kubeovn subnet '%s': %w", cfg.Name, err)
		}
		res, err := nfvNetworkSubnetFromKubeovnSubnet(subnet)
		if err != nil {
			return nil, fmt.Errorf("convert kubeovn subnet '%s' (id: %s) to nfv NetworkSubnet: %w", subnet.Name, subnet.GetUID(), err)
		}
		return res, nil
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		subnetList := &kubeovnv1.SubnetList{}
		if err := m.client.List(ctx, subnetList, client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
			return nil, fmt.Errorf("list kubeovn subnets: %w", err)
		}
		uid := misc.IdentifierToUID(cfg.Uid)
		for idx := range subnetList.Items {
			subnetRef := &subnetList.Items[idx]
			if subnetRef.GetUID() == uid {
				res, err := nfvNetworkSubnetFromKubeovnSubnet(subnetRef)
				if err != nil {
					return nil, fmt.Errorf("convert kubeovn subnet '%s' (id: %s) to nfv NetworkSubnet: %w", subnetRef.Name, subnetRef.GetUID(), err)
				}
				return res, nil
			}
		}
		return nil, &apperrors.ErrNotFound{Entity: "kubeovn subnet", Identifier: cfg.Uid.GetValue()}
	} else if cfg.NetAttachName != "" {
		netAttach := &netattv1.NetworkAttachmentDefinition{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: *m.k8sCfg.Namespace, Name: cfg.NetAttachName}, netAttach); err != nil {
			return nil, fmt.Errorf("get network attachment definition '%s': %w", cfg.NetAttachName, err)
		}
		if !misc.IsObjectManagedByKubeNfv(netAttach) {
			return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "NetworkAttachmentDefinition", ObjectName: cfg.NetAttachName, ObjectId: string(netAttach.GetUID())}
		}
		subnetName, ok := netAttach.Labels[network.K8sSubnetNameLabel]
		if !ok {
			return nil, &apperrors.ErrInvalidArgument{Field: fmt.Sprintf("NetworkAttachmentDefinition '%s'", cfg.NetAttachName), Reason: fmt.Sprintf("missing '%s' label", network.K8sSubnetNameLabel)}
		}
		return m.GetSubnet(ctx, network.GetSubnetByName(subnetName))
	} else if cfg.IPAddress != nil && cfg.NetId != nil {
		subnets, err := m.ListSubnets(ctx)
		if err != nil {
			return nil, fmt.Errorf("list subnets: %w", err)
		}
		for _, sub := range subnets {
			if sub.NetworkId.Value == cfg.NetId.Value && network.IpBelongsToCidr(cfg.IPAddress, sub.Cidr) {
				return sub, nil
			}
		}
		return nil, &apperrors.ErrNotFound{Entity: "subnet"}
	}
	return nil, &apperrors.ErrInvalidArgument{Field: "subnet identifier", Reason: "name, uid, net attach name or network and ip must be specified"}
}

func (m *manager) ListSubnets(ctx context.Context) ([]*vivnfm.NetworkSubnet, error) {
	subnetList := &kubeovnv1.SubnetList{}
	if err := m.client.List(ctx, subnetList, client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
		return nil, fmt.Errorf("list kubeovn subnets: %w", err)
	}
	res := make([]*vivnfm.NetworkSubnet, 0, len(subnetList.Items))
	for idx := range subnetList.Items {
		subnetRef := &subnetList.Items[idx]
		nfvSubnet, err := nfvNetworkSubnetFromKubeovnSubnet(subnetRef)
		if err != nil {
			return nil, fmt.Errorf("convert kubeovn subnet '%s' (id: %s) to vivnfm.NetworkSubnet: %w", subnetRef.GetName(), subnetRef.GetUID(), err)
		}
		res = append(res, nfvSubnet)
	}
	return res, nil
}

func (m *manager) DeleteSubnet(ctx context.Context, opts ...network.GetSubnetOpt) error {
	subnet, err := m.GetSubnet(ctx, opts...)
	if err != nil {
		return fmt.Errorf("get subnet: %w", err)
	}
	netAttachName := subnet.Metadata.Fields[network.K8sSubnetNetAttachNameLabel]
	// The only way to get name from the vivnfm.NetworkSubnet resource is to get it by label.
	subnetName := subnet.Metadata.Fields[network.K8sSubnetNameLabel]

	subnetObj := &kubeovnv1.Subnet{ObjectMeta: v1.ObjectMeta{Name: subnetName}}
	if err := m.client.Delete(ctx, subnetObj); err != nil {
		return fmt.Errorf("delete kubeovn subnet '%s' (id: %s): %w", subnetName, subnet.ResourceId.Value, err)
	}
	// delete multus NetworkAttachmentDefinition
	nad := &netattv1.NetworkAttachmentDefinition{ObjectMeta: v1.ObjectMeta{Name: netAttachName, Namespace: *m.k8sCfg.Namespace}}
	if err := m.client.Delete(ctx, nad); err != nil {
		return fmt.Errorf("delete multus NetworkAttachmentDefinition '%s' for subnet '%s': %w", netAttachName, subnet.ResourceId.Value, err)
	}
	return nil
}
