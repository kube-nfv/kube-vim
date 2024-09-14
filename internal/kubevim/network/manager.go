package network

import "github.com/DiMalovanyy/kube-vim-api/pb/nfv"

type Manager interface {
	CreateNetwork(string /*name*/, *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error)
}
