package kubeovn

import (
	"fmt"
	"net"

	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func kubeovnVpcFromNfvNetworkData(name string, nfvNet *nfv.VirtualNetworkData) (*kubeovnv1.Vpc, error) {
	if len(name) == 0 {
		return nil, fmt.Errorf("name can't be empty")
	}
	if nfvNet == nil {
		return nil, fmt.Errorf("network data can't be nil")
	}
	res := &kubeovnv1.Vpc{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
			},
		},
		Spec: kubeovnv1.VpcSpec{},
	}
	return res, nil
}

func kubeovnVpcToNfvNetwork(vpc *kubeovnv1.Vpc) (*nfv.VirtualNetwork, error) {
	uid := vpc.GetUID()
	if len(uid) == 0 {
		return nil, fmt.Errorf("UID for kube-ovn vpc can't be empty")
	}
	name := vpc.GetName()
	if len(name) == 0 {
		return nil, fmt.Errorf("Name for kube-ovn vpc can't be empty")
	}
	return &nfv.VirtualNetwork{
		NetworkResourceId:   misc.UIDToIdentifier(uid),
		NetworkResourceName: &name,
		Bandwidth:           0,
		NetworkType:         "flat",
		IsShared:            false,
		OperationalState:    nfv.OperationalState_ENABLED,
	}, nil
}

// Returns kubeovn IP version string representation of the nfv.IPVersion enum or
// error if it is contains unexpected data.
//
// Kubeovn IPVersion string MUST be one of the: IPv4, IPv6 or Dual
// Note(dmalovan): Dual IPVersion is not yet supported.
func kubeovnIpVersionFromNfv(ipVersion *nfv.IPVersion) (string, error) {
	if ipVersion == nil {
		return "", fmt.Errorf("ip version not specified")
	}
	switch *ipVersion {
	case nfv.IPVersion_IPV4:
		return "IPv4", nil
	case nfv.IPVersion_IPV6:
		return "IPv6", nil
	default:
		return "", fmt.Errorf("unknown ip version: %v", *ipVersion)
	}
}

// Returns nfv.IpVersion enum representation of the kubeovn IP version string or
// error if it is contains unexpected data.
//
// Kubeovn IPVersion string MUST be one of the: IPv4, IPv6 or Dual
// Note(dmalovan): Dual IPVersion is not yet supported.
func nfvIpversionFromKubeovn(ipVersion string) (*nfv.IPVersion, error) {
	if ipVersion == "" {
		return nil, fmt.Errorf("ip version not specified")
	}
	switch ipVersion {
	case "IPv4":
		return nfv.IPVersion_IPV4.Enum(), nil
	case "IPv6":
		return nfv.IPVersion_IPV6.Enum(), nil
	default:
		return nil, fmt.Errorf("unknown ip version: %s", ipVersion)
	}
}

// Returns the kubeovn Subnet k8s object or error if convertation from the
// NetworkSubnetData structure failed.
func kubeovnSubnetFromNfvSubnetData(name string, nfvSubnet *nfv.NetworkSubnetData) (*kubeovnv1.Subnet, error) {
	if len(name) == 0 {
		return nil, fmt.Errorf("name can't be empty")
	}
	if nfvSubnet == nil {
		return nil, fmt.Errorf("subnet data can't be nil")
	}
	ipProto, err := kubeovnIpVersionFromNfv(nfvSubnet.IpVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to convert nfv IPVersion: %w", err)
	}
	var enableDhcp bool
	if nfvSubnet.IsDhcpEnabled == nil || *nfvSubnet.IsDhcpEnabled == true {
		enableDhcp = true
	} else {
		enableDhcp = false
	}
	if nfvSubnet.Cidr == nil {
		return nil, fmt.Errorf("cidr should be specified for the NwtworkSubnetData")
	}
	if _, _, err := net.ParseCIDR(nfvSubnet.Cidr.GetCidr()); err != nil {
		return nil, fmt.Errorf("cidr \"%s\" is in incorrect format: %w", nfvSubnet.Cidr.GetCidr(), err)
	}

	sub := &kubeovnv1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				common.K8sManagedByLabel:   common.KubeNfvName,
				network.K8sSubnetNameLabel: name,
			},
		},
		Spec: kubeovnv1.SubnetSpec{
			Protocol:   ipProto,
			EnableDHCP: enableDhcp,
			CIDRBlock:  nfvSubnet.Cidr.GetCidr(),
			Default:    false,
			// Only distributed gateway currently supported
			GatewayType: "distributed",
			// Enable outgoing NAT by default
			NatOutgoing: true,
		},
	}
	if nfvSubnet.GatewayIp != nil {
		if ip := net.ParseIP(nfvSubnet.GatewayIp.GetIp()); ip == nil {
			return nil, fmt.Errorf("gateway ip \"%s\" is in invalid format", nfvSubnet.GatewayIp.GetIp())
		}
		sub.Spec.Gateway = nfvSubnet.GatewayIp.GetIp()
	}
	if nfvSubnet.AddressPool != nil {
		// Not yet supported
	}
	return sub, nil
}

// Converts the instantiated kubeovn Subnet resource to the nfv.NetworkSubnet.
// TODO(dmalovan): Add address pool if it is exists
func nfvNetworkSubnetFromKubeovnSubnet(kubeovnSub *kubeovnv1.Subnet) (*nfv.NetworkSubnet, error) {
	if kubeovnSub == nil {
		return nil, fmt.Errorf("subnet can't be nil")
	}
	if !misc.IsObjectInstantiated(kubeovnSub) {
		return nil, fmt.Errorf("subnet is not from Kubernetes (likely created manually)")
	}
	if !misc.IsObjectManagedByKubeNfv(kubeovnSub) {
		return nil, fmt.Errorf("subnet \"%s\" with uid \"%s\" is not managed by the kube-nfv", kubeovnSub.GetName(), kubeovnSub.GetUID())
	}

	var optNetworkId *nfv.Identifier
	if networkId, ok := kubeovnSub.Labels[network.K8sNetworkIdLabel]; ok && networkId != "" {
		optNetworkId = &nfv.Identifier{
			Value: networkId,
		}
	}
	ipVersion, err := nfvIpversionFromKubeovn(kubeovnSub.Spec.Protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ip protocol from the kubeovn resource spec: %w", err)
	}
	return &nfv.NetworkSubnet{
		ResourceId: misc.UIDToIdentifier(kubeovnSub.UID),
		NetworkId:  optNetworkId,
		IpVersion:  *ipVersion,
		GatewayIp: &nfv.IPAddress{
			Ip: kubeovnSub.Spec.Gateway,
		},
		Cidr: &nfv.IPSubnetCIDR{
			Cidr: kubeovnSub.Spec.CIDRBlock,
		},
		IsDhcpEnabled: kubeovnSub.Spec.EnableDHCP,
		Metadata: &nfv.Metadata{
			Fields: kubeovnSub.Labels,
		},
	}, nil
}

func formatSubnetName(networkName string, subnetName string) string {
	return fmt.Sprintf("%s-subnet-%s", networkName, subnetName)
}

func formatNetAttachName(subnetName string) string {
	return subnetName + "-netattach"
}

func formatNetAttachConfig(netAttachName string, namespace string) string {
	return fmt.Sprintf(`{
"cniVersion": "0.3.0",
"type": "kube-ovn",
"server_socket": "/run/openvswitch/kube-ovn-daemon.sock",
"provider": "%s"
}`, formatNetAttachKubeOvnProvider(netAttachName, namespace))
}

func formatNetAttachKubeOvnProvider(netAttachName, namespace string) string {
	return fmt.Sprintf("%s.%s.ovn", netAttachName, namespace)
}
