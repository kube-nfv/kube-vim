package network

import (
	"net"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
)

func IpBelongsToCidr(ip *nfvcommon.IPAddress, cidr *nfvcommon.IPSubnetCIDR) bool {
	if ip == nil || cidr == nil {
		return false
	}
	parsedIP := net.ParseIP(ip.Ip)
	if parsedIP == nil {
		return false
	}
	_, ipNet, err := net.ParseCIDR(cidr.Cidr)
	if err != nil {
		return false
	}
	return ipNet.Contains(parsedIP)
}
