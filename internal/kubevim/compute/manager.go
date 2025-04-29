package compute

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

type Manager interface {
	AllocateComputeResource(context.Context, *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error)
	GetComputeResource(context.Context, ...GetComputeOpt) (*nfv.VirtualCompute, error)
	ListComputeResources(context.Context) ([]*nfv.VirtualCompute, error)
	DeleteComputeResource(context.Context, ...GetComputeOpt) error
}

type GetComputeOpt func (*getComputeOpts)
type getComputeOpts struct {
	Name string
	Uid *nfv.Identifier
}

func GetComputeByName(name string) GetComputeOpt {
	return func(gco *getComputeOpts) { gco.Name = name }
}
func GetComputeByUid(uid *nfv.Identifier) GetComputeOpt {
	return func(gco *getComputeOpts) { gco.Uid = uid }
}
func ApplyGetComputeOpts(gco ...GetComputeOpt) *getComputeOpts {
	res := &getComputeOpts{}
	for _, opt := range gco {
		opt(res)
	}
	return res
}
