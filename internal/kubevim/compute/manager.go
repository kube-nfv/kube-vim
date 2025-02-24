package compute

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

type Manager interface {
	AllocateComputeResource(context.Context, *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error)
	QueryComputeResource(context.Context) ([]*nfv.VirtualCompute, error)
}
