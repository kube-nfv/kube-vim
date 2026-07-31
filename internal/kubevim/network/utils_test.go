package network

import (
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/stretchr/testify/assert"
)

func TestIpBelongsToCidr(t *testing.T) {
	tests := []struct {
		name string
		ip   *nfvcommon.IPAddress
		cidr *nfvcommon.IPSubnetCIDR
		want bool
	}{
		{"ipv4 inside", &nfvcommon.IPAddress{Ip: "10.0.0.5"}, &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}, true},
		{"ipv4 outside", &nfvcommon.IPAddress{Ip: "10.0.1.5"}, &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}, false},
		{"ipv4 network address", &nfvcommon.IPAddress{Ip: "10.0.0.0"}, &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}, true},
		{"ipv6 inside", &nfvcommon.IPAddress{Ip: "2001:db8::1"}, &nfvcommon.IPSubnetCIDR{Cidr: "2001:db8::/32"}, true},
		{"nil ip", nil, &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}, false},
		{"nil cidr", &nfvcommon.IPAddress{Ip: "10.0.0.5"}, nil, false},
		{"invalid ip", &nfvcommon.IPAddress{Ip: "not-an-ip"}, &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}, false},
		{"invalid cidr", &nfvcommon.IPAddress{Ip: "10.0.0.5"}, &nfvcommon.IPSubnetCIDR{Cidr: "not-a-cidr"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IpBelongsToCidr(tt.ip, tt.cidr))
		})
	}
}
