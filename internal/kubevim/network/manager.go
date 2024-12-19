package network

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
    K8sNetworkLabel = "network.kubevim.kubenfv.io/network-name"
    K8sSubnetName   = "network.kubevim.kubenfv.io/subnet-name"
)

type Manager interface {
	CreateNetwork(context.Context, string /*name*/, *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error)
}
