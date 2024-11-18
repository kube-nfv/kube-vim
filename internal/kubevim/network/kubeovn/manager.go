package kubeovn

import (
	"context"
	"fmt"

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
	createdVpc, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(ctx, vpc, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed to create kube-ovn Vpc k8s object: %w", err)
	}
    for _, l3Attr := range networkData.Layer3Attributes {

    }
	return kubeovnVpcToNfvNetwork(createdVpc)
}
