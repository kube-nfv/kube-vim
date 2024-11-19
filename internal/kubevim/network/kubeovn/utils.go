package kubeovn

import (
	"fmt"
	"net"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/k8s"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
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
				config.K8sManagedByLabel: config.KubeNfvName,
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
		NetworkResourceId:   k8s.UIDToIdentifier(uid),
		NetworkResourceName: &name,
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
	if ipVersion == nfv.IPVersion_IPV4.Enum() {
		return "IPv4", nil
	}
	if ipVersion == nfv.IPVersion_IPV6.Enum() {
		return "IPv6", nil
	}
	return "", fmt.Errorf("unknown ip version: %s", ipVersion)
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
				config.K8sManagedByLabel: config.KubeNfvName,
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
