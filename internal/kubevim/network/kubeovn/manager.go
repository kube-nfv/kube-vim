package kubeovn

import (
	"context"
	"fmt"

	ovn_client "github.com/DiMalovanyy/kube-vim-api/kube-ovn-api/pkg/client/clientset/versioned"
	"github.com/DiMalovanyy/kube-vim-api/pb/nfv"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Will manage kube-vim networking for VNF using kube-ovn
type manager struct {
    kubeOvnClient *ovn_client.Clientset
}

func NewKubeovnNetworkManager(ctx context.Context, k8sConfig *rest.Config) (*manager, error) {
    c, err := ovn_client.NewForConfig(k8sConfig)
    if err != nil {
        return nil, fmt.Errorf("Failed to create kube-ovn k8s client: %w", err)
    }
    return &manager{
        kubeOvnClient: c,
    }, nil
}

func (m *manager) CreateNetwork(name string, networkData *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error) {
    vpc, err := kubeovnVpcFromNfvNetworkData(name, networkData)
    if err != nil {
        return nil, fmt.Errorf("Failed to convert nfv VirtualNetworkData to kube-ovn Vpc: %w", err)
    }
    createdVpc, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(context.TODO(), vpc, v1.CreateOptions{})
    if err != nil {
        fmt.Errorf("Failed to create kube-ovn Vpc k8s object: %w", err)
    }
    return kubeovnVpcToNfvNetwork(createdVpc)
}
