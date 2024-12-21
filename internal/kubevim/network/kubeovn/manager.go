package kubeovn

import (
	"context"
	"fmt"
	"strconv"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	k8s_utils "github.com/DiMalovanyy/kube-vim/internal/k8s"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network"
	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netatt_client "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	ovn_client "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/client/clientset/versioned"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Will manage kube-vim networking for VNF using kube-ovn
type manager struct {
	kubeOvnClient *ovn_client.Clientset
    netAttachClient *netatt_client.Clientset
    namespace string
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
		kubeOvnClient: ovnC,
        netAttachClient: netAttC,
        namespace: config.KubeNfvDefaultNamespace,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string, networkData *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error) {
	vpc, err := kubeovnVpcFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("Failed to convert nfv VirtualNetworkData to kube-ovn Vpc: %w", err)
	}
	subnetsToCreate := make([]*kubeovnv1.Subnet, 0, len(networkData.Layer3Attributes))
	for idx, l3Attr := range networkData.Layer3Attributes {
		subnet, err := kubeovnSubnetFromNfvSubnetData(formatSubnetName(name, strconv.Itoa(idx)), l3Attr)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubeovn network resource from the spicified VirtualNetworkData l3 attribute with index \"%d\": %w", idx, err)
		}
		// Add VPC to the subnet
		subnet.Spec.Vpc = vpc.GetName()
        subnet.Labels[network.K8sNetworkNameLabel] = vpc.GetName()
		subnetsToCreate = append(subnetsToCreate, subnet)
	}
	createdVpc, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(ctx, vpc, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-ovn Vpc k8s object: %w", err)
	}
    subnetIds := make([]*nfv.Identifier, 0, len(subnetsToCreate))
	for _, subnet := range subnetsToCreate {
        // Create multus NetworkAttachmentDefinition for each subnet
        netAttachName := formatNetAttachName(subnet.GetName())
        _, err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(m.namespace).Create(
            ctx,
            &netattv1.NetworkAttachmentDefinition{
                ObjectMeta: v1.ObjectMeta{
                    Name: netAttachName,
                    Labels: map[string]string{
                        config.K8sManagedByLabel: config.KubeNfvName,
                        network.K8sSubnetNameLabel: subnet.GetName(),
                        network.K8sNetworkIdLabel : string(subnet.GetUID()),
                    },
                },
                Spec: netattv1.NetworkAttachmentDefinitionSpec{
                    Config: formatNetAttachConfig(netAttachName, m.namespace),
                },
            }, v1.CreateOptions{})
        if err != nil {
            return nil, fmt.Errorf("failed to create multus network-attachment-definition for subnet \"%s\": %w", subnet.GetName(), err)
        }
        // specify the netattach provider for the kube-ovn subnet
        subnet.Spec.Provider = formatNetAttachKubeOvnProvider(netAttachName, m.namespace)
        subnet.Labels[network.K8sNetworkIdLabel] = string(createdVpc.GetUID())
		createdSubnet, err := m.kubeOvnClient.KubeovnV1().Subnets().Create(ctx, subnet, v1.CreateOptions{})
        if err != nil {
            m.DeleteNetwork(ctx, name)
			return nil, fmt.Errorf("failed to create kubeovn subnet \"%s\": %w", subnet.GetName(), err)
		}
        subnetIds = append(subnetIds, k8s_utils.UIDToIdentifier(createdSubnet.GetUID()))
	}
	return &nfv.VirtualNetwork{
		NetworkResourceId:   k8s_utils.UIDToIdentifier(createdVpc.GetUID()),
		NetworkResourceName: &createdVpc.Name,
        SubnetId: subnetIds,
        NetworkPort: []*nfv.VirtualNetworkPort{},
        Bandwidth: 0,
        NetworkType: "flat",
        IsShared: false,
        OperationalState: nfv.OperationalState_ENABLED,
	}, nil
}
func (m *manager) GetNetwork(context.Context, ...network.GetNetworkOpt) (*nfv.VirtualNetwork, error) {

    return nil, nil
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
        uid := k8s_utils.IdentifierToUID(cfg.Uid)
        for idx, _ := range subnetList.Items {
            subnetRef := &subnetList.Items[idx]
            if subnetRef.GetUID() == uid {
                return nfvNetworkSubnetFromKubeovnSubnet(subnetRef)
            }
        }
        return nil, fmt.Errorf("kubeovn subnet with id \"%s\" not found: %w", cfg.Uid.GetValue(), config.NotFoundErr)
    }
    return nil, fmt.Errorf("either subnet name or uid should be specified to get kubeovn subnet: %w", config.InvalidArgumentErr)
}

func (m *manager) DeleteNetwork(ctx context.Context, name string) error {
    return nil
}

