package kubeovn

import (
	"context"
	"fmt"

	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	ovn_client "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/client/clientset/versioned"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Will manage kube-vim networking for VNF using kube-ovn
type manager struct {
	kubeOvnClient *ovn_client.Clientset
}

func NewKubeovnNetworkManager(k8sConfig *rest.Config) (*manager, error) {
	c, err := ovn_client.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to create kube-ovn k8s client: %w", err)
	}
	return &manager{
		kubeOvnClient: c,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string, networkData *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error) {
	vpc, err := kubeovnVpcFromNfvNetworkData(name, networkData)
	if err != nil {
		return nil, fmt.Errorf("Failed to convert nfv VirtualNetworkData to kube-ovn Vpc: %w", err)
	}
	subnetsToCreate := make([]*kubeovnv1.Subnet, 0, len(networkData.Layer3Attributes))
	for idx, l3Attr := range networkData.Layer3Attributes {
		subnet, err := kubeovnSubnetFromNfvSubnetData(fmt.Sprintf("%s-subnet-%d", name, idx), l3Attr)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubeovn network resource from the spicified VirtualNetworkData l3 attribute with index \"%d\"", idx)
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
		if _, err := m.kubeOvnClient.KubeovnV1().Subnets().Create(ctx, subnet, v1.CreateOptions{}); err != nil {
			// TODO: Delete VPC
			return nil, fmt.Errorf("failed to create kubeovn subnet \"%s\": %w", subnet.GetName(), err)
		}
	}
	return kubeovnVpcToNfvNetwork(createdVpc)
}
