package kubevirt

import (
	"context"
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/kubevirt"
	flavourmock "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/mock"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	imagemock "github.com/kube-nfv/kube-vim/internal/kubevim/image/mock"
	networkmock "github.com/kube-nfv/kube-vim/internal/kubevim/network/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// imageManagerMock satisfies image.Manager: the gRPC admin.AdminServer surface
// comes from the embedded UnimplementedAdminServer (which gomock cannot generate),
// while the ETSI query surface is a gomock.
type imageManagerMock struct {
	admin.UnimplementedAdminServer
	*imagemock.MockNfvImageManager
}

type computeMocks struct {
	flavour *flavourmock.MockManager
	image   *imagemock.MockNfvImageManager
	network *networkmock.MockManager
}

func newComputeManager(t *testing.T, objs ...client.Object) (*manager, computeMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := computeMocks{
		flavour: flavourmock.NewMockManager(ctrl),
		image:   imagemock.NewMockNfvImageManager(ctrl),
		network: networkmock.NewMockManager(ctrl),
	}
	cl := k8stest.NewClient(t, objs...)
	ns := k8stest.TestNamespace
	mgr, err := NewComputeManager(cl, cl, &config.K8sConfig{Namespace: &ns}, nil,
		m.flavour, &imageManagerMock{MockNfvImageManager: m.image}, m.network)
	require.NoError(t, err)
	return mgr, m
}

func seedVM(name string) *kubevirtv1.VirtualMachine {
	meta := k8stest.ManagedMeta(name)
	meta.Namespace = k8stest.TestNamespace
	return &kubevirtv1.VirtualMachine{ObjectMeta: meta}
}

func TestListComputeResources(t *testing.T) {
	t.Parallel()
	t.Run("empty cluster returns nothing", func(t *testing.T) {
		m, _ := newComputeManager(t)
		got, err := m.ListComputeResources(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("a VM with no VMI yet is skipped, not an error", func(t *testing.T) {
		m, _ := newComputeManager(t, seedVM("vm1"))
		got, err := m.ListComputeResources(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// kubevirtFlavour builds a flavour whose metadata carries the annotations
// AllocateComputeResource requires (kubevirt source + instancetype/preference).
func kubevirtFlavour() *vivnfm.VirtualComputeFlavour {
	boot := true
	bootSize := resource.MustParse("10Gi")
	return &vivnfm.VirtualComputeFlavour{
		FlavourId: k8stest.ID("f1"),
		Metadata: &nfvcommon.Metadata{Fields: map[string]string{
			flavour.K8sFlavourSourceLabel:     kubevirt_flavour.KubevirtFlavourSource,
			kubevirtv1.InstancetypeAnnotation: "flavour-f1",
			kubevirtv1.PreferenceAnnotation:   "flavour-pref-f1",
		}},
		StorageAttributes: []*vivnfm.VirtualStorageData{{IsBoot: &boot, SizeOfStorage: &bootSize}},
	}
}

func readyImage() *vivnfm.SoftwareImageInformation {
	size := resource.MustParse("2Gi")
	return &vivnfm.SoftwareImageInformation{
		SoftwareImageId: k8stest.ID("img1"),
		Name:            "img1",
		Status:          "ready",
		Size:            &size,
	}
}

// podOnlyVMI is what KubeVirt would create for a VM with only the management
// (pod) network; the fake client has no controller, so the test seeds it so
// waitForVmi finds it and the VM->nfv conversion has an interface to read.
func podOnlyVMI(name string) *kubevirtv1.VirtualMachineInstance {
	meta := k8stest.ManagedMeta(name)
	meta.Namespace = k8stest.TestNamespace
	return &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: meta,
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Networks: []kubevirtv1.Network{{
				Name:          KubevirtVmMgmtNetworkName,
				NetworkSource: kubevirtv1.NetworkSource{Pod: &kubevirtv1.PodNetwork{}},
			}},
			Domain: kubevirtv1.DomainSpec{Devices: kubevirtv1.Devices{Interfaces: []kubevirtv1.Interface{{
				Name:                   KubevirtVmMgmtNetworkName,
				InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{Masquerade: &kubevirtv1.InterfaceMasquerade{}},
			}}}},
		},
	}
}

func allocateReq() *vivnfm.AllocateComputeRequest {
	return &vivnfm.AllocateComputeRequest{
		ComputeName:      k8stest.Ptr("myvm"),
		ComputeFlavourId: k8stest.ID("f1"),
		VcImageId:        k8stest.ID("img1"),
	}
}

func TestAllocateComputeResource(t *testing.T) {
	t.Parallel()
	t.Run("builds the VM and returns the compute (pod network only)", func(t *testing.T) {
		// Seed the VMI KubeVirt would create, so the post-create wait resolves.
		m, mocks := newComputeManager(t, podOnlyVMI("myvm"))
		mocks.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(kubevirtFlavour(), nil)
		mocks.image.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(readyImage(), nil)
		// No InterfaceData => resolveInterfaces makes no network calls.

		got, err := m.AllocateComputeResource(context.Background(), allocateReq())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "myvm", got.GetComputeName())

		// The VM was persisted with the flavour/image ownership labels and a boot DV.
		cl := m.client
		vm := &kubevirtv1.VirtualMachine{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: k8stest.TestNamespace, Name: "myvm"}, vm))
		assert.Equal(t, "f1", vm.Labels[flavour.K8sFlavourIdLabel])
		assert.Equal(t, "img1", vm.Labels[image.K8sImageIdLabel])
		require.Len(t, vm.Spec.DataVolumeTemplates, 1)
		assert.Equal(t, "myvm-boot-dv", vm.Spec.DataVolumeTemplates[0].Name)
	})

	t.Run("nil request is rejected", func(t *testing.T) {
		m, _ := newComputeManager(t)
		_, err := m.AllocateComputeResource(context.Background(), nil)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("missing flavour id is rejected before any manager call", func(t *testing.T) {
		m, _ := newComputeManager(t) // no mock expectations => no delegation
		_, err := m.AllocateComputeResource(context.Background(), &vivnfm.AllocateComputeRequest{VcImageId: k8stest.ID("img1")})
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("flavour lookup error is wrapped", func(t *testing.T) {
		m, mocks := newComputeManager(t)
		mocks.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(nil, &apperrors.ErrNotFound{Entity: "flavour"})
		_, err := m.AllocateComputeResource(context.Background(), allocateReq())
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})

	t.Run("non-kubevirt flavour source is unsupported", func(t *testing.T) {
		m, mocks := newComputeManager(t)
		flav := kubevirtFlavour()
		flav.Metadata.Fields[flavour.K8sFlavourSourceLabel] = "openstack"
		mocks.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(flav, nil)
		_, err := m.AllocateComputeResource(context.Background(), allocateReq())
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("image lookup error is wrapped", func(t *testing.T) {
		m, mocks := newComputeManager(t)
		mocks.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(kubevirtFlavour(), nil)
		mocks.image.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(nil, &apperrors.ErrNotFound{Entity: "image"})
		_, err := m.AllocateComputeResource(context.Background(), allocateReq())
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}
