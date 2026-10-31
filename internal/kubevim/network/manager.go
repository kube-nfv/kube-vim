package network

import (
	"context"

	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
)

const (
	// K8sNetworkIdLabel is on every object backing a network.
	K8sNetworkIdLabel = "network.kubevim.kubenfv.io/network-id"
	// K8sSubnetIdIPv4Label and K8sSubnetIdIPv6Label carry the subnet ids on the network's Kube-OVN subnet.
	K8sSubnetIdIPv4Label = "network.kubevim.kubenfv.io/subnet-id-ipv4"
	K8sSubnetIdIPv6Label = "network.kubevim.kubenfv.io/subnet-id-ipv6"
	// K8sNicTypeLabel marks a network's NetworkAttachmentDefinition with the interfaces it serves: bridge or sriov.
	K8sNicTypeLabel = "network.kubevim.kubenfv.io/nic-type"
	// K8sManagementNetworkLabel marks the objects of the kube-vim-managed management network.
	K8sManagementNetworkLabel = "network.kubevim.kubenfv.io/management"

	// K8sNetworkAnnotation holds the network as the consumer created it (protojson, without ids, subnets or outputs).
	K8sNetworkAnnotation = "network.kubevim.kubenfv.io/network"
	// K8sSubnetIPv4Annotation and K8sSubnetIPv6Annotation hold each subnet as the consumer created it.
	K8sSubnetIPv4Annotation = "network.kubevim.kubenfv.io/subnet-ipv4"
	K8sSubnetIPv6Annotation = "network.kubevim.kubenfv.io/subnet-ipv6"

	NicTypeBridge = "bridge"
	NicTypeSriov  = "sriov"
)

// Attachment is what a VM interface needs to attach to a network.
type Attachment struct {
	// NetAttachName is the NetworkAttachmentDefinition in the kube-vim namespace the interface attaches through.
	NetAttachName string
	// LogicalSwitch is the network's Kube-OVN subnet; empty for SR-IOV attachments.
	LogicalSwitch string
	// Cidrs are the CIDRs of the network's subnets.
	Cidrs []string
}

//go:generate go run go.uber.org/mock/mockgen -source=manager.go -destination=mock/mock_manager.go -package=mock

type Manager interface {
	CreateNetwork(context.Context, *networkv1.Network) (*networkv1.Network, error)
	GetNetwork(ctx context.Context, networkId string) (*networkv1.Network, error)
	ListNetworks(context.Context) ([]*networkv1.Network, error)
	DeleteNetwork(ctx context.Context, networkId string) error

	GetSubnet(ctx context.Context, subnetId string) (*networkv1.Subnet, error)
	ListSubnets(context.Context) ([]*networkv1.Subnet, error)

	// Attachment returns how interfaces of nicType (NicTypeBridge or NicTypeSriov) attach to the network.
	Attachment(ctx context.Context, networkId string, nicType string) (*Attachment, error)

	// EnsureManagementNetwork is idempotent: it creates the Vpc + Subnet +
	// NetworkAttachmentDefinition described by cfg if they do not exist, and
	// patches their labels and annotations if they exist with the wrong ones. It
	// never mutates the subnet CIDR or gateway of an existing subnet (workloads
	// might already have IPs from it). Intended to be called once at kube-vim
	// startup. If cfg is nil or cfg.Enabled is false (or unset), the method is a no-op.
	EnsureManagementNetwork(context.Context, *config.ManagementNetworkConfig) error
}
