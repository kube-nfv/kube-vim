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
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config"
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
		return nil, fmt.Errorf("Failed to create kube-ovn k8s client: %w", err)
	}
	netAttC, err := netatt_client.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create multus network-attachment-definition k8s client: %w", err)
	}
	return &manager{
		kubeOvnClient:   ovnC,
		netAttachClient: netAttC,
		namespace:       common.KubeNfvDefaultNamespace,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string, networkData *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error) {
	if networkData.NetworkType == nil || *networkData.NetworkType == nfv.NetworkType_OVERLAY {
		net, err := m.createOverlayNetwork(ctx, name, networkData)
		if err != nil {
			return nil, fmt.Errorf("failed to create overlay network: %w", err)
		}
		return net, nil
	}
	if *networkData.NetworkType == nfv.NetworkType_UNDERLAY {
		net, err := m.createUnderlayNetwork(ctx, name, networkData)
		if err != nil {
			return nil, fmt.Errorf("failed to create underlay network: %w", err)
		}
		return net, nil
	}
	return nil, fmt.Errorf("undefined network type \"%s\"", networkData.NetworkType)
}

// Instantiates virtual subnet for the given network. Returns the Ids of the successfully allocated and error if some of the subnets allocation failed.
func (m *manager) allocateL3Attributes(ctx context.Context, networkName string, l3Attributes []*nfv.NetworkSubnetData) ([]*nfv.Identifier, error) {
	var l3Failed error
	subnetIds := make([]*nfv.Identifier, 0, len(l3Attributes))

	for idx, l3attr := range l3Attributes {
		if l3attr.NetworkId == nil || l3attr.NetworkId.Value == "" {
			l3attr.NetworkId = &nfv.Identifier{
				Value: networkName,
			}
		}
		subnetName := formatSubnetName(networkName, strconv.Itoa(idx))
		subnet, err := m.CreateSubnet(ctx, subnetName, l3attr)
		if err != nil {
			l3Failed = fmt.Errorf("failed to create subnet from vpc l3 attribute with index \"%d\": %w", idx, err)
			break
		}
		subnetIds = append(subnetIds, subnet.ResourceId)
	}
	return subnetIds, l3Failed
}

func (m *manager) createOverlayNetwork(ctx context.Context, name string, networkData *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error) {
	vpc, err := kubeovnVpcFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("Failed to convert nfv VirtualNetworkData to kube-ovn Vpc: %w", err)
	}
	createdVpc, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(ctx, vpc, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-ovn Vpc k8s object: %w", err)
	}
	subnetIds, err := m.allocateL3Attributes(ctx, vpc.Name, networkData.Layer3Attributes)
	if err != nil {
		// Log resource cleanup error
		m.DeleteNetwork(ctx, network.GetNetworkByName(name))
		return nil, fmt.Errorf("failed to create vpc l3 attributes. All created resources cleaned up: %w", err)
	}

	res, err := kubeovnVpcToNfvNetwork(createdVpc, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("failed to convert kubeovn network to the nfv VirtualNetwork: %w", err)
	}
	return res, nil
}

func (m *manager) createUnderlayNetwork(ctx context.Context, name string, networkData *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error) {
	// For now the only way to setup the underlay network is setup the vlan on top of the ProviderNetwork. For untagged network vlan should be 0.
	// TODO: Create special managed CRD to manage underlay networks.
	vlan, err := kubeovnVlanFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VirtualNetworkData to the kubeovn vlan: %w", err)
	}
	createdVlan, err := m.kubeOvnClient.KubeovnV1().Vlans().Create(ctx, vlan, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubeovn vlan from the VirtualNetworkData: %w", err)
	}
	subnetIds, err := m.allocateL3Attributes(ctx, vlan.Name, networkData.Layer3Attributes)
	if err != nil {
		// Log resource cleanup error
		m.DeleteNetwork(ctx, network.GetNetworkByName(name))
		return nil, fmt.Errorf("failed to create vpc l3 attributes. All created resources cleaned up: %w", err)
	}

	res, err := kubeovnVlanToNfvNetwork(createdVlan, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("failed to convert kubeovn network to the nfv VirtualNetwork: %w", err)
	}
	return res, nil
}

// Return the network and all l3 attributes that was aquired.
// Works ONLY with the networks created by kube-vim
func (m *manager) GetNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*nfv.VirtualNetwork, error) {
	if net, err := m.getOverlayNetwork(ctx, opts...); err != nil && !k8s_errors.IsNotFound(err) && !errors.Is(err, common.NotFoundErr) {
		return nil, fmt.Errorf("failed to get overlay network: %w", err)
	} else if err == nil {
		return net, nil
	}
	if net, err := m.getUnderlayNetwork(ctx, opts...); err != nil && !k8s_errors.IsNotFound(err) && !errors.Is(err, common.NotFoundErr) {
		return nil, fmt.Errorf("failed to get underlay network: %w", err)
	} else if err == nil {
		return net, nil
	}
	return nil, fmt.Errorf("network not found: %w", common.NotFoundErr)
}

func (m *manager) getOverlayNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*nfv.VirtualNetwork, error) {
	cfg := network.ApplyGetNetworkOpts(opts...)
	var vpc *kubeovnv1.Vpc
	if cfg.Name != "" {
		var err error
		vpc, err = m.kubeOvnClient.KubeovnV1().Vpcs().Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get kubeovn vpc specified by the network name \"%s\": %w", cfg.Name, err)
		}
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vpcList, err := m.kubeOvnClient.KubeovnV1().Vpcs().List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get list of the kubeovn vpcs to identify vpc with id \"%s\": %w", cfg.Uid.Value, err)
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
			return nil, fmt.Errorf("kubeovn vpc with id \"%s\" not found: %w", cfg.Uid.GetValue(), common.NotFoundErr)
		}
	} else {
		return nil, fmt.Errorf("either network name or uid should be specified to get kubeovn network: %w", common.InvalidArgumentErr)
	}

	subnetIds := []*nfv.Identifier{}
	for _, subnetName := range vpc.Status.Subnets {
		subnet, err := m.GetSubnet(ctx, network.GetSubnetByName(subnetName))
		if err != nil {
			return nil, fmt.Errorf("failed to get subnet \"%s\" references by the vpc \"%s\": %w", subnetName, vpc.Name, err)
		}
		subnetIds = append(subnetIds, subnet.ResourceId)
	}

	res, err := kubeovnVpcToNfvNetwork(vpc, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("failed to convert kubeovn network to the nfv VirtualNetwork: %w", err)
	}
	return res, nil
}

func (m *manager) getUnderlayNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*nfv.VirtualNetwork, error) {
	cfg := network.ApplyGetNetworkOpts(opts...)
	var vlan *kubeovnv1.Vlan
	if cfg.Name != "" {
		var err error
		vlan, err = m.kubeOvnClient.KubeovnV1().Vlans().Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get kubeovn vlan specified by the network name \"%s\": %w", cfg.Name, err)
		}
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vlanList, err := m.kubeOvnClient.KubeovnV1().Vlans().List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get the list of the kubeovn vlans to identify a vlan with an id \"%s\": %w", cfg.Uid.Value, err)
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
			return nil, fmt.Errorf("kubeovn vlan with an id \"%s\" not found: %w", cfg.Uid.GetValue(), common.NotFoundErr)
		}
	} else {
		return nil, fmt.Errorf("either network name or uid should be specified to get kubeovn network: %w", common.InvalidArgumentErr)
	}

	subnetIds := []*nfv.Identifier{}
	for _, subnetName := range vlan.Status.Subnets {
		subnet, err := m.GetSubnet(ctx, network.GetSubnetByName(subnetName))
		if err != nil {
			return nil, fmt.Errorf("failed to get subnet \"%s\" references by the vlan \"%s\": %w", subnetName, vlan.Name, err)
		}
		subnetIds = append(subnetIds, subnet.ResourceId)
	}
	res, err := kubeovnVlanToNfvNetwork(vlan, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("failed to convert kubeovn network to the nfv VirtualNetwork: %w", err)
	}
	return res, nil
}

func (m *manager) ListNetworks(ctx context.Context) ([]*nfv.VirtualNetwork, error) {
	netList, err := m.kubeOvnClient.KubeovnV1().Vpcs().List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list kubeovn vpcs: %w", err)
	}
	res := make([]*nfv.VirtualNetwork, 0, len(netList.Items))
	for _, vpc := range netList.Items {
		netName := vpc.Name
		net, err := m.GetNetwork(ctx, network.GetNetworkByName(netName))
		if err != nil {
			return nil, fmt.Errorf("failed to get kubeovn network with name \"%s\": %e", netName, err)
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
		return fmt.Errorf("failed to get network: %w", err)
	}
	for _, subnetId := range net.SubnetId {
		if err := m.DeleteSubnet(ctx, network.GetSubnetByUid(subnetId)); err != nil {
			return fmt.Errorf("failed to deleted network related subnet with id \"%s\": %w", subnetId.Value, err)
		}
	}
	if net.NetworkType == nfv.NetworkType_OVERLAY {
		if err = m.kubeOvnClient.KubeovnV1().Vpcs().Delete(ctx, *net.NetworkResourceName, v1.DeleteOptions{}); err != nil {
			return fmt.Errorf("failed to delete kubeovn vpc with name \"%s\" and id \"%s\": %w", *net.NetworkResourceName, *&net.NetworkResourceId.Value, err)
		}
	} else if net.NetworkType == nfv.NetworkType_UNDERLAY {
		if err = m.kubeOvnClient.KubeovnV1().Vlans().Delete(ctx, *net.NetworkResourceName, v1.DeleteOptions{}); err != nil {
			return fmt.Errorf("failed to delete kubeovn vlan with name \"%s\" and id \"%s\": %w", *net.NetworkResourceName, *&net.NetworkResourceId.Value, err)
		}
	}
	return fmt.Errorf("network has unidentified network type \"%s\"", net.NetworkType)
}

// Creates the kubeovn subnet from the specified nfv.NetworkSubnetData.
// If the subnet creation (or convertion) fails all resources (eg. Subnet, multus netowrkAttachmentDefinitions are cleared)
func (m *manager) CreateSubnet(ctx context.Context, name string, subnetData *nfv.NetworkSubnetData) (*nfv.NetworkSubnet, error) {
	var vnet *nfv.VirtualNetwork
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
			return nil, fmt.Errorf("failed to get vpc specified by id \"%s\": %w", netId.Value, err)
		}
	}
	subnet, err := kubeovnSubnetFromNfvSubnetData(name, subnetData)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubeovn subnet from specified NetworkSubnetData: %w", err)
	}

	if vnet != nil && vnet.NetworkResourceName != nil {
		if vnet.NetworkType == nfv.NetworkType_OVERLAY {
			subnet.Spec.Vpc = *vnet.NetworkResourceName
		}
		if vnet.NetworkType == nfv.NetworkType_UNDERLAY {
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
		return nil, fmt.Errorf("failed to create multus network-attachment-definition for subnet \"%s\": %w", subnet.GetName(), err)
	}
	subnet.Spec.Provider = formatNetAttachKubeOvnProvider(netAttachName, m.namespace)
	subnet.Labels[network.K8sSubnetNetAttachNameLabel] = netAttachName

	cleanupNetAttach := func() error {
		return m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Delete(ctx, netAttachName, v1.DeleteOptions{})
	}

	createdSubnet, err := m.kubeOvnClient.KubeovnV1().Subnets().Create(ctx, subnet, v1.CreateOptions{})
	if err != nil {
		cleanupNetAttach()
		return nil, fmt.Errorf("failed to create kubeovn subnet \"%s\": %w", subnet.GetName(), err)
	}

	nfvSubnet, err := nfvNetworkSubnetFromKubeovnSubnet(createdSubnet)
	if err != nil {
		// Subnet deletion should also delete nettwork attachment
		m.DeleteSubnet(ctx, network.GetSubnetByUid(misc.UIDToIdentifier(createdSubnet.GetUID())))
		return nil, fmt.Errorf("failed to convert created kubeovn subnet with name \"%s\" and id \"%s\" to the nfv.NetworkSubnet. Subnet will be deleted: %w", createdSubnet.GetName(), createdSubnet.GetUID(), err)
	}
	return nfvSubnet, nil
}

func (m *manager) GetSubnet(ctx context.Context, opts ...network.GetSubnetOpt) (*nfv.NetworkSubnet, error) {
	cfg := network.ApplyGetSubnetOpts(opts...)
	if cfg.Name != "" {
		subnet, err := m.kubeOvnClient.KubeovnV1().Subnets().Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get a kubeovn subnet with name \"%s\": %w", cfg.Name, err)
		}
		return nfvNetworkSubnetFromKubeovnSubnet(subnet)
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		subnetList, err := m.kubeOvnClient.KubeovnV1().Subnets().List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list a kubeovn subnets: %w", err)
		}
		uid := misc.IdentifierToUID(cfg.Uid)
		for idx := range subnetList.Items {
			subnetRef := &subnetList.Items[idx]
			if subnetRef.GetUID() == uid {
				return nfvNetworkSubnetFromKubeovnSubnet(subnetRef)
			}
		}
		return nil, fmt.Errorf("kubeovn subnet with id \"%s\" not found: %w", cfg.Uid.GetValue(), common.NotFoundErr)
	}
	return nil, fmt.Errorf("either subnet name or uid should be specified to get kubeovn subnet: %w", common.InvalidArgumentErr)
}

func (m *manager) ListSubnets(ctx context.Context) ([]*nfv.NetworkSubnet, error) {
	subnetList, err := m.kubeOvnClient.KubeovnV1().Subnets().List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list kubeovn subnets: %w", err)
	}
	res := make([]*nfv.NetworkSubnet, 0, len(subnetList.Items))
	for idx := range subnetList.Items {
		subnetRef := &subnetList.Items[idx]
		nfvSubnet, err := nfvNetworkSubnetFromKubeovnSubnet(subnetRef)
		if err != nil {
			return nil, fmt.Errorf("failed to convert kubeovn subnet with name \"%s\" and id \"%s\" to the nfv.NetworkSubnet: %w", subnetRef.GetName(), subnetRef.GetUID(), err)
		}
		res = append(res, nfvSubnet)
	}
	return res, nil
}

func (m *manager) DeleteSubnet(ctx context.Context, opts ...network.GetSubnetOpt) error {
	subnet, err := m.GetSubnet(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to get subnet: %w", err)
	}
	netAttachName := subnet.Metadata.Fields[network.K8sSubnetNetAttachNameLabel]
	// The only way to get name from the nfv.NetworkSubnet resource is to get it by label.
	subnetName := subnet.Metadata.Fields[network.K8sSubnetNameLabel]

	// delete multus NetworkAttachmentDefinition
	if err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Delete(ctx, netAttachName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete multus NetworkAttachmentDefinition \"%s\" for subnet \"%s\": %w", netAttachName, subnet.ResourceId.Value, err)
	}
	if err := m.kubeOvnClient.KubeovnV1().Subnets().Delete(ctx, subnetName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete kubeovn subnet with name \"%s\" and id \"%s\": %w", subnetName, subnet.ResourceId.Value, err)
	}
	return nil
}
