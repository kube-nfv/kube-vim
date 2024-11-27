package compute

import "github.com/kube-nfv/kube-vim-api/pb/nfv"

type Manager interface {
	AllocateComputeResource(*nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error)
}
