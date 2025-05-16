package network

import (
	"net"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
)

func IpBelongsToCidr(ip *nfv.IPAddress, cidr *nfv.IPSubnetCIDR) bool {
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
