package kubevirt

import (
	"context"
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	networkmock "github.com/kube-nfv/kube-vim/internal/kubevim/network/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = "kube-nfv"

func id(v string) *nfvcommon.Identifier { return &nfvcommon.Identifier{Value: v} }

func newNetMock(t *testing.T) *networkmock.MockManager {
	t.Helper()
	return networkmock.NewMockManager(gomock.NewController(t))
}

func TestGetSubnetIpam(t *testing.T) {
	subnetId := id("sub1")

	t.Run("no ipam yields dynamic allocation for the subnet", func(t *testing.T) {
		nm := newNetMock(t) // no calls expected
		got, err := getSubnetIpam(context.Background(), subnetId, nm, nil)
		require.NoError(t, err)
		assert.Equal(t, subnetId, got.SubnetId)
		assert.Nil(t, got.IpAddress, "dynamic IP")
		assert.Nil(t, got.MacAddress, "dynamic MAC")
	})

	t.Run("ipam without static ip is returned as-is", func(t *testing.T) {
		nm := newNetMock(t) // no subnet lookup for a dynamic IPAM
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{SubnetId: subnetId}
		got, err := getSubnetIpam(context.Background(), subnetId, nm, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam})
		require.NoError(t, err)
		assert.Same(t, ipam, got)
	})

	t.Run("static ip inside the subnet cidr is accepted", func(t *testing.T) {
		nm := newNetMock(t)
		nm.EXPECT().GetSubnet(gomock.Any(), gomock.Any()).
			Return(&vivnfm.NetworkSubnet{ResourceId: subnetId, Cidr: &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}}, nil)
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{SubnetId: subnetId, IpAddress: &nfvcommon.IPAddress{Ip: "10.0.0.5"}}
		got, err := getSubnetIpam(context.Background(), subnetId, nm, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam})
		require.NoError(t, err)
		assert.Same(t, ipam, got)
	})

	t.Run("static ip outside the subnet cidr is rejected", func(t *testing.T) {
		nm := newNetMock(t)
		nm.EXPECT().GetSubnet(gomock.Any(), gomock.Any()).
			Return(&vivnfm.NetworkSubnet{ResourceId: subnetId, Cidr: &nfvcommon.IPSubnetCIDR{Cidr: "10.0.0.0/24"}}, nil)
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{SubnetId: subnetId, IpAddress: &nfvcommon.IPAddress{Ip: "192.168.1.5"}}
		_, err := getSubnetIpam(context.Background(), subnetId, nm, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam})
		require.Error(t, err)
	})
}

func TestGetNetworkIpam(t *testing.T) {
	networkId := id("net1")

	t.Run("ipam with subnet id delegates to subnet ipam", func(t *testing.T) {
		nm := newNetMock(t) // dynamic subnet IPAM needs no lookup
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{NetworkId: networkId, SubnetId: id("sub1")}
		got, err := getNetworkIpam(context.Background(), networkId, nm, []*vivnfm.VirtualNetworkInterfaceIPAM{ipam}, false)
		require.NoError(t, err)
		assert.Equal(t, "sub1", got.SubnetId.GetValue())
	})

	t.Run("no ipam with returnIfNoIpam errors", func(t *testing.T) {
		nm := newNetMock(t) // short-circuits before any network lookup
		_, err := getNetworkIpam(context.Background(), networkId, nm, nil, true)
		assert.ErrorIs(t, err, ErrIPAMConfigurationMissing)
	})

	t.Run("no ipam falls back to the network's first subnet", func(t *testing.T) {
		nm := newNetMock(t)
		nm.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).
			Return(&vivnfm.VirtualNetwork{SubnetId: []*nfvcommon.Identifier{id("sub9")}}, nil)
		got, err := getNetworkIpam(context.Background(), networkId, nm, nil, false)
		require.NoError(t, err)
		assert.Equal(t, "sub9", got.SubnetId.GetValue())
		assert.Nil(t, got.IpAddress)
	})

	t.Run("no ipam and network has no subnets is rejected", func(t *testing.T) {
		nm := newNetMock(t)
		nm.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).Return(&vivnfm.VirtualNetwork{}, nil)
		_, err := getNetworkIpam(context.Background(), networkId, nm, nil, false)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestInitSriovNetwork(t *testing.T) {
	t.Run("builds multus network and sriov interface with mac", func(t *testing.T) {
		netInst := &vivnfm.VirtualNetwork{
			NetworkResourceId: id("net1"),
			NetworkType:       nfvcommon.NetworkType_NETWORK_TYPE_SRIOV,
			Metadata:          &nfvcommon.Metadata{Fields: map[string]string{network.K8sNetworkNetAttachNameLabel: "sriov-nad"}},
		}
		ipams := []*vivnfm.VirtualNetworkInterfaceIPAM{{NetworkId: id("net1"), MacAddress: &nfvcommon.MacAddress{Mac: "aa:bb:cc:dd:ee:ff"}}}
		net, iface, _, err := initSriovNetwork(netInst, ipams)
		require.NoError(t, err)
		require.NotNil(t, net.Multus)
		assert.Equal(t, "sriov-nad", net.Multus.NetworkName)
		require.NotNil(t, iface.SRIOV)
		assert.Equal(t, "aa:bb:cc:dd:ee:ff", iface.MacAddress)
	})

	t.Run("missing netattach label is unsupported", func(t *testing.T) {
		netInst := &vivnfm.VirtualNetwork{NetworkResourceId: id("net1"), Metadata: &nfvcommon.Metadata{Fields: map[string]string{}}}
		_, _, _, err := initSriovNetwork(netInst, nil)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("nil metadata is unsupported", func(t *testing.T) {
		_, _, _, err := initSriovNetwork(&vivnfm.VirtualNetwork{}, nil)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})
}

func TestInitNetwork(t *testing.T) {
	subnet := &vivnfm.NetworkSubnet{
		ResourceId: id("sub1"),
		Metadata: &nfvcommon.Metadata{Fields: map[string]string{
			network.K8sSubnetNetAttachNameLabel: "sub1-netattach",
			network.K8sSubnetNameLabel:          "sub1",
		}},
	}

	t.Run("builds multus network with logical switch and address annotations", func(t *testing.T) {
		nm := newNetMock(t)
		nm.EXPECT().GetSubnet(gomock.Any(), gomock.Any()).Return(subnet, nil)
		ipam := &vivnfm.VirtualNetworkInterfaceIPAM{
			SubnetId:   id("sub1"),
			IpAddress:  &nfvcommon.IPAddress{Ip: "10.0.0.5"},
			MacAddress: &nfvcommon.MacAddress{Mac: "aa:bb:cc:dd:ee:ff"},
		}
		net, iface, ann, err := initNetwork(context.Background(), nm, ipam, testNamespace)
		require.NoError(t, err)
		require.NotNil(t, net.Multus)
		assert.Equal(t, "sub1-netattach", net.Multus.NetworkName)
		require.NotNil(t, iface.Bridge)
		assert.Equal(t, "sub1", ann["sub1-netattach."+testNamespace+".ovn.kubernetes.io/logical_switch"])
		assert.Equal(t, "10.0.0.5", ann["sub1-netattach."+testNamespace+".ovn.kubernetes.io/ip_address"])
		assert.Equal(t, "aa:bb:cc:dd:ee:ff", ann["sub1-netattach."+testNamespace+".ovn.kubernetes.io/mac_address"])
	})

	t.Run("ipam without a subnet id is rejected", func(t *testing.T) {
		nm := newNetMock(t) // rejected before any lookup
		_, _, _, err := initNetwork(context.Background(), nm, &vivnfm.VirtualNetworkInterfaceIPAM{}, testNamespace)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestInitNetworks(t *testing.T) {
	t.Run("always prepends the pod network", func(t *testing.T) {
		nm := newNetMock(t)
		nets, ifaces, _, err := initNetworks(context.Background(), nm, nil, nil, testNamespace)
		require.NoError(t, err)
		require.Len(t, nets, 1)
		require.Len(t, ifaces, 1)
		assert.NotNil(t, nets[0].Pod)
		assert.NotNil(t, ifaces[0].Masquerade)
	})

	t.Run("interface without network or subnet id is rejected", func(t *testing.T) {
		nm := newNetMock(t)
		data := []*vivnfm.VirtualNetworkInterfaceData{{}}
		_, _, _, err := initNetworks(context.Background(), nm, data, nil, testNamespace)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("sriov network id dispatches to the sriov interface builder", func(t *testing.T) {
		nm := newNetMock(t)
		nm.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).Return(&vivnfm.VirtualNetwork{
			NetworkResourceId: id("net1"),
			NetworkType:       nfvcommon.NetworkType_NETWORK_TYPE_SRIOV,
			Metadata:          &nfvcommon.Metadata{Fields: map[string]string{network.K8sNetworkNetAttachNameLabel: "sriov-nad"}},
		}, nil)
		data := []*vivnfm.VirtualNetworkInterfaceData{{NetworkId: id("net1")}}
		nets, ifaces, _, err := initNetworks(context.Background(), nm, data, nil, testNamespace)
		require.NoError(t, err)
		require.Len(t, ifaces, 2) // pod + sriov
		assert.NotNil(t, ifaces[1].SRIOV)
		assert.Equal(t, "sriov-nad", nets[1].Multus.NetworkName)
	})
}

func newComputeManager(t *testing.T, objs ...client.Object) *manager {
	t.Helper()
	cl := k8stest.NewClient(t, objs...)
	ns := testNamespace
	m, err := NewComputeManager(cl, cl, &config.K8sConfig{Namespace: &ns}, nil, nil, nil, newNetMock(t))
	require.NoError(t, err)
	return m
}

func seedVM(name string) *kubevirtv1.VirtualMachine {
	meta := k8stest.ManagedMeta(name)
	meta.Namespace = testNamespace
	return &kubevirtv1.VirtualMachine{ObjectMeta: meta}
}

func TestListComputeResources(t *testing.T) {
	t.Run("empty cluster returns nothing", func(t *testing.T) {
		m := newComputeManager(t)
		got, err := m.ListComputeResources(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("a VM with no VMI yet is skipped, not an error", func(t *testing.T) {
		m := newComputeManager(t, seedVM("vm1"))
		got, err := m.ListComputeResources(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
