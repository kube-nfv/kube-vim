package kubeovn

import (
	"context"
	"testing"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = "kube-nfv"

func newManager(t *testing.T, objs ...client.Object) (*manager, client.Client) {
	t.Helper()
	cl := k8stest.NewClient(t, objs...)
	ns := testNamespace
	m, err := NewKubeovnNetworkManager(cl, cl, &config.K8sConfig{Namespace: &ns}, nil)
	require.NoError(t, err)
	return m, cl
}

func seedVpc(name string, subnets ...string) *kubeovnv1.Vpc {
	return &kubeovnv1.Vpc{
		ObjectMeta: managedMeta(name),
		Status:     kubeovnv1.VpcStatus{Subnets: subnets},
	}
}

func seedVlan(name string, id int, subnets ...string) *kubeovnv1.Vlan {
	return &kubeovnv1.Vlan{
		ObjectMeta: managedMeta(name),
		Spec:       kubeovnv1.VlanSpec{ID: id, Provider: "provider"},
		Status:     kubeovnv1.VlanStatus{Subnets: subnets},
	}
}

func seedSubnet(name string) *kubeovnv1.Subnet {
	meta := managedMeta(name)
	meta.Labels[network.K8sSubnetNameLabel] = name
	meta.Labels[network.K8sSubnetNetAttachNameLabel] = formatNetAttachName(name)
	return &kubeovnv1.Subnet{
		ObjectMeta: meta,
		Spec:       kubeovnv1.SubnetSpec{Protocol: "IPv4", CIDRBlock: "10.0.0.0/24", Gateway: "10.0.0.1", EnableDHCP: true},
	}
}

func TestGetNetwork(t *testing.T) {
	t.Run("overlay by name", func(t *testing.T) {
		m, _ := newManager(t, seedVpc("net1"))
		got, err := m.GetNetwork(context.Background(), network.GetNetworkByName("net1"))
		require.NoError(t, err)
		assert.Equal(t, "net1", got.GetNetworkResourceName())
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY, got.NetworkType)
		assert.Equal(t, "uid-net1", got.NetworkResourceId.GetValue())
	})

	t.Run("overlay by uid", func(t *testing.T) {
		m, _ := newManager(t, seedVpc("net1"))
		got, err := m.GetNetwork(context.Background(), network.GetNetworkByUid(&nfvcommon.Identifier{Value: "uid-net1"}))
		require.NoError(t, err)
		assert.Equal(t, "net1", got.GetNetworkResourceName())
	})

	t.Run("underlay by name", func(t *testing.T) {
		m, _ := newManager(t, seedVlan("vl1", 100))
		got, err := m.GetNetwork(context.Background(), network.GetNetworkByName("vl1"))
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY, got.NetworkType)
		assert.Equal(t, uint64(100), got.GetSegmentationId())
	})

	t.Run("missing network is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetNetwork(context.Background(), network.GetNetworkByName("nope"))
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}

func TestListNetworks(t *testing.T) {
	t.Run("joins vpcs, vlans and subnets", func(t *testing.T) {
		m, _ := newManager(t,
			seedVpc("net1", "sub1"),
			seedVlan("vl1", 100, "sub2"),
			seedSubnet("sub1"),
			seedSubnet("sub2"),
		)
		got, err := m.ListNetworks(context.Background())
		require.NoError(t, err)
		require.Len(t, got, 2)

		byName := map[string]*vivnfm.VirtualNetwork{}
		for _, n := range got {
			byName[n.GetNetworkResourceName()] = n
		}
		require.Contains(t, byName, "net1")
		require.Contains(t, byName, "vl1")
		assert.Equal(t, "uid-sub1", byName["net1"].SubnetId[0].GetValue())
		assert.Equal(t, "uid-sub2", byName["vl1"].SubnetId[0].GetValue())
	})

	t.Run("errors when a referenced subnet is missing", func(t *testing.T) {
		m, _ := newManager(t, seedVpc("net1", "ghost"))
		_, err := m.ListNetworks(context.Background())
		require.Error(t, err)
	})

	t.Run("empty returns nothing", func(t *testing.T) {
		m, _ := newManager(t)
		got, err := m.ListNetworks(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestCreateNetwork(t *testing.T) {
	t.Run("overlay creates a vpc", func(t *testing.T) {
		m, cl := newManager(t)
		got, err := m.CreateNetwork(context.Background(), "net1", &vivnfm.VirtualNetworkData{})
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY, got.NetworkType)
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Name: "net1"}, &kubeovnv1.Vpc{}))
	})

	t.Run("underlay creates a vlan", func(t *testing.T) {
		m, cl := newManager(t)
		underlay := nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY
		got, err := m.CreateNetwork(context.Background(), "vl1", &vivnfm.VirtualNetworkData{
			NetworkType:     &underlay,
			ProviderNetwork: ptr("provider"),
			SegmentationId:  uptr(100),
		})
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY, got.NetworkType)
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Name: "vl1"}, &kubeovnv1.Vlan{}))
	})
}

func TestDeleteNetwork(t *testing.T) {
	t.Run("overlay deletes the vpc", func(t *testing.T) {
		m, cl := newManager(t, seedVpc("net1"))
		require.NoError(t, m.DeleteNetwork(context.Background(), network.GetNetworkByName("net1")))
		err := cl.Get(context.Background(), client.ObjectKey{Name: "net1"}, &kubeovnv1.Vpc{})
		assert.True(t, apierrors.IsNotFound(err))
	})

	t.Run("missing network surfaces not found", func(t *testing.T) {
		m, _ := newManager(t)
		err := m.DeleteNetwork(context.Background(), network.GetNetworkByName("nope"))
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}

func TestGetSubnet(t *testing.T) {
	t.Run("by name", func(t *testing.T) {
		m, _ := newManager(t, seedSubnet("sub1"))
		got, err := m.GetSubnet(context.Background(), network.GetSubnetByName("sub1"))
		require.NoError(t, err)
		assert.Equal(t, "uid-sub1", got.ResourceId.GetValue())
		assert.Equal(t, "10.0.0.0/24", got.Cidr.GetCidr())
	})

	t.Run("by uid", func(t *testing.T) {
		m, _ := newManager(t, seedSubnet("sub1"))
		got, err := m.GetSubnet(context.Background(), network.GetSubnetByUid(&nfvcommon.Identifier{Value: "uid-sub1"}))
		require.NoError(t, err)
		assert.Equal(t, "sub1", got.Metadata.Fields[network.K8sSubnetNameLabel])
	})

	t.Run("missing uid is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetSubnet(context.Background(), network.GetSubnetByUid(&nfvcommon.Identifier{Value: "uid-nope"}))
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})

	t.Run("no selector is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetSubnet(context.Background())
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestListSubnets(t *testing.T) {
	m, _ := newManager(t, seedSubnet("sub1"), seedSubnet("sub2"))
	got, err := m.ListSubnets(context.Background())
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestDeleteSubnet(t *testing.T) {
	nad := &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: formatNetAttachName("sub1"), Namespace: testNamespace},
	}
	m, cl := newManager(t, seedSubnet("sub1"), nad)
	require.NoError(t, m.DeleteSubnet(context.Background(), network.GetSubnetByName("sub1")))

	err := cl.Get(context.Background(), client.ObjectKey{Name: "sub1"}, &kubeovnv1.Subnet{})
	assert.True(t, apierrors.IsNotFound(err), "subnet should be gone")
	err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: formatNetAttachName("sub1")}, &netattv1.NetworkAttachmentDefinition{})
	assert.True(t, apierrors.IsNotFound(err), "netattach should be gone")
}

func ptr(s string) *string  { return &s }
func uptr(u uint64) *uint64 { return &u }
