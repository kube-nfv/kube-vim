package kubeovn

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netatt_client "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	ovn_client "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/client/clientset/versioned"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	"github.com/kube-nfv/kube-vim/internal/config"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Will manage kube-vim networking for VNF using kube-ovn
type manager struct {
	kubeOvnClient   *ovn_client.Clientset
	netAttachClient *netatt_client.Clientset
	namespace       string
}

func NewKubeovnNetworkManager(k8sConfig *rest.Config) (*manager, error) {
	ovnC, err := ovn_client.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create kube-ovn k8s client: %w", err)
	}
	netAttC, err := netatt_client.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create multus network-attachment-definition k8s client: %w", err)
	}
	return &manager{
		kubeOvnClient:   ovnC,
		netAttachClient: netAttC,
		namespace:       common.KubeNfvDefaultNamespace,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string, networkData *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	if networkData.NetworkType == nil || *networkData.NetworkType == nfvcommon.NetworkType_OVERLAY {
		net, err := m.createOverlayNetwork(ctx, name, networkData)
		if err != nil {
			return nil, fmt.Errorf("create overlay network '%s': %w", name, err)
		}
		return net, nil
	}
	if *networkData.NetworkType == nfvcommon.NetworkType_UNDERLAY {
		net, err := m.createUnderlayNetwork(ctx, name, networkData)
		if err != nil {
			return nil, fmt.Errorf("create underlay network '%s': %w", name, err)
		}
		return net, nil
	}
	return nil, fmt.Errorf("unsupported network type '%s': %w", networkData.NetworkType, apperrors.ErrUnsupported)
}

// Instantiates virtual subnet for the given network. Returns the Ids of the successfully allocated and error if some of the subnets allocation failed.
func (m *manager) allocateL3Attributes(ctx context.Context, networkName string, l3Attributes []*vivnfm.NetworkSubnetData) ([]*nfvcommon.Identifier, error) {
	var l3Failed error
	subnetIds := make([]*nfvcommon.Identifier, 0, len(l3Attributes))

	for idx, l3attr := range l3Attributes {
		if l3attr.NetworkId == nil || l3attr.NetworkId.Value == "" {
			l3attr.NetworkId = &nfvcommon.Identifier{
				Value: networkName,
			}
		}
		subnetName := formatSubnetName(networkName, strconv.Itoa(idx))
		subnet, err := m.CreateSubnet(ctx, subnetName, l3attr)
		if err != nil {
			l3Failed = fmt.Errorf("create subnet from l3 attribute index %d for network '%s': %w", idx, networkName, err)
			break
		}
		subnetIds = append(subnetIds, subnet.ResourceId)
	}
	return subnetIds, l3Failed
}

func (m *manager) createOverlayNetwork(ctx context.Context, name string, networkData *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	vpc, err := kubeovnVpcFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("convert nfv VirtualNetworkData to kube-ovn Vpc for network '%s': %w", name, err)
	}
	createdVpc, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(ctx, vpc, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create kube-ovn Vpc k8s object '%s': %w", vpc.Name, err)
	}
	subnetIds, err := m.allocateL3Attributes(ctx, vpc.Name, networkData.Layer3Attributes)
	if err != nil {
		// Log resource cleanup error
		m.DeleteNetwork(ctx, network.GetNetworkByName(name))
		return nil, fmt.Errorf("create vpc l3 attributes (resources cleaned up): %w", err)
	}

	res, err := kubeovnVpcToNfvNetwork(createdVpc, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("convert kubeovn vpc '%s' (id: %s) to nfv VirtualNetwork: %w", createdVpc.Name, createdVpc.GetUID(), err)
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
	createdVlan, err := m.kubeOvnClient.KubeovnV1().Vlans().Create(ctx, vlan, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create kubeovn vlan '%s': %w", vlan.Name, err)
	}
	subnetIds, err := m.allocateL3Attributes(ctx, vlan.Name, networkData.Layer3Attributes)
	if err != nil {
		// Log resource cleanup error
		m.DeleteNetwork(ctx, network.GetNetworkByName(name))
		return nil, fmt.Errorf("create vlan l3 attributes (resources cleaned up): %w", err)
	}

	res, err := kubeovnVlanToNfvNetwork(createdVlan, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("convert kubeovn vlan '%s' (id: %s) to nfv VirtualNetwork: %w", createdVlan.Name, createdVlan.GetUID(), err)
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
		var err error
		vpc, err = m.kubeOvnClient.KubeovnV1().Vpcs().Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get kubeovn vpc '%s': %w", cfg.Name, err)
		}
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vpcList, err := m.kubeOvnClient.KubeovnV1().Vpcs().List(ctx, v1.ListOptions{})
		if err != nil {
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
		var err error
		vlan, err = m.kubeOvnClient.KubeovnV1().Vlans().Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get kubeovn vlan '%s': %w", cfg.Name, err)
		}
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vlanList, err := m.kubeOvnClient.KubeovnV1().Vlans().List(ctx, v1.ListOptions{})
		if err != nil {
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
	netList, err := m.kubeOvnClient.KubeovnV1().Vpcs().List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list kubeovn vpcs: %w", err)
	}
	vlanList, err := m.kubeOvnClient.KubeovnV1().Vlans().List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list kubeovn vlans: %w", err)
	}
	res := make([]*vivnfm.VirtualNetwork, 0, len(netList.Items)+len(vlanList.Items))
	for _, vpc := range netList.Items {
		netName := vpc.Name
		net, err := m.GetNetwork(ctx, network.GetNetworkByName(netName))
		if err != nil {
			return nil, fmt.Errorf("get kubeovn vpc network '%s' (id: %s): %w", netName, vpc.GetUID(), err)
		}
		res = append(res, net)
	}
	for _, vlan := range vlanList.Items {
		netName := vlan.Name
		net, err := m.GetNetwork(ctx, network.GetNetworkByName(netName))
		if err != nil {
			return nil, fmt.Errorf("get kubeovn vlan network '%s' (id: %s): %w", netName, vlan.GetUID(), err)
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
	if net.NetworkType == nfvcommon.NetworkType_OVERLAY {
		if err = m.kubeOvnClient.KubeovnV1().Vpcs().Delete(ctx, *net.NetworkResourceName, v1.DeleteOptions{}); err != nil {
			return fmt.Errorf("delete kubeovn vpc '%s' (id: %s): %w", *net.NetworkResourceName, net.NetworkResourceId.Value, err)
		}
	} else if net.NetworkType == nfvcommon.NetworkType_UNDERLAY {
		if err = m.kubeOvnClient.KubeovnV1().Vlans().Delete(ctx, *net.NetworkResourceName, v1.DeleteOptions{}); err != nil {
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
	subnet, err := kubeovnSubnetFromNfvSubnetData(name, subnetData)
	if err != nil {
		return nil, fmt.Errorf("create kubeovn subnet '%s' from NetworkSubnetData: %w", name, err)
	}

	if vnet != nil && vnet.NetworkResourceName != nil {
		if vnet.NetworkType == nfvcommon.NetworkType_OVERLAY {
			subnet.Spec.Vpc = *vnet.NetworkResourceName
		}
		if vnet.NetworkType == nfvcommon.NetworkType_UNDERLAY {
			subnet.Spec.Vlan = *vnet.NetworkResourceName
		}
		subnet.Labels[network.K8sNetworkNameLabel] = *vnet.NetworkResourceName
		subnet.Labels[network.K8sNetworkIdLabel] = vnet.NetworkResourceId.Value
		subnet.Labels[network.K8sNetworkTypeLabel] = vnet.NetworkType.String()
	}
	netAttachName := formatNetAttachName(subnet.GetName())
	_, err = m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Create(
		ctx,
		&netattv1.NetworkAttachmentDefinition{
			ObjectMeta: v1.ObjectMeta{
				Name: netAttachName,
				Labels: map[string]string{
					common.K8sManagedByLabel:   common.KubeNfvName,
					network.K8sSubnetNameLabel: subnet.GetName(),
				},
			},
			Spec: netattv1.NetworkAttachmentDefinitionSpec{
				Config: formatNetAttachConfig(netAttachName, m.namespace),
			},
		}, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create multus network-attachment-definition for subnet '%s': %w", subnet.GetName(), err)
	}
	subnet.Spec.Provider = formatNetAttachKubeOvnProvider(netAttachName, m.namespace)
	subnet.Labels[network.K8sSubnetNetAttachNameLabel] = netAttachName

	cleanupNetAttach := func() error {
		return m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Delete(ctx, netAttachName, v1.DeleteOptions{})
	}

	createdSubnet, err := m.kubeOvnClient.KubeovnV1().Subnets().Create(ctx, subnet, v1.CreateOptions{})
	if err != nil {
		cleanupNetAttach()
		return nil, fmt.Errorf("create kubeovn subnet '%s': %w", subnet.GetName(), err)
	}

	nfvSubnet, err := nfvNetworkSubnetFromKubeovnSubnet(createdSubnet)
	if err != nil {
		// Subnet deletion should also delete nettwork attachment
		m.DeleteSubnet(ctx, network.GetSubnetByUid(misc.UIDToIdentifier(createdSubnet.GetUID())))
		return nil, fmt.Errorf("convert created kubeovn subnet '%s' (id: %s) to vivnfm.NetworkSubnet (subnet will be deleted): %w", createdSubnet.GetName(), createdSubnet.GetUID(), err)
	}
	return nfvSubnet, nil
}

func (m *manager) GetSubnet(ctx context.Context, opts ...network.GetSubnetOpt) (*vivnfm.NetworkSubnet, error) {
	cfg := network.ApplyGetSubnetOpts(opts...)
	if cfg.Name != "" {
		subnet, err := m.kubeOvnClient.KubeovnV1().Subnets().Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get kubeovn subnet '%s': %w", cfg.Name, err)
		}
		res, err := nfvNetworkSubnetFromKubeovnSubnet(subnet)
		if err != nil {
			return nil, fmt.Errorf("convert kubeovn subnet '%s' (id: %s) to nfv NetworkSubnet: %w", subnet.Name, subnet.GetUID(), err)
		}
		return res, nil
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		subnetList, err := m.kubeOvnClient.KubeovnV1().Subnets().List(ctx, v1.ListOptions{})
		if err != nil {
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
		netAttach, err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Get(ctx, cfg.NetAttachName, v1.GetOptions{})
		if err != nil {
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
	subnetList, err := m.kubeOvnClient.KubeovnV1().Subnets().List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil {
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

	// delete multus NetworkAttachmentDefinition
	if err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Delete(ctx, netAttachName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete multus NetworkAttachmentDefinition '%s' for subnet '%s': %w", netAttachName, subnet.ResourceId.Value, err)
	}
	if err := m.kubeOvnClient.KubeovnV1().Subnets().Delete(ctx, subnetName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete kubeovn subnet '%s' (id: %s): %w", subnetName, subnet.ResourceId.Value, err)
	}
	return nil
}
