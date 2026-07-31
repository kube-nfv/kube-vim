package kubeovn

import (
	"testing"
	"time"

	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// managedMeta returns an ObjectMeta that passes both IsObjectInstantiated and
// IsObjectManagedByKubeNfv checks.
func managedMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:              name,
		UID:               types.UID("uid-" + name),
		ResourceVersion:   "1",
		CreationTimestamp: metav1.NewTime(time.Now()),
		Labels:            map[string]string{common.K8sManagedByLabel: common.KubeNfvName},
	}
}

func TestKubeovnVpcFromNfvNetworkData(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		vpc, err := kubeovnVpcFromNfvNetworkData("net1", &vivnfm.VirtualNetworkData{})
		require.NoError(t, err)
		assert.Equal(t, "net1", vpc.Name)
		assert.Equal(t, common.KubeNfvName, vpc.Labels[common.K8sManagedByLabel])
	})
	t.Run("empty name", func(t *testing.T) {
		_, err := kubeovnVpcFromNfvNetworkData("", &vivnfm.VirtualNetworkData{})
		assert.Error(t, err)
	})
	t.Run("nil data", func(t *testing.T) {
		_, err := kubeovnVpcFromNfvNetworkData("net1", nil)
		assert.Error(t, err)
	})
}

func TestKubeovnVpcToNfvNetwork(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		vpc := &kubeovnv1.Vpc{ObjectMeta: managedMeta("net1")}
		subnets := []*nfvcommon.Identifier{{Value: "sub1"}}
		got, err := kubeovnVpcToNfvNetwork(vpc, subnets)
		require.NoError(t, err)
		assert.Equal(t, "uid-net1", got.NetworkResourceId.GetValue())
		assert.Equal(t, "net1", got.GetNetworkResourceName())
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY, got.NetworkType)
		assert.Equal(t, subnets, got.SubnetId)
	})
	t.Run("nil vpc", func(t *testing.T) {
		_, err := kubeovnVpcToNfvNetwork(nil, nil)
		assert.Error(t, err)
	})
	t.Run("not instantiated", func(t *testing.T) {
		vpc := &kubeovnv1.Vpc{ObjectMeta: metav1.ObjectMeta{
			Name:   "net1",
			UID:    "uid-1",
			Labels: map[string]string{common.K8sManagedByLabel: common.KubeNfvName},
		}}
		_, err := kubeovnVpcToNfvNetwork(vpc, nil)
		assert.Error(t, err)
	})
	t.Run("not managed", func(t *testing.T) {
		vpc := &kubeovnv1.Vpc{ObjectMeta: metav1.ObjectMeta{
			Name:              "net1",
			UID:               "uid-1",
			ResourceVersion:   "1",
			CreationTimestamp: metav1.NewTime(time.Now()),
		}}
		_, err := kubeovnVpcToNfvNetwork(vpc, nil)
		assert.Error(t, err)
	})
}

func TestKubeovnVlanToNfvNetwork(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		vlan := &kubeovnv1.Vlan{
			ObjectMeta: managedMeta("vlan1"),
			Spec:       kubeovnv1.VlanSpec{ID: 100, Provider: "provider1"},
		}
		got, err := kubeovnVlanToNfvNetwork(vlan, nil)
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY, got.NetworkType)
		assert.Equal(t, "provider1", got.GetProviderNetwork())
		assert.Equal(t, uint64(100), got.GetSegmentationId())
	})
	t.Run("nil vlan", func(t *testing.T) {
		_, err := kubeovnVlanToNfvNetwork(nil, nil)
		assert.Error(t, err)
	})
}

func TestKubeovnVlanFromNfvNetworkData(t *testing.T) {
	provider := "provider1"
	segID := uint64(100)
	underlay := nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY
	t.Run("valid", func(t *testing.T) {
		vlan, err := kubeovnVlanFromNfvNetworkData("vlan1", &vivnfm.VirtualNetworkData{
			NetworkType:     &underlay,
			ProviderNetwork: &provider,
			SegmentationId:  &segID,
		})
		require.NoError(t, err)
		assert.Equal(t, 100, vlan.Spec.ID)
		assert.Equal(t, "provider1", vlan.Spec.Provider)
	})
	t.Run("wrong network type", func(t *testing.T) {
		overlay := nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY
		_, err := kubeovnVlanFromNfvNetworkData("vlan1", &vivnfm.VirtualNetworkData{
			NetworkType:     &overlay,
			ProviderNetwork: &provider,
		})
		assert.Error(t, err)
	})
	t.Run("missing provider", func(t *testing.T) {
		_, err := kubeovnVlanFromNfvNetworkData("vlan1", &vivnfm.VirtualNetworkData{NetworkType: &underlay})
		assert.Error(t, err)
	})
	t.Run("empty name", func(t *testing.T) {
		_, err := kubeovnVlanFromNfvNetworkData("", &vivnfm.VirtualNetworkData{NetworkType: &underlay, ProviderNetwork: &provider})
		assert.Error(t, err)
	})
}

func TestKubeovnIpVersionRoundTrip(t *testing.T) {
	v4 := nfvcommon.IPVersion_IPV4
	v6 := nfvcommon.IPVersion_IPV6
	tests := []struct {
		nfv *nfvcommon.IPVersion
		str string
	}{
		{&v4, "IPv4"},
		{&v6, "IPv6"},
	}
	for _, tt := range tests {
		s, err := kubeovnIpVersionFromNfv(tt.nfv)
		require.NoError(t, err)
		assert.Equal(t, tt.str, s)

		back, err := nfvIpversionFromKubeovn(s)
		require.NoError(t, err)
		assert.Equal(t, *tt.nfv, *back)
	}
}

func TestKubeovnIpVersionErrors(t *testing.T) {
	_, err := kubeovnIpVersionFromNfv(nil)
	assert.Error(t, err)

	unknown := nfvcommon.IPVersion(99)
	_, err = kubeovnIpVersionFromNfv(&unknown)
	assert.Error(t, err)

	_, err = nfvIpversionFromKubeovn("")
	assert.Error(t, err)

	_, err = nfvIpversionFromKubeovn("Dual")
	assert.Error(t, err)
}

func TestKubeovnSubnetFromNfvSubnetData(t *testing.T) {
	v4 := nfvcommon.IPVersion_IPV4
	t.Run("valid with defaults", func(t *testing.T) {
		sub, err := kubeovnSubnetFromNfvSubnetData("sub1", &vivnfm.NetworkSubnetData{
			IpVersion: &v4,
			Cidr:      &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"},
		})
		require.NoError(t, err)
		assert.Equal(t, "IPv4", sub.Spec.Protocol)
		assert.Equal(t, "10.0.0.0/24", sub.Spec.CIDRBlock)
		assert.True(t, sub.Spec.EnableDHCP, "dhcp defaults to enabled when unset")
		assert.Empty(t, sub.Spec.Gateway)
	})
	t.Run("dhcp disabled and gateway", func(t *testing.T) {
		dhcp := false
		sub, err := kubeovnSubnetFromNfvSubnetData("sub1", &vivnfm.NetworkSubnetData{
			IpVersion:     &v4,
			Cidr:          &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"},
			IsDhcpEnabled: &dhcp,
			GatewayIp:     &nfvcommon.IPAddress{Ip: "10.0.0.1"},
		})
		require.NoError(t, err)
		assert.False(t, sub.Spec.EnableDHCP)
		assert.Equal(t, "10.0.0.1", sub.Spec.Gateway)
	})
	t.Run("invalid cidr", func(t *testing.T) {
		_, err := kubeovnSubnetFromNfvSubnetData("sub1", &vivnfm.NetworkSubnetData{
			IpVersion: &v4,
			Cidr:      &nfvcommon.IPSubnetCIDR{Cidr: "bad"},
		})
		assert.Error(t, err)
	})
	t.Run("missing cidr", func(t *testing.T) {
		_, err := kubeovnSubnetFromNfvSubnetData("sub1", &vivnfm.NetworkSubnetData{IpVersion: &v4})
		assert.Error(t, err)
	})
	t.Run("invalid gateway", func(t *testing.T) {
		_, err := kubeovnSubnetFromNfvSubnetData("sub1", &vivnfm.NetworkSubnetData{
			IpVersion: &v4,
			Cidr:      &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"},
			GatewayIp: &nfvcommon.IPAddress{Ip: "bad"},
		})
		assert.Error(t, err)
	})
}

func TestNfvNetworkSubnetFromKubeovnSubnet(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		sub := &kubeovnv1.Subnet{
			ObjectMeta: managedMeta("sub1"),
			Spec: kubeovnv1.SubnetSpec{
				Protocol:   "IPv4",
				CIDRBlock:  "10.0.0.0/24",
				Gateway:    "10.0.0.1",
				EnableDHCP: true,
			},
		}
		got, err := nfvNetworkSubnetFromKubeovnSubnet(sub)
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.IPVersion_IPV4, got.IpVersion)
		assert.Equal(t, "10.0.0.0/24", got.Cidr.GetCidr())
		assert.Equal(t, "10.0.0.1", got.GatewayIp.GetIp())
		assert.True(t, got.IsDhcpEnabled)
	})
	t.Run("nil", func(t *testing.T) {
		_, err := nfvNetworkSubnetFromKubeovnSubnet(nil)
		assert.Error(t, err)
	})
	t.Run("not managed", func(t *testing.T) {
		sub := &kubeovnv1.Subnet{ObjectMeta: metav1.ObjectMeta{
			Name:              "sub1",
			UID:               "uid-1",
			ResourceVersion:   "1",
			CreationTimestamp: metav1.NewTime(time.Now()),
		}}
		_, err := nfvNetworkSubnetFromKubeovnSubnet(sub)
		assert.Error(t, err)
	})
}

func TestFormatHelpers(t *testing.T) {
	assert.Equal(t, "net1-subnet-sub1", formatSubnetName("net1", "sub1"))
	assert.Equal(t, "sub1-netattach", formatNetAttachName("sub1"))
	assert.Equal(t, "na1.ns1.ovn", formatNetAttachKubeOvnProvider("na1", "ns1"))
	assert.Contains(t, formatNetAttachConfig("na1", "ns1"), `"provider": "na1.ns1.ovn"`)
	assert.Contains(t, formatNetAttachConfig("na1", "ns1"), `"type": "kube-ovn"`)
}
