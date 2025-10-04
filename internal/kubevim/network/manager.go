package network

import (
	"context"
	"fmt"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
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
	CreateNetwork(context.Context, string /*name*/, *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error)
	GetNetwork(context.Context, ...GetNetworkOpt) (*vivnfm.VirtualNetwork, error)
	ListNetworks(context.Context) ([]*vivnfm.VirtualNetwork, error)
	DeleteNetwork(context.Context, ...GetNetworkOpt) error

	CreateSubnet(context.Context, string /*name*/, *vivnfm.NetworkSubnetData) (*vivnfm.NetworkSubnet, error)
	GetSubnet(context.Context, ...GetSubnetOpt) (*vivnfm.NetworkSubnet, error)
	ListSubnets(context.Context) ([]*vivnfm.NetworkSubnet, error)
	DeleteSubnet(context.Context, ...GetSubnetOpt) error
}

func NetworkTypeStrToNfvType(networkTypeStr string) (*nfvcommon.NetworkType, error) {
	typeVal, ok := nfvcommon.NetworkResourceType_value[networkTypeStr]
	if !ok {
		return nil, fmt.Errorf("invalid networkType \"%s\"", networkTypeStr)
	}
	return (*nfvcommon.NetworkType)(&typeVal), nil
}

type GetNetworkOpt func(*getNetworkOpts)
type getNetworkOpts struct {
	Name string
	Uid  *nfvcommon.Identifier
}

func GetNetworkByName(name string) GetNetworkOpt {
	return func(gno *getNetworkOpts) { gno.Name = name }
}
func GetNetworkByUid(uid *nfvcommon.Identifier) GetNetworkOpt {
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
	Name          string
	Uid           *nfvcommon.Identifier
	NetAttachName string
	NetId         *nfvcommon.Identifier
	IPAddress     *nfvcommon.IPAddress
}

func GetSubnetByName(name string) GetSubnetOpt {
	return func(gso *getSubnetOpts) { gso.Name = name }
}
func GetSubnetByUid(uid *nfvcommon.Identifier) GetSubnetOpt {
	return func(gso *getSubnetOpts) { gso.Uid = uid }
}
func GetSubnetByNetAttachName(netAttachName string) GetSubnetOpt {
	return func(gso *getSubnetOpts) { gso.NetAttachName = netAttachName }
}

// Returns the subnet from VPC ID and IP address which should belongs to the subnet
func GetSubnetByNetworkIP(netId *nfvcommon.Identifier, ip *nfvcommon.IPAddress) GetSubnetOpt {
	return func(gso *getSubnetOpts) {
		gso.NetId = netId
		gso.IPAddress = ip
	}
}

func ApplyGetSubnetOpts(gso ...GetSubnetOpt) *getSubnetOpts {
	res := &getSubnetOpts{}
	for _, opt := range gso {
		opt(res)
	}
	return res
}
