package services

import (
	"context"
	"testing"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	computemock "github.com/kube-nfv/kube-vim/internal/kubevim/compute/mock"
	flavourmock "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/mock"
	imagemock "github.com/kube-nfv/kube-vim/internal/kubevim/image/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListFlavoursFilterBySize(t *testing.T) {
	t.Parallel()
	flavours := []*flavourv1.Flavour{
		{
			FlavourId:         "small",
			VirtualMemory:     &flavourv1.VirtualMemoryData{VirtualMemSize: "2G", HugepageSize: "2Mi"},
			StorageAttributes: []*commonv1.VirtualStorageData{{SizeOfStorage: "10Gi"}},
		},
		{
			FlavourId:     "large",
			VirtualMemory: &flavourv1.VirtualMemoryData{VirtualMemSize: "10Gi"},
		},
	}
	tests := []struct {
		name    string
		filter  string
		wantIds []string
		wantErr error
	}{
		{name: "eq by value, not by spelling", filter: "(eq,virtualMemory/virtualMemSize,2000M)", wantIds: []string{"small"}},
		{name: "neq by value", filter: "(neq,virtualMemory/virtualMemSize,2000M)", wantIds: []string{"large"}},
		{name: "gt by value, not lexicographic", filter: "(gt,virtualMemory/virtualMemSize,3Gi)", wantIds: []string{"large"}},
		{name: "lt by value, not lexicographic", filter: "(lt,virtualMemory/virtualMemSize,3Gi)", wantIds: []string{"small"}},
		{name: "hugepage size, unset does not match", filter: "(eq,virtualMemory/hugepageSize,2048Ki)", wantIds: []string{"small"}},
		{name: "storage attribute", filter: "(eq,storageAttributes/sizeOfStorage,10240Mi)", wantIds: []string{"small"}},
		{name: "not a quantity", filter: "(eq,virtualMemory/virtualMemSize,big)", wantErr: apperrors.ErrInvalidFilter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := flavourmock.NewMockManager(gomock.NewController(t))
			mgr.EXPECT().ListFlavours(gomock.Any()).Return(flavours, nil)
			resp, err := (&FlavourService{Mgr: mgr}).ListFlavours(context.Background(), &flavourv1.ListFlavoursRequest{Filter: tt.filter})
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

func TestListComputesFilterBySize(t *testing.T) {
	t.Parallel()
	computes := []*computev1.Compute{{
		ComputeId:     "a",
		VirtualMemory: &computev1.VirtualMemory{VirtualMemSize: "2G", HugepageSize: "1Gi"},
		Disks:         []*commonv1.Volume{{StorageData: &commonv1.VirtualStorageData{SizeOfStorage: "20Gi"}}},
	}}
	for _, filter := range []string{
		"(eq,virtualMemory/virtualMemSize,2000M)",
		"(eq,virtualMemory/hugepageSize,1024Mi)",
		"(eq,disks/storageData/sizeOfStorage,20480Mi)",
	} {
		t.Run(filter, func(t *testing.T) {
			mgr := computemock.NewMockManager(gomock.NewController(t))
			mgr.EXPECT().ListComputes(gomock.Any()).Return(computes, nil)
			resp, err := (&ComputeService{Mgr: mgr}).ListComputes(context.Background(), &computev1.ListComputesRequest{Filter: filter})
			require.NoError(t, err)
			assert.Len(t, resp.GetComputes(), 1)
		})
	}
}

func TestListComputesFilterWithoutMemory(t *testing.T) {
	t.Parallel()
	computes := []*computev1.Compute{
		{ComputeId: "a", VirtualMemory: &computev1.VirtualMemory{VirtualMemSize: "2G"}},
		{ComputeId: "no-memory"},
	}
	mgr := computemock.NewMockManager(gomock.NewController(t))
	mgr.EXPECT().ListComputes(gomock.Any()).Return(computes, nil)
	resp, err := (&ComputeService{Mgr: mgr}).ListComputes(context.Background(), &computev1.ListComputesRequest{Filter: "(eq,virtualMemory/virtualMemSize,2000M)"})
	require.NoError(t, err)
	require.Len(t, resp.GetComputes(), 1)
	assert.Equal(t, "a", resp.GetComputes()[0].GetComputeId())
}

func TestListImagesFilterBySize(t *testing.T) {
	t.Parallel()
	images := []*imagev1.Image{
		{ImageId: "a", MinDisk: "1G", MinRam: "2G", Size: "3G"},
		{ImageId: "importing"},
	}
	for _, filter := range []string{
		"(eq,minDisk,1000M)",
		"(eq,minRam,2000M)",
		"(eq,size,3000M)",
	} {
		t.Run(filter, func(t *testing.T) {
			mgr := imagemock.NewMockManager(gomock.NewController(t))
			mgr.EXPECT().ListImages(gomock.Any()).Return(images, nil)
			resp, err := (&ImageService{Mgr: mgr}).ListImages(context.Background(), &imagev1.ListImagesRequest{Filter: filter})
			require.NoError(t, err)
			require.Len(t, resp.GetImages(), 1)
			assert.Equal(t, "a", resp.GetImages()[0].GetImageId())
		})
	}
}
