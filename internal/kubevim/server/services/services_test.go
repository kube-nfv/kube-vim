package services

import (
	"context"
	"testing"

	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	computemock "github.com/kube-nfv/kube-vim/internal/kubevim/compute/mock"
	flavourmock "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListFlavoursFilter(t *testing.T) {
	t.Parallel()
	flavours := []*flavourv1.Flavour{{FlavourId: "a", FlavourName: "small"}, {FlavourId: "b", FlavourName: "large"}}
	tests := []struct {
		name    string
		req     *flavourv1.ListFlavoursRequest
		wantIds []string
		wantErr error
	}{
		{name: "no filter returns all", req: &flavourv1.ListFlavoursRequest{}, wantIds: []string{"a", "b"}},
		{name: "filter by JSON name", req: &flavourv1.ListFlavoursRequest{Filter: "(eq,flavourName,large)"}, wantIds: []string{"b"}},
		{name: "malformed filter", req: &flavourv1.ListFlavoursRequest{Filter: "(eq,flavourName"}, wantErr: apperrors.ErrInvalidFilter},
		{name: "paging marker is unsupported", req: &flavourv1.ListFlavoursRequest{NextpageOpaqueMarker: "x"}, wantErr: apperrors.ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := flavourmock.NewMockManager(gomock.NewController(t))
			mgr.EXPECT().ListFlavours(gomock.Any()).Return(flavours, nil)
			s := &FlavourService{Mgr: mgr}
			resp, err := s.ListFlavours(context.Background(), tt.req)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			var ids []string
			for _, f := range resp.GetFlavours() {
				ids = append(ids, f.GetFlavourId())
			}
			assert.Equal(t, tt.wantIds, ids)
		})
	}
}

func TestDeleteCompute(t *testing.T) {
	t.Parallel()
	t.Run("delegates the id", func(t *testing.T) {
		mgr := computemock.NewMockManager(gomock.NewController(t))
		mgr.EXPECT().DeleteCompute(gomock.Any(), "id1").Return(nil)
		_, err := (&ComputeService{Mgr: mgr}).DeleteCompute(context.Background(), &computev1.DeleteComputeRequest{ComputeId: "id1"})
		require.NoError(t, err)
	})
	t.Run("delete_volumes is unsupported", func(t *testing.T) {
		mgr := computemock.NewMockManager(gomock.NewController(t))
		_, err := (&ComputeService{Mgr: mgr}).DeleteCompute(context.Background(), &computev1.DeleteComputeRequest{ComputeId: "id1", DeleteVolumes: true})
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})
}
