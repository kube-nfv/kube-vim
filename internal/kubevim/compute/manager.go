package compute

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	// Request Annotation that identify that subnetId might be ommited (even for Overlay networks) and
	// the first (random) subnet will be chosen for network port. Annotation should be present
	// only in AllocateComputeRequest.VirtualNetworkInterfaceData
	// Values should be one of the following:
	//    random - Random subnet will be chosen from the VPC for the network port.
	//    manual - SubnetID should be specified (default)
	KubenfvVmNetworkSubnetAssignmentAnnotation = "compute.kubevim.kubenfv.io/network.subnet.assignment"
	UnknownNetworkSubnetAssigmentAnnotationMsg = "unknown network subnet assignment annotation, should be one of: random, manual"
)

type Manager interface {
	AllocateComputeResource(context.Context, *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error)
	GetComputeResource(context.Context, ...GetComputeOpt) (*nfv.VirtualCompute, error)
	ListComputeResources(context.Context) ([]*nfv.VirtualCompute, error)
	DeleteComputeResource(context.Context, ...GetComputeOpt) error
}

type GetComputeOpt func(*getComputeOpts)
type getComputeOpts struct {
	Name string
	Uid  *nfv.Identifier
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
