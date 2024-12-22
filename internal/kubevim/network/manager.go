package network

import (
	"context"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

const (
    K8sNetworkNameLabel         = "network.kubevim.kubenfv.io/network-name"
    K8sNetworkIdLabel           = "network.kubevim.kubenfv.io/network-id"
    K8sSubnetNameLabel          = "network.kubevim.kubenfv.io/subnet-name"
    K8sSubnetIdLabel            = "network.kubevim.kubenfv.io/subnet-id"
    K8sSubnetNetAttachNameLabel = "network.kubevim.kubenfv.io/subnet-netattach-name"
)

type Manager interface {
	CreateNetwork(context.Context, string /*name*/, *nfv.VirtualNetworkData) (*nfv.VirtualNetwork, error)
    GetNetwork(context.Context, ...GetNetworkOpt) (*nfv.VirtualNetwork, error)
    GetSubnet(context.Context, ...GetSubnetOpt) (*nfv.NetworkSubnet, error)
}

type GetNetworkOpt func(*getNetworkOpts)
type getNetworkOpts struct {
    Name string
    Uid *nfv.Identifier
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
    Uid *nfv.Identifier
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
