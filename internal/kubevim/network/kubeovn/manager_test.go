package kubeovn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const testNamespace = k8stest.TestNamespace

func newManagerWithClient(t *testing.T, cl, reader client.Client) *manager {
	t.Helper()
	ns := testNamespace
	m, err := NewKubeovnNetworkManager(cl, reader, &config.K8sConfig{Namespace: &ns}, &config.SriovNetworkConfig{SocketFile: k8stest.Ptr("unix:/var/run/ovs.sock")}, nil)
	require.NoError(t, err)
	return m
}

func newManager(t *testing.T) (*manager, client.Client) {
	t.Helper()
	cl := k8stest.NewIndexedClient(t, IdIndexes)
	return newManagerWithClient(t, cl, cl), cl
}

func v4(cidr string) *networkv1.SubnetData {
	return &networkv1.SubnetData{IpVersion: commonv1.IpVersion_IP_VERSION_IPV4, Cidr: cidr}
}

func v6(cidr string) *networkv1.SubnetData {
	return &networkv1.SubnetData{IpVersion: commonv1.IpVersion_IP_VERSION_IPV6, Cidr: cidr}
}

func geneve(l3 ...*networkv1.SubnetData) *networkv1.Network {
	return &networkv1.Network{NetworkName: "data", NetworkType: networkv1.NetworkType_NETWORK_TYPE_GENEVE, Layer3Attributes: l3}
}

func vlan(id uint32, l3 ...*networkv1.SubnetData) *networkv1.Network {
	return &networkv1.Network{NetworkType: networkv1.NetworkType_NETWORK_TYPE_VLAN, ProviderNetwork: "mx5p0", SegmentationId: id, Mtu: 9000, Layer3Attributes: l3}
}

func get[T client.Object](t *testing.T, cl client.Client, name string, obj T) T {
	t.Helper()
	ns := ""
	if _, ok := any(obj).(*netattv1.NetworkAttachmentDefinition); ok {
		ns = testNamespace
	}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, obj))
	return obj
}

func TestCreateNetwork(t *testing.T) {
	t.Parallel()
	t.Run("geneve dual stack is one vpc, one subnet and a bridge nad", func(t *testing.T) {
		m, cl := newManager(t)
		gw := v4("10.0.0.0/24")
		gw.GatewayIp = "10.0.0.1"
		gw6 := v6("fd00::/64")
		gw6.GatewayIp = "fd00::1"
		n := geneve(gw, gw6)
		n.Mtu = 1400
		got, err := m.CreateNetwork(context.Background(), n)
		require.NoError(t, err)
		require.Len(t, got.SubnetIds, 2)
		assert.Equal(t, "data", got.NetworkName)
		assert.Empty(t, got.Layer3Attributes, "input-only")

		name := formatNetworkName(got.NetworkId)
		// Kube-OVN rejects a subnet named like its Vpc.
		vpc := get(t, cl, formatVpcName(name), &kubeovnv1.Vpc{})
		assert.Equal(t, got.NetworkId, vpc.Labels[network.K8sNetworkIdLabel])
		sub := get(t, cl, name, &kubeovnv1.Subnet{})
		assert.Equal(t, vpc.Name, sub.Spec.Vpc)
		assert.Equal(t, "Dual", sub.Spec.Protocol)
		assert.Equal(t, "10.0.0.0/24,fd00::/64", sub.Spec.CIDRBlock)
		assert.Equal(t, "10.0.0.1,fd00::1", sub.Spec.Gateway)
		assert.Equal(t, uint32(1400), sub.Spec.Mtu)
		assert.True(t, sub.Spec.EnableDHCP)
		assert.Equal(t, got.SubnetIds[0], sub.Labels[network.K8sSubnetIdIPv4Label])
		assert.Equal(t, got.SubnetIds[1], sub.Labels[network.K8sSubnetIdIPv6Label])
		nad := get(t, cl, name, &netattv1.NetworkAttachmentDefinition{})
		assert.Equal(t, network.NicTypeBridge, nad.Labels[network.K8sNicTypeLabel])
		assertProviderMatches(t, sub, nad)
	})

	t.Run("vlan with sriov and no subnets is a vlan and an ovs-cni nad", func(t *testing.T) {
		m, cl := newManager(t)
		n := vlan(100)
		n.Sriov = &networkv1.SriovSettings{Pool: "openshift.io/mlx_vf_pf0_vfio"}
		got, err := m.CreateNetwork(context.Background(), n)
		require.NoError(t, err)
		assert.Empty(t, got.SubnetIds)
		assert.Equal(t, "openshift.io/mlx_vf_pf0_vfio", got.GetSriov().GetPool())

		name := formatNetworkName(got.NetworkId)
		v := get(t, cl, name, &kubeovnv1.Vlan{})
		assert.Equal(t, 100, v.Spec.ID)
		assert.Equal(t, "mx5p0", v.Spec.Provider)
		err = cl.Get(context.Background(), client.ObjectKey{Name: name}, &kubeovnv1.Subnet{})
		assert.True(t, apierrors.IsNotFound(err), "no kube-ovn subnet without layer3 attributes")
		nad := get(t, cl, formatSriovNetAttachName(name), &netattv1.NetworkAttachmentDefinition{})
		assert.Equal(t, "openshift.io/mlx_vf_pf0_vfio", nad.Annotations[nadResourceNameAnnotation])
		assert.Equal(t, network.NicTypeSriov, nad.Labels[network.K8sNicTypeLabel])
		var cfg ovsCniConfig
		require.NoError(t, json.Unmarshal([]byte(nad.Spec.Config), &cfg))
		assert.Equal(t, "ovs", cfg.Type)
		require.NotNil(t, cfg.Vlan)
		assert.Equal(t, 100, *cfg.Vlan)
		assert.Equal(t, 9000, cfg.MTU)
	})

	t.Run("flat is an untagged vlan with its subnet on it", func(t *testing.T) {
		m, cl := newManager(t)
		n := vlan(0, v4("192.168.0.0/24"))
		n.NetworkType = networkv1.NetworkType_NETWORK_TYPE_FLAT
		got, err := m.CreateNetwork(context.Background(), n)
		require.NoError(t, err)
		name := formatNetworkName(got.NetworkId)
		assert.Equal(t, 0, get(t, cl, name, &kubeovnv1.Vlan{}).Spec.ID)
		assert.Equal(t, name, get(t, cl, name, &kubeovnv1.Subnet{}).Spec.Vlan)
	})

	t.Run("address pools are unsupported", func(t *testing.T) {
		m, _ := newManager(t)
		l3 := v4("10.0.0.0/24")
		l3.AddressPools = []*networkv1.IpAddressPool{{StartIp: "10.0.0.10", EndIp: "10.0.0.20"}}
		_, err := m.CreateNetwork(context.Background(), geneve(l3))
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("a gateway on one family of a dual-stack network is rejected and rolled back", func(t *testing.T) {
		m, cl := newManager(t)
		l3 := v4("10.0.0.0/24")
		l3.GatewayIp = "10.0.0.1"
		_, err := m.CreateNetwork(context.Background(), geneve(l3, v6("fd00::/64")))
		var invalid *apperrors.ErrInvalidArgument
		require.ErrorAs(t, err, &invalid)
		vpcs := &kubeovnv1.VpcList{}
		require.NoError(t, cl.List(context.Background(), vpcs))
		assert.Empty(t, vpcs.Items)
	})
}

func TestGetNetworkAndSubnets(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	created, err := m.CreateNetwork(context.Background(), geneve(v4("10.0.0.0/24"), v6("fd00::/64")))
	require.NoError(t, err)

	got, err := m.GetNetwork(context.Background(), created.NetworkId)
	require.NoError(t, err)
	assert.Equal(t, created.NetworkId, got.NetworkId)
	assert.Equal(t, created.SubnetIds, got.SubnetIds)
	assert.Equal(t, commonv1.OperationalState_OPERATIONAL_STATE_ENABLED, got.OperationalState)

	sub, err := m.GetSubnet(context.Background(), created.SubnetIds[1])
	require.NoError(t, err)
	assert.Equal(t, created.NetworkId, sub.NetworkId)
	assert.Equal(t, "fd00::/64", sub.GetData().GetCidr())

	subs, err := m.ListSubnets(context.Background())
	require.NoError(t, err)
	assert.Len(t, subs, 2)

	_, err = m.GetNetwork(context.Background(), "nope")
	var notFound *apperrors.ErrNotFound
	assert.ErrorAs(t, err, &notFound)
	_, err = m.GetSubnet(context.Background(), "nope")
	assert.ErrorAs(t, err, &notFound)
}

func TestAttachment(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	n := vlan(100, v4("10.0.0.0/24"))
	n.Sriov = &networkv1.SriovSettings{Pool: "pool"}
	both, err := m.CreateNetwork(context.Background(), n)
	require.NoError(t, err)
	l2, err := m.CreateNetwork(context.Background(), vlan(200))
	require.NoError(t, err)
	name := formatNetworkName(both.NetworkId)

	got, err := m.Attachment(context.Background(), both.NetworkId, network.NicTypeBridge)
	require.NoError(t, err)
	assert.Equal(t, &network.Attachment{NetAttachName: name, LogicalSwitch: name, Cidrs: []string{"10.0.0.0/24"}}, got)
	got, err = m.Attachment(context.Background(), both.NetworkId, network.NicTypeSriov)
	require.NoError(t, err)
	assert.Equal(t, formatSriovNetAttachName(name), got.NetAttachName)
	assert.Empty(t, got.LogicalSwitch)

	var invalid *apperrors.ErrInvalidArgument
	_, err = m.Attachment(context.Background(), l2.NetworkId, network.NicTypeBridge)
	assert.ErrorAs(t, err, &invalid, "no subnets, no bridged interfaces")
	_, err = m.Attachment(context.Background(), l2.NetworkId, network.NicTypeSriov)
	assert.ErrorAs(t, err, &invalid, "no sriov settings")
}

func TestListNetworks(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	a, err := m.CreateNetwork(context.Background(), geneve(v4("10.0.0.0/24")))
	require.NoError(t, err)
	b, err := m.CreateNetwork(context.Background(), vlan(100))
	require.NoError(t, err)

	got, err := m.ListNetworks(context.Background())
	require.NoError(t, err)
	ids := map[string][]string{}
	for _, n := range got {
		ids[n.NetworkId] = n.SubnetIds
	}
	assert.Equal(t, map[string][]string{a.NetworkId: a.SubnetIds, b.NetworkId: nil}, ids)
}

func TestDeleteNetwork(t *testing.T) {
	t.Parallel()
	t.Run("removes every object of the network", func(t *testing.T) {
		m, cl := newManager(t)
		n := vlan(100, v4("10.0.0.0/24"))
		n.Sriov = &networkv1.SriovSettings{Pool: "pool"}
		created, err := m.CreateNetwork(context.Background(), n)
		require.NoError(t, err)
		require.NoError(t, m.DeleteNetwork(context.Background(), created.NetworkId))

		for _, list := range []client.ObjectList{&kubeovnv1.VlanList{}, &kubeovnv1.SubnetList{}, &netattv1.NetworkAttachmentDefinitionList{}} {
			require.NoError(t, cl.List(context.Background(), list))
			items, _ := json.Marshal(list)
			assert.NotContains(t, string(items), created.NetworkId)
		}
	})

	t.Run("missing network is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		var notFound *apperrors.ErrNotFound
		assert.ErrorAs(t, m.DeleteNetwork(context.Background(), "nope"), &notFound)
	})

	t.Run("the management network is kept", func(t *testing.T) {
		m, cl := newManager(t)
		require.NoError(t, m.EnsureManagementNetwork(context.Background(), mgmtConfig()))
		vpc := get(t, cl, "mgmt", &kubeovnv1.Vpc{})
		var invalid *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, m.DeleteNetwork(context.Background(), vpc.Labels[network.K8sNetworkIdLabel]), &invalid)
	})
}

func TestCreateNetworkWithStaleCache(t *testing.T) {
	t.Parallel()
	t.Run("waits until the cache sees the new network", func(t *testing.T) {
		base := k8stest.NewIndexedClient(t, IdIndexes)
		m := newManagerWithClient(t, interceptor.NewClient(base, laggingCache()), base)

		got, err := m.CreateNetwork(context.Background(), geneve(v4("10.10.0.0/24")))
		require.NoError(t, err)
		// The next cached read (RO's status refresh) must find it.
		_, err = m.GetNetwork(context.Background(), got.NetworkId)
		require.NoError(t, err)
	})

	t.Run("cache that never catches up fails and removes everything", func(t *testing.T) {
		base := k8stest.NewIndexedClient(t, IdIndexes)
		m := newManagerWithClient(t, interceptor.NewClient(base, staleCache), base)
		m.cacheSyncTimeout = 100 * time.Millisecond

		_, err := m.CreateNetwork(context.Background(), geneve(v4("10.10.0.0/24")))
		require.Error(t, err)
		assertNoNetworkObjects(t, base)
	})

	t.Run("failed subnet removes the vpc and netattach", func(t *testing.T) {
		indexed := k8stest.NewIndexedClient(t, IdIndexes)
		base := interceptor.NewClient(indexed, interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*kubeovnv1.Subnet); ok {
					return errors.New("subnet rejected")
				}
				return c.Create(ctx, obj, opts...)
			},
		})
		m := newManagerWithClient(t, base, base)

		_, err := m.CreateNetwork(context.Background(), geneve(v4("10.10.0.0/24")))
		require.Error(t, err)
		assertNoNetworkObjects(t, indexed)
	})
}

func assertNoNetworkObjects(t *testing.T, cl client.Client) {
	t.Helper()
	vpcs := &kubeovnv1.VpcList{}
	require.NoError(t, cl.List(context.Background(), vpcs))
	assert.Empty(t, vpcs.Items, "vpc should be rolled back")
	nads := &netattv1.NetworkAttachmentDefinitionList{}
	require.NoError(t, cl.List(context.Background(), nads, client.InNamespace(testNamespace)))
	assert.Empty(t, nads.Items, "netattach should be rolled back")
}

func mgmtConfig() *config.ManagementNetworkConfig {
	ns := testNamespace
	return &config.ManagementNetworkConfig{
		Enabled:               k8stest.Ptr(true),
		Name:                  k8stest.Ptr("mgmt"),
		Cidr:                  k8stest.Ptr("10.240.0.0/24"),
		NetAttachDefName:      k8stest.Ptr("mgmt-netattach"),
		NetAttachDefNamespace: &ns,
	}
}

func TestEnsureManagementNetwork(t *testing.T) {
	t.Parallel()
	m, cl := newManager(t)
	require.NoError(t, m.EnsureManagementNetwork(context.Background(), mgmtConfig()))
	vpc := get(t, cl, "mgmt", &kubeovnv1.Vpc{})
	networkId := vpc.Labels[network.K8sNetworkIdLabel]
	require.NotEmpty(t, networkId)

	// A restart keeps the ids clients already hold.
	require.NoError(t, m.EnsureManagementNetwork(context.Background(), mgmtConfig()))
	got, err := m.GetNetwork(context.Background(), networkId)
	require.NoError(t, err)
	assert.Equal(t, "mgmt", got.NetworkName)
	assert.Equal(t, networkv1.NetworkType_NETWORK_TYPE_GENEVE, got.NetworkType)
	require.Len(t, got.SubnetIds, 1)
	sub, err := m.GetSubnet(context.Background(), got.SubnetIds[0])
	require.NoError(t, err)
	assert.Equal(t, "10.240.0.0/24", sub.GetData().GetCidr())

	assertProviderMatches(t, get(t, cl, formatSubnetName("mgmt", "0"), &kubeovnv1.Subnet{}), get(t, cl, "mgmt-netattach", &netattv1.NetworkAttachmentDefinition{}))
}

// Kube-OVN binds an attachment to the subnet whose provider equals the NAD's provider.
func assertProviderMatches(t *testing.T, sub *kubeovnv1.Subnet, nad *netattv1.NetworkAttachmentDefinition) {
	t.Helper()
	var cfg struct {
		Provider string `json:"provider"`
	}
	require.NoError(t, json.Unmarshal([]byte(nad.Spec.Config), &cfg))
	assert.Equal(t, cfg.Provider, sub.Spec.Provider)
}

// staleCache hides kube-ovn networks and subnets from reads, like an informer cache
// that has not yet observed objects created moments ago.
var staleCache = interceptor.Funcs{
	Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		switch obj.(type) {
		case *kubeovnv1.Vpc, *kubeovnv1.Vlan, *kubeovnv1.Subnet:
			return apierrors.NewNotFound(kubeovnv1.Resource("stale"), key.Name)
		}
		return c.Get(ctx, key, obj, opts...)
	},
	List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		switch list.(type) {
		case *kubeovnv1.VpcList, *kubeovnv1.VlanList, *kubeovnv1.SubnetList:
			return nil
		}
		return c.List(ctx, list, opts...)
	},
}

// laggingCache misses the first Get of a kube-ovn network or subnet, then reads through.
func laggingCache() interceptor.Funcs {
	missed := false
	return interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			switch obj.(type) {
			case *kubeovnv1.Vpc, *kubeovnv1.Vlan, *kubeovnv1.Subnet:
				if !missed {
					missed = true
					return apierrors.NewNotFound(kubeovnv1.Resource("lagging"), key.Name)
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
}
