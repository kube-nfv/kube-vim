package kubevirt

import (
	"context"
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	networkmock "github.com/kube-nfv/kube-vim/internal/kubevim/network/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newResolver(t *testing.T) (*ipamResolver, *networkmock.MockManager) {
	t.Helper()
	nm := networkmock.NewMockManager(gomock.NewController(t))
	return newIpamResolver(nm, k8stest.TestNamespace), nm
}

func TestSubnetIpam(t *testing.T) {
	t.Parallel()
	subnetId := k8stest.ID("sub1")

	t.Run("no ipam yields dynamic allocation for the subnet", func(t *testing.T) {
		r, _ := newResolver(t) // no calls expected
		got, err := r.subnetIpam(context.Background(), subnetId, nil)
		require.NoError(t, err)
		assert.Equal(t, subnetId, got.SubnetId)
		assert.Nil(t, got.IpAddress, "dynamic IP")
		assert.Nil(t, got.MacAddress, "dynamic MAC")
	})

	t.Run("ipam without static ip is returned as-is", func(t *testing.T) {
		r, _ := newResolver(t) // no subnet lookup for a dynamic IPAM
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{SubnetId: subnetId}
		got, err := r.subnetIpam(context.Background(), subnetId, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam})
		require.NoError(t, err)
		assert.Same(t, ipam, got)
	})

	t.Run("static ip inside the subnet cidr is accepted", func(t *testing.T) {
		r, nm := newResolver(t)
		nm.EXPECT().GetSubnet(gomock.Any(), gomock.Any()).
			Return(&vivnfm.NetworkSubnet{ResourceId: subnetId, Cidr: &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}}, nil)
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{SubnetId: subnetId, IpAddress: &nfvcommon.IPAddress{Ip: "10.0.0.5"}}
		got, err := r.subnetIpam(context.Background(), subnetId, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam})
		require.NoError(t, err)
		assert.Same(t, ipam, got)
	})

	t.Run("static ip outside the subnet cidr is rejected", func(t *testing.T) {
		r, nm := newResolver(t)
		nm.EXPECT().GetSubnet(gomock.Any(), gomock.Any()).
			Return(&vivnfm.NetworkSubnet{ResourceId: subnetId, Cidr: &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}}, nil)
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{SubnetId: subnetId, IpAddress: &nfvcommon.IPAddress{Ip: "192.168.1.5"}}
		_, err := r.subnetIpam(context.Background(), subnetId, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam})
		require.Error(t, err)
	})
}

func TestNetworkIpam(t *testing.T) {
	t.Parallel()
	networkId := k8stest.ID("net1")

	t.Run("ipam with subnet id delegates to subnet ipam", func(t *testing.T) {
		r, _ := newResolver(t) // dynamic subnet IPAM needs no lookup
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{NetworkId: networkId, SubnetId: k8stest.ID("sub1")}
		got, err := r.networkIpam(context.Background(), networkId, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam}, false)
		require.NoError(t, err)
		assert.Equal(t, "sub1", got.SubnetId.GetValue())
	})

	t.Run("no ipam with returnIfNoIpam errors", func(t *testing.T) {
		r, _ := newResolver(t) // short-circuits before any network lookup
		_, err := r.networkIpam(context.Background(), networkId, nil, true)
		assert.ErrorIs(t, err, ErrIPAMConfigurationMissing)
	})

	t.Run("no ipam falls back to the network's first subnet", func(t *testing.T) {
		r, nm := newResolver(t)
		nm.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).
			Return(&vivnfm.VirtualNetwork{SubnetId: []*nfvcommon.Identifier{k8stest.ID("sub9")}}, nil)
		got, err := r.networkIpam(context.Background(), networkId, nil, false)
		require.NoError(t, err)
		assert.Equal(t, "sub9", got.SubnetId.GetValue())
		assert.Nil(t, got.IpAddress)
	})

	t.Run("no ipam and network has no subnets is rejected", func(t *testing.T) {
		r, nm := newResolver(t)
		nm.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).Return(&vivnfm.VirtualNetwork{}, nil)
		_, err := r.networkIpam(context.Background(), networkId, nil, false)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestInitSriovNetwork(t *testing.T) {
	t.Parallel()
	t.Run("builds multus network and sriov interface with mac", func(t *testing.T) {
		netInst := &vivnfm.VirtualNetwork{
			NetworkResourceId: k8stest.ID("net1"),
			NetworkType:       nfvcommon.NetworkType_NETWORK_TYPE_SRIOV,
			Metadata:          &nfvcommon.Metadata{Fields: map[string]string{network.K8sNetworkNetAttachNameLabel: "sriov-nad"}},
		}
		ipams := []*vivnfm.VirtualNetworkInterfaceIPAM{{NetworkId: k8stest.ID("net1"), MacAddress: &nfvcommon.MacAddress{Mac: "aa:bb:cc:dd:ee:ff"}}}
		net, iface, _, err := initSriovNetwork(netInst, ipams)
		require.NoError(t, err)
		require.NotNil(t, net.Multus)
		assert.Equal(t, "sriov-nad", net.Multus.NetworkName)
		require.NotNil(t, iface.SRIOV)
		assert.Equal(t, "aa:bb:cc:dd:ee:ff", iface.MacAddress)
	})

	t.Run("missing netattach label is unsupported", func(t *testing.T) {
		netInst := &vivnfm.VirtualNetwork{NetworkResourceId: k8stest.ID("net1"), Metadata: &nfvcommon.Metadata{Fields: map[string]string{}}}
		_, _, _, err := initSriovNetwork(netInst, nil)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("nil metadata is unsupported", func(t *testing.T) {
		_, _, _, err := initSriovNetwork(&vivnfm.VirtualNetwork{}, nil)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})
}

func TestInitNetwork(t *testing.T) {
	t.Parallel()
	subnet := &vivnfm.NetworkSubnet{
		ResourceId: k8stest.ID("sub1"),
		Metadata: &nfvcommon.Metadata{Fields: map[string]string{
			network.K8sSubnetNetAttachNameLabel: "sub1-netattach",
			network.K8sSubnetNameLabel:          "sub1",
		}},
	}

	t.Run("builds multus network with logical switch and address annotations", func(t *testing.T) {
		r, nm := newResolver(t)
		nm.EXPECT().GetSubnet(gomock.Any(), gomock.Any()).Return(subnet, nil)
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{
			SubnetId:   k8stest.ID("sub1"),
			IpAddress:  &nfvcommon.IPAddress{Ip: "10.0.0.5"},
			MacAddress: &nfvcommon.MacAddress{Mac: "aa:bb:cc:dd:ee:ff"},
		}
		net, iface, ann, err := r.initNetwork(context.Background(), ipam)
		require.NoError(t, err)
		require.NotNil(t, net.Multus)
		assert.Equal(t, "sub1-netattach", net.Multus.NetworkName)
		require.NotNil(t, iface.Bridge)
		ns := k8stest.TestNamespace
		assert.Equal(t, "sub1", ann["sub1-netattach."+ns+".ovn.kubernetes.io/logical_switch"])
		assert.Equal(t, "10.0.0.5", ann["sub1-netattach."+ns+".ovn.kubernetes.io/ip_address"])
		assert.Equal(t, "aa:bb:cc:dd:ee:ff", ann["sub1-netattach."+ns+".ovn.kubernetes.io/mac_address"])
	})

	t.Run("ipam without a subnet id is rejected", func(t *testing.T) {
		r, _ := newResolver(t) // rejected before any lookup
		_, _, _, err := r.initNetwork(context.Background(), &vivnfm.VirtualNetworkInterfaceIPAM{})
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestResolveInterfaces(t *testing.T) {
	t.Parallel()
	t.Run("always prepends the pod network", func(t *testing.T) {
		r, _ := newResolver(t)
		nets, ifaces, _, err := r.resolveInterfaces(context.Background(), nil, nil)
		require.NoError(t, err)
		require.Len(t, nets, 1)
		require.Len(t, ifaces, 1)
		assert.NotNil(t, nets[0].Pod)
		assert.NotNil(t, ifaces[0].Masquerade)
	})

	t.Run("interface without network or subnet id is rejected", func(t *testing.T) {
		r, _ := newResolver(t)
		data := []*vivnfm.VirtualNetworkInterfaceData{{}}
		_, _, _, err := r.resolveInterfaces(context.Background(), data, nil)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("sriov network id dispatches to the sriov interface builder", func(t *testing.T) {
		r, nm := newResolver(t)
		nm.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).Return(&vivnfm.VirtualNetwork{
			NetworkResourceId: k8stest.ID("net1"),
			NetworkType:       nfvcommon.NetworkType_NETWORK_TYPE_SRIOV,
			Metadata:          &nfvcommon.Metadata{Fields: map[string]string{network.K8sNetworkNetAttachNameLabel: "sriov-nad"}},
		}, nil)
		data := []*vivnfm.VirtualNetworkInterfaceData{{NetworkId: k8stest.ID("net1")}}
		nets, ifaces, _, err := r.resolveInterfaces(context.Background(), data, nil)
		require.NoError(t, err)
		require.Len(t, ifaces, 2) // pod + sriov
		assert.NotNil(t, ifaces[1].SRIOV)
		assert.Equal(t, "sriov-nad", nets[1].Multus.NetworkName)
	})
}
