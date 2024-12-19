package kubeovn

import (
	"context"
	"fmt"
	"strconv"

	"github.com/DiMalovanyy/kube-vim/internal/config"
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
		subnetsToCreate = append(subnetsToCreate, subnet)
	}
	createdVpc, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(ctx, vpc, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-ovn Vpc k8s object: %w", err)
	}
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
                        network.K8sSubnetName: subnet.GetName(),
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
		if _, err := m.kubeOvnClient.KubeovnV1().Subnets().Create(ctx, subnet, v1.CreateOptions{}); err != nil {
            m.DeleteNetwork(ctx, name)
			return nil, fmt.Errorf("failed to create kubeovn subnet \"%s\": %w", subnet.GetName(), err)
		}

	}
	return kubeovnVpcToNfvNetwork(createdVpc)
}

func (m *manager) DeleteNetwork(ctx context.Context, name string) error {
    return nil
}

