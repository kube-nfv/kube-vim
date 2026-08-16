package sriov

import (
	"context"
	"testing"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = "kube-nfv"

func newManager(t *testing.T, objs ...client.Object) (*manager, client.Client) {
	t.Helper()
	cl := k8stest.NewClient(t, objs...)
	ns := testNamespace
	m, err := NewSriovNetworkManager(cl, &config.K8sConfig{Namespace: &ns}, nil, nil)
	require.NoError(t, err)
	return m, cl
}

func sriovType() *nfvcommon.NetworkType {
	t := nfvcommon.NetworkType_NETWORK_TYPE_SRIOV
	return &t
}

// seedNad builds an instantiated, kube-nfv-owned SR-IOV NAD ready to seed.
func seedNad(t *testing.T, name, resource string, vlan uint64) *netattv1.NetworkAttachmentDefinition {
	t.Helper()
	cfg, err := formatOvsCniConfig(name, vlan, "")
	require.NoError(t, err)
	meta := k8stest.ManagedMeta(name)
	meta.Namespace = testNamespace
	meta.Annotations = map[string]string{nadResourceNameAnnotation: resource}
	meta.Labels[network.K8sNetworkTypeLabel] = nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String()
	meta.Labels[network.K8sNetworkNameLabel] = name
	return &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: meta,
		Spec:       netattv1.NetworkAttachmentDefinitionSpec{Config: cfg},
	}
}

func TestCreateNetwork(t *testing.T) {
	t.Run("creates NAD and prefixes bare provider resource", func(t *testing.T) {
		m, cl := newManager(t)
		got, err := m.CreateNetwork(context.Background(), "net1", &vivnfm.VirtualNetworkData{
			NetworkType:     sriovType(),
			ProviderNetwork: ptr("intel_sriov"),
			SegmentationId:  uptr(100),
		})
		require.NoError(t, err)
		assert.Equal(t, "net1", got.GetNetworkResourceName())
		assert.Equal(t, uint64(100), got.GetSegmentationId())

		nad := &netattv1.NetworkAttachmentDefinition{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "net1"}, nad))
		assert.Equal(t, "openshift.io/intel_sriov", nad.Annotations[nadResourceNameAnnotation])
		assert.Equal(t, common.KubeNfvName, nad.Labels[common.K8sManagedByLabel])
	})

	t.Run("keeps qualified provider resource as-is", func(t *testing.T) {
		m, cl := newManager(t)
		_, err := m.CreateNetwork(context.Background(), "net1", &vivnfm.VirtualNetworkData{
			NetworkType:     sriovType(),
			ProviderNetwork: ptr("mellanox.com/sriov_rdma"),
		})
		require.NoError(t, err)
		nad := &netattv1.NetworkAttachmentDefinition{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "net1"}, nad))
		assert.Equal(t, "mellanox.com/sriov_rdma", nad.Annotations[nadResourceNameAnnotation])
	})

	t.Run("wrong network type is unsupported", func(t *testing.T) {
		m, _ := newManager(t)
		overlay := nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY
		_, err := m.CreateNetwork(context.Background(), "net1", &vivnfm.VirtualNetworkData{
			NetworkType:     &overlay,
			ProviderNetwork: ptr("intel_sriov"),
		})
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("missing provider network is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.CreateNetwork(context.Background(), "net1", &vivnfm.VirtualNetworkData{NetworkType: sriovType()})
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("layer3 attributes are rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.CreateNetwork(context.Background(), "net1", &vivnfm.VirtualNetworkData{
			NetworkType:      sriovType(),
			ProviderNetwork:  ptr("intel_sriov"),
			Layer3Attributes: []*vivnfm.NetworkSubnetData{{}},
		})
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestGetNetwork(t *testing.T) {
	t.Run("by name", func(t *testing.T) {
		m, _ := newManager(t, seedNad(t, "net1", "openshift.io/intel_sriov", 100))
		got, err := m.GetNetwork(context.Background(), network.GetNetworkByName("net1"))
		require.NoError(t, err)
		assert.Equal(t, "net1", got.GetNetworkResourceName())
		assert.Equal(t, "openshift.io/intel_sriov", got.GetProviderNetwork())
	})

	t.Run("by uid", func(t *testing.T) {
		m, _ := newManager(t, seedNad(t, "net1", "openshift.io/intel_sriov", 0))
		got, err := m.GetNetwork(context.Background(), network.GetNetworkByUid(&nfvcommon.Identifier{Value: "uid-net1"}))
		require.NoError(t, err)
		assert.Equal(t, "net1", got.GetNetworkResourceName())
	})

	t.Run("missing name is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetNetwork(context.Background(), network.GetNetworkByName("nope"))
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})

	t.Run("non-sriov NAD with same name is ErrNotFound", func(t *testing.T) {
		nad := seedNad(t, "net1", "openshift.io/intel_sriov", 0)
		nad.Labels[network.K8sNetworkTypeLabel] = nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY.String()
		m, _ := newManager(t, nad)
		_, err := m.GetNetwork(context.Background(), network.GetNetworkByName("net1"))
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})

	t.Run("no selector is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetNetwork(context.Background())
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestListNetworks(t *testing.T) {
	t.Run("returns only kube-nfv-owned sriov networks", func(t *testing.T) {
		foreign := seedNad(t, "foreign", "openshift.io/x", 0)
		delete(foreign.Labels, common.K8sManagedByLabel)
		m, _ := newManager(t, seedNad(t, "net1", "openshift.io/a", 0), seedNad(t, "net2", "openshift.io/b", 0), foreign)
		got, err := m.ListNetworks(context.Background())
		require.NoError(t, err)
		require.Len(t, got, 2)
	})

	t.Run("empty returns nothing", func(t *testing.T) {
		m, _ := newManager(t)
		got, err := m.ListNetworks(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestDeleteNetwork(t *testing.T) {
	t.Run("removes the NAD", func(t *testing.T) {
		m, cl := newManager(t, seedNad(t, "net1", "openshift.io/a", 0))
		require.NoError(t, m.DeleteNetwork(context.Background(), network.GetNetworkByName("net1")))
		err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "net1"}, &netattv1.NetworkAttachmentDefinition{})
		assert.True(t, apierrors.IsNotFound(err))
	})

	t.Run("deleting a missing network surfaces not found", func(t *testing.T) {
		m, _ := newManager(t)
		err := m.DeleteNetwork(context.Background(), network.GetNetworkByName("nope"))
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}

func TestSubnetsUnsupported(t *testing.T) {
	m, _ := newManager(t)
	_, err := m.CreateSubnet(context.Background(), "s", nil)
	assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	_, err = m.GetSubnet(context.Background())
	assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	_, err = m.ListSubnets(context.Background())
	assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	assert.ErrorIs(t, m.DeleteSubnet(context.Background()), apperrors.ErrUnsupported)
}

func ptr(s string) *string  { return &s }
func uptr(u uint64) *uint64 { return &u }
