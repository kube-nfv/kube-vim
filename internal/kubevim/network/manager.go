package network

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

type Manager interface {
	CreateNetwork(context.Context, string /*name*/, *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error)
}
