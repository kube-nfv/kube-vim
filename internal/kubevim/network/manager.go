package network

import "github.com/kube-nfv/kube-vim-api/pb/nfv"

type Manager interface {
	CreateNetwork(string /*name*/, *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error)
}
