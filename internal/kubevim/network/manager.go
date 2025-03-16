package network

import (
	"context"
	"fmt"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
	K8sNetworkNameLabel         = "network.kubevim.kubenfv.io/network-name"
	K8sNetworkIdLabel           = "network.kubevim.kubenfv.io/network-id"
	K8sNetworkTypeLabel         = "network.kubevim.kubenfv.io/netowrk-type"
	K8sSubnetNameLabel          = "network.kubevim.kubenfv.io/subnet-name"
	K8sSubnetIdLabel            = "network.kubevim.kubenfv.io/subnet-id"
	K8sSubnetNetAttachNameLabel = "network.kubevim.kubenfv.io/subnet-netattach-name"
)

type Manager interface {
	CreateNetwork(context.Context, string /*name*/, *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error)
	GetNetwork(context.Context, ...GetNetworkOpt) (*nfv.VirtualNetwork, error)
	ListNetworks(context.Context) ([]*nfv.VirtualNetwork, error)
	DeleteNetwork(context.Context, ...GetNetworkOpt) error

	CreateSubnet(context.Context, string /*name*/, *nfv.NetworkSubnetData) (*nfv.NetworkSubnet, error)
	GetSubnet(context.Context, ...GetSubnetOpt) (*nfv.NetworkSubnet, error)
	ListSubnets(context.Context) ([]*nfv.NetworkSubnet, error)
	DeleteSubnet(context.Context, ...GetSubnetOpt) error
}

func NetworkTypeStrToNfvType(networkTypeStr string) (*nfv.NetworkType, error) {
	typeVal, ok := nfv.NetworkResourceType_value[networkTypeStr]
	if !ok {
		return nil, fmt.Errorf("invalid networkType \"%s\"", networkTypeStr)
	}
	return (*nfv.NetworkType)(&typeVal), nil
}

type GetNetworkOpt func(*getNetworkOpts)
type getNetworkOpts struct {
	Name string
	Uid  *nfv.Identifier
}

func GetNetworkByName(name string) GetNetworkOpt {
	return func(gno *getNetworkOpts) { gno.Name = name }
}
func GetNetworkByUid(uid *nfv.Identifier) GetNetworkOpt {
	return func(gno *getNetworkOpts) { gno.Uid = uid }
}
func ApplyGetNetworkOpts(gno ...GetNetworkOpt) *getNetworkOpts {
	res := &getNetworkOpts{}
	for _, opt := range gno {
		opt(res)
	}
	return res
}

type GetSubnetOpt func(*getSubnetOpts)
type getSubnetOpts struct {
	Name string
	Uid  *nfv.Identifier
}

func GetSubnetByName(name string) GetSubnetOpt {
	return func(gso *getSubnetOpts) { gso.Name = name }
}
func GetSubnetByUid(uid *nfv.Identifier) GetSubnetOpt {
	return func(gso *getSubnetOpts) { gso.Uid = uid }
}
func ApplyGetSubnetOpts(gso ...GetSubnetOpt) *getSubnetOpts {
	res := &getSubnetOpts{}
	for _, opt := range gso {
		opt(res)
	}
	return res
}
