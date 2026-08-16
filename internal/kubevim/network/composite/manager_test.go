package composite

import (
	"context"
	"errors"
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

// setup builds the composite over two mock backends. Expectations set on a mock
// assert the composite dispatched to it; leaving a mock without an expectation
// asserts the composite did NOT call it.
func setup(t *testing.T) (*networkmock.MockManager, *networkmock.MockManager, network.Manager) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ovn := networkmock.NewMockManager(ctrl)
	sriov := networkmock.NewMockManager(ctrl)
	return ovn, sriov, NewManager(ovn, sriov)
}

func notFound() error { return &apperrors.ErrNotFound{Entity: "network"} }

func sriovNet() *vivnfm.VirtualNetwork {
	return &vivnfm.VirtualNetwork{NetworkType: nfvcommon.NetworkType_NETWORK_TYPE_SRIOV}
}

func TestCreateNetworkDispatch(t *testing.T) {
	t.Parallel()
	t.Run("sriov type routes to sriov backend", func(t *testing.T) {
		_, sriov, m := setup(t)
		sriovT := nfvcommon.NetworkType_NETWORK_TYPE_SRIOV
		sriov.EXPECT().CreateNetwork(gomock.Any(), "net", gomock.Any()).Return(sriovNet(), nil)
		_, err := m.CreateNetwork(context.Background(), "net", &vivnfm.VirtualNetworkData{NetworkType: &sriovT})
		require.NoError(t, err)
	})

	t.Run("nil type defaults to ovn backend", func(t *testing.T) {
		ovn, _, m := setup(t)
		ovn.EXPECT().CreateNetwork(gomock.Any(), "net", gomock.Any()).Return(&vivnfm.VirtualNetwork{}, nil)
		_, err := m.CreateNetwork(context.Background(), "net", &vivnfm.VirtualNetworkData{})
		require.NoError(t, err)
	})
}

func TestGetNetworkDispatch(t *testing.T) {
	t.Parallel()
	t.Run("ovn hit does not fall through to sriov", func(t *testing.T) {
		ovn, _, m := setup(t) // no sriov expectation: must not be called
		ovn.EXPECT().GetNetwork(gomock.Any()).Return(&vivnfm.VirtualNetwork{}, nil)
		_, err := m.GetNetwork(context.Background())
		require.NoError(t, err)
	})

	t.Run("ovn not-found falls through to sriov", func(t *testing.T) {
		ovn, sriov, m := setup(t)
		ovn.EXPECT().GetNetwork(gomock.Any()).Return(nil, notFound())
		sriov.EXPECT().GetNetwork(gomock.Any()).Return(sriovNet(), nil)
		got, err := m.GetNetwork(context.Background())
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_SRIOV, got.NetworkType)
	})

	t.Run("ovn hard error short-circuits", func(t *testing.T) {
		ovn, _, m := setup(t) // sriov must not be called on a hard error
		ovn.EXPECT().GetNetwork(gomock.Any()).Return(nil, errors.New("boom"))
		_, err := m.GetNetwork(context.Background())
		require.Error(t, err)
	})

	t.Run("both not-found returns not-found", func(t *testing.T) {
		ovn, sriov, m := setup(t)
		ovn.EXPECT().GetNetwork(gomock.Any()).Return(nil, notFound())
		sriov.EXPECT().GetNetwork(gomock.Any()).Return(nil, notFound())
		_, err := m.GetNetwork(context.Background())
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}

func TestListNetworksDispatch(t *testing.T) {
	t.Parallel()
	t.Run("concatenates both backends", func(t *testing.T) {
		ovn, sriov, m := setup(t)
		ovn.EXPECT().ListNetworks(gomock.Any()).Return([]*vivnfm.VirtualNetwork{{}, {}}, nil)
		sriov.EXPECT().ListNetworks(gomock.Any()).Return([]*vivnfm.VirtualNetwork{{}}, nil)
		got, err := m.ListNetworks(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("ovn error propagates", func(t *testing.T) {
		ovn, _, m := setup(t)
		ovn.EXPECT().ListNetworks(gomock.Any()).Return(nil, errors.New("boom"))
		_, err := m.ListNetworks(context.Background())
		require.Error(t, err)
	})
}

func TestDeleteNetworkDispatch(t *testing.T) {
	t.Parallel()
	t.Run("ovn not-found falls through to sriov", func(t *testing.T) {
		ovn, sriov, m := setup(t)
		ovn.EXPECT().DeleteNetwork(gomock.Any()).Return(notFound())
		sriov.EXPECT().DeleteNetwork(gomock.Any()).Return(nil)
		require.NoError(t, m.DeleteNetwork(context.Background()))
	})
}

func TestCreateSubnetDispatch(t *testing.T) {
	t.Parallel()
	t.Run("sriov-typed network routes subnet to sriov", func(t *testing.T) {
		ovn, sriov, m := setup(t)
		// composite.GetNetwork resolves via ovn first; a SR-IOV result routes the subnet to sriov.
		ovn.EXPECT().GetNetwork(gomock.Any(), gomock.Any()).Return(sriovNet(), nil)
		sriov.EXPECT().CreateSubnet(gomock.Any(), "sub", gomock.Any()).Return(&vivnfm.NetworkSubnet{}, nil)
		_, err := m.CreateSubnet(context.Background(), "sub", &vivnfm.NetworkSubnetData{NetworkId: k8stest.ID("uid-x")})
		require.NoError(t, err)
	})

	t.Run("no network id defaults subnet to ovn", func(t *testing.T) {
		ovn, _, m := setup(t)
		ovn.EXPECT().CreateSubnet(gomock.Any(), "sub", gomock.Any()).Return(&vivnfm.NetworkSubnet{}, nil)
		_, err := m.CreateSubnet(context.Background(), "sub", &vivnfm.NetworkSubnetData{})
		require.NoError(t, err)
	})
}

func TestSubnetReadsAlwaysOvn(t *testing.T) {
	t.Parallel()
	ovn, _, m := setup(t) // sriov must never be touched by subnet reads / mgmt network
	ovn.EXPECT().GetSubnet(gomock.Any()).Return(&vivnfm.NetworkSubnet{}, nil)
	ovn.EXPECT().ListSubnets(gomock.Any()).Return(nil, nil)
	ovn.EXPECT().DeleteSubnet(gomock.Any()).Return(nil)
	ovn.EXPECT().EnsureManagementNetwork(gomock.Any(), gomock.Any()).Return(nil)

	_, err := m.GetSubnet(context.Background())
	require.NoError(t, err)
	_, err = m.ListSubnets(context.Background())
	require.NoError(t, err)
	require.NoError(t, m.DeleteSubnet(context.Background()))
	require.NoError(t, m.EnsureManagementNetwork(context.Background(), nil))
}
