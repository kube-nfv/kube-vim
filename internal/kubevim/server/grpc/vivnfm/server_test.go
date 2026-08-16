package vivnfm

import (
	"context"
	"errors"
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	computemock "github.com/kube-nfv/kube-vim/internal/kubevim/compute/mock"
	flavourmock "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/mock"
	imagemock "github.com/kube-nfv/kube-vim/internal/kubevim/image/mock"
	networkmock "github.com/kube-nfv/kube-vim/internal/kubevim/network/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// imageManagerMock satisfies image.Manager: the gRPC admin.AdminServer surface
// comes from the embedded UnimplementedAdminServer (which gomock cannot generate,
// due to its forced mustEmbed method), while the ETSI query surface is a gomock.
type imageManagerMock struct {
	admin.UnimplementedAdminServer
	*imagemock.MockNfvImageManager
}

type mocks struct {
	image   *imagemock.MockNfvImageManager
	flavour *flavourmock.MockManager
	network *networkmock.MockManager
	compute *computemock.MockManager
}

func newServer(t *testing.T) (*ViVnfmServer, mocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := mocks{
		image:   imagemock.NewMockNfvImageManager(ctrl),
		flavour: flavourmock.NewMockManager(ctrl),
		network: networkmock.NewMockManager(ctrl),
		compute: computemock.NewMockManager(ctrl),
	}
	s := &ViVnfmServer{
		ImageMgr:   &imageManagerMock{MockNfvImageManager: m.image},
		FlavourMgr: m.flavour,
		NetworkMgr: m.network,
		ComputeMgr: m.compute,
	}
	return s, m
}

func id(v string) *nfvcommon.Identifier { return &nfvcommon.Identifier{Value: v} }

func TestQueryImages(t *testing.T) {
	t.Run("delegates and returns the manager list", func(t *testing.T) {
		s, m := newServer(t)
		m.image.EXPECT().ListImages(gomock.Any()).Return([]*vivnfm.SoftwareImageInformation{{}, {}}, nil)
		resp, err := s.QueryImages(context.Background(), &vivnfm.QueryImagesRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.SoftwareImagesInformation, 2)
	})

	t.Run("wraps a manager error", func(t *testing.T) {
		s, m := newServer(t)
		m.image.EXPECT().ListImages(gomock.Any()).Return(nil, errors.New("boom"))
		_, err := s.QueryImages(context.Background(), &vivnfm.QueryImagesRequest{})
		require.Error(t, err)
	})
}

func TestQueryImage(t *testing.T) {
	s, m := newServer(t)
	img := &vivnfm.SoftwareImageInformation{SoftwareImageId: id("img1")}
	m.image.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(img, nil)
	resp, err := s.QueryImage(context.Background(), &vivnfm.QueryImageRequest{SoftwareImageId: id("img1")})
	require.NoError(t, err)
	assert.Equal(t, "img1", resp.SoftwareImageInformation.SoftwareImageId.GetValue())
}

func TestCreateComputeFlavour(t *testing.T) {
	s, m := newServer(t)
	m.flavour.EXPECT().CreateFlavour(gomock.Any(), gomock.Any()).Return(id("f1"), nil)
	resp, err := s.CreateComputeFlavour(context.Background(), &vivnfm.CreateComputeFlavourRequest{Flavour: &vivnfm.VirtualComputeFlavour{}})
	require.NoError(t, err)
	assert.Equal(t, "f1", resp.FlavourId.GetValue())
}

func TestDeleteComputeFlavour(t *testing.T) {
	t.Run("success returns empty response", func(t *testing.T) {
		s, m := newServer(t)
		m.flavour.EXPECT().DeleteFlavour(gomock.Any(), gomock.Any()).Return(nil)
		_, err := s.DeleteComputeFlavour(context.Background(), &vivnfm.DeleteComputeFlavourRequest{ComputeFlavourId: id("f1")})
		require.NoError(t, err)
	})

	t.Run("wraps a manager error", func(t *testing.T) {
		s, m := newServer(t)
		m.flavour.EXPECT().DeleteFlavour(gomock.Any(), gomock.Any()).Return(errors.New("boom"))
		_, err := s.DeleteComputeFlavour(context.Background(), &vivnfm.DeleteComputeFlavourRequest{ComputeFlavourId: id("f1")})
		require.Error(t, err)
	})
}

func TestQueryComputeFlavour(t *testing.T) {
	s, m := newServer(t)
	m.flavour.EXPECT().GetFlavours(gomock.Any()).Return([]*vivnfm.VirtualComputeFlavour{{}, {}}, nil)
	resp, err := s.QueryComputeFlavour(context.Background(), &vivnfm.QueryComputeFlavourRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Flavours, 2)
}

func TestQueryVirtualisedComputeResource(t *testing.T) {
	t.Run("lists and returns compute resources", func(t *testing.T) {
		s, m := newServer(t)
		m.compute.EXPECT().ListComputeResources(gomock.Any()).Return([]*vivnfm.VirtualCompute{{}}, nil)
		resp, err := s.QueryVirtualisedComputeResource(context.Background(), &vivnfm.QueryComputeRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.QueryResult, 1)
	})

	t.Run("wraps a manager error", func(t *testing.T) {
		s, m := newServer(t)
		m.compute.EXPECT().ListComputeResources(gomock.Any()).Return(nil, errors.New("boom"))
		_, err := s.QueryVirtualisedComputeResource(context.Background(), &vivnfm.QueryComputeRequest{})
		require.Error(t, err)
	})
}

func TestTerminateVirtualisedComputeResource(t *testing.T) {
	t.Run("delegates deletion and echoes the id", func(t *testing.T) {
		s, m := newServer(t)
		m.compute.EXPECT().DeleteComputeResource(gomock.Any(), gomock.Any()).Return(nil)
		resp, err := s.TerminateVirtualisedComputeResource(context.Background(), &vivnfm.TerminateComputeRequest{ComputeId: id("c1")})
		require.NoError(t, err)
		assert.Equal(t, "c1", resp.ComputeId.GetValue())
	})

	t.Run("wraps a manager error", func(t *testing.T) {
		s, m := newServer(t)
		m.compute.EXPECT().DeleteComputeResource(gomock.Any(), gomock.Any()).Return(errors.New("boom"))
		_, err := s.TerminateVirtualisedComputeResource(context.Background(), &vivnfm.TerminateComputeRequest{ComputeId: id("c1")})
		require.Error(t, err)
	})
}

func TestAllocateVirtualisedNetworkResource(t *testing.T) {
	name := "net1"

	t.Run("nil request is InvalidArgument", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.AllocateVirtualisedNetworkResource(context.Background(), nil)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("empty name is InvalidArgument", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.AllocateVirtualisedNetworkResource(context.Background(), &vivnfm.AllocateNetworkRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("network type without data is InvalidArgument", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.AllocateVirtualisedNetworkResource(context.Background(), &vivnfm.AllocateNetworkRequest{
			NetworkResourceName: &name,
			NetworkResourceType: nfvcommon.NetworkResourceType_NETWORK,
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("network type delegates to CreateNetwork", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().CreateNetwork(gomock.Any(), name, gomock.Any()).Return(&vivnfm.VirtualNetwork{}, nil)
		resp, err := s.AllocateVirtualisedNetworkResource(context.Background(), &vivnfm.AllocateNetworkRequest{
			NetworkResourceName: &name,
			NetworkResourceType: nfvcommon.NetworkResourceType_NETWORK,
			TypeNetworkData:     &vivnfm.VirtualNetworkData{},
		})
		require.NoError(t, err)
		assert.NotNil(t, resp.NetworkData)
	})

	t.Run("subnet type without data is InvalidArgument", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.AllocateVirtualisedNetworkResource(context.Background(), &vivnfm.AllocateNetworkRequest{
			NetworkResourceName: &name,
			NetworkResourceType: nfvcommon.NetworkResourceType_SUBNET,
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("subnet type delegates to CreateSubnet", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().CreateSubnet(gomock.Any(), name, gomock.Any()).Return(&vivnfm.NetworkSubnet{}, nil)
		resp, err := s.AllocateVirtualisedNetworkResource(context.Background(), &vivnfm.AllocateNetworkRequest{
			NetworkResourceName: &name,
			NetworkResourceType: nfvcommon.NetworkResourceType_SUBNET,
			TypeSubnetData:      &vivnfm.NetworkSubnetData{},
		})
		require.NoError(t, err)
		assert.NotNil(t, resp.SubnetData)
	})
}

func TestQueryVirtualisedNetworkResource(t *testing.T) {
	t.Run("nil request is InvalidArgument", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.QueryVirtualisedNetworkResource(context.Background(), nil)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("network type lists networks", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().ListNetworks(gomock.Any()).Return([]*vivnfm.VirtualNetwork{{}, {}}, nil)
		resp, err := s.QueryVirtualisedNetworkResource(context.Background(), &vivnfm.QueryNetworkRequest{NetworkResourceType: nfvcommon.NetworkResourceType_NETWORK})
		require.NoError(t, err)
		assert.Len(t, resp.QueryNetworkResult, 2)
	})

	t.Run("subnet type lists subnets", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().ListSubnets(gomock.Any()).Return([]*vivnfm.NetworkSubnet{{}}, nil)
		resp, err := s.QueryVirtualisedNetworkResource(context.Background(), &vivnfm.QueryNetworkRequest{NetworkResourceType: nfvcommon.NetworkResourceType_SUBNET})
		require.NoError(t, err)
		assert.Len(t, resp.QuerySubnetResult, 1)
	})
}

func TestTerminateVirtualisedNetworkResource(t *testing.T) {
	t.Run("network deletion echoes the id", func(t *testing.T) {
		s, m := newServer(t) // subnet path must not run when network delete succeeds
		m.network.EXPECT().DeleteNetwork(gomock.Any(), gomock.Any()).Return(nil)
		resp, err := s.TerminateVirtualisedNetworkResource(context.Background(), &vivnfm.TerminateNetworkRequest{NetworkResourceId: id("r1")})
		require.NoError(t, err)
		assert.Equal(t, "r1", resp.NetworkResourceId.GetValue())
	})

	t.Run("network not-found falls through to subnet deletion", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().DeleteNetwork(gomock.Any(), gomock.Any()).Return(&apperrors.ErrNotFound{Entity: "network"})
		m.network.EXPECT().DeleteSubnet(gomock.Any(), gomock.Any()).Return(nil)
		resp, err := s.TerminateVirtualisedNetworkResource(context.Background(), &vivnfm.TerminateNetworkRequest{NetworkResourceId: id("r1")})
		require.NoError(t, err)
		assert.Equal(t, "r1", resp.NetworkResourceId.GetValue())
	})

	t.Run("hard network error does not attempt subnet deletion", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().DeleteNetwork(gomock.Any(), gomock.Any()).Return(errors.New("boom"))
		_, err := s.TerminateVirtualisedNetworkResource(context.Background(), &vivnfm.TerminateNetworkRequest{NetworkResourceId: id("r1")})
		require.Error(t, err)
	})

	t.Run("both not-found is an error", func(t *testing.T) {
		s, m := newServer(t)
		m.network.EXPECT().DeleteNetwork(gomock.Any(), gomock.Any()).Return(&apperrors.ErrNotFound{Entity: "network"})
		m.network.EXPECT().DeleteSubnet(gomock.Any(), gomock.Any()).Return(&apperrors.ErrNotFound{Entity: "subnet"})
		_, err := s.TerminateVirtualisedNetworkResource(context.Background(), &vivnfm.TerminateNetworkRequest{NetworkResourceId: id("r1")})
		require.Error(t, err)
	})
}
