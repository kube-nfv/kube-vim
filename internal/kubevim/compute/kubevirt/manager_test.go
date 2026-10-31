package kubevirt

import (
	"context"
	"testing"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	flavourmock "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/mock"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	imagemock "github.com/kube-nfv/kube-vim/internal/kubevim/image/mock"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	networkmock "github.com/kube-nfv/kube-vim/internal/kubevim/network/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	testNamespace = k8stest.TestNamespace
	networkId     = "3e2d1c0b-9a8f-4e7d-8c6b-5a4f3e2d1c0b"
)

type computeMocks struct {
	flavour *flavourmock.MockManager
	image   *imagemock.MockManager
	network *networkmock.MockManager
}

// runningVmi is the VMI KubeVirt would run for vm.
func runningVmi(vm *kubevirtv1.VirtualMachine) kubevirtv1.VirtualMachineInstance {
	meta := k8stest.ManagedMeta(vm.Name)
	meta.Namespace = vm.Namespace
	return kubevirtv1.VirtualMachineInstance{
		ObjectMeta: meta,
		Spec:       vm.Spec.Template.Spec,
		Status: kubevirtv1.VirtualMachineInstanceStatus{
			Phase:    kubevirtv1.Running,
			NodeName: "worker-1",
			Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
				{Name: KubevirtVmMgmtNetworkName, IPs: []string{"10.16.0.5"}, MAC: "02:00:00:00:00:01", InterfaceName: "eth0"},
			},
		},
	}
}

// startVmis stands in for KubeVirt: every VM has a running VMI. VMIs are built on read, since the
// fake client cannot create one (its uint64 status field breaks the field tracker).
var startVmis = interceptor.Funcs{
	Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		vmi, ok := obj.(*kubevirtv1.VirtualMachineInstance)
		if !ok {
			return c.Get(ctx, key, obj, opts...)
		}
		vm := &kubevirtv1.VirtualMachine{}
		if err := c.Get(ctx, key, vm); err != nil {
			return err
		}
		*vmi = runningVmi(vm)
		return nil
	},
	List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		vmis, ok := list.(*kubevirtv1.VirtualMachineInstanceList)
		if !ok {
			return c.List(ctx, list, opts...)
		}
		vms := &kubevirtv1.VirtualMachineList{}
		if err := c.List(ctx, vms, opts...); err != nil {
			return err
		}
		for i := range vms.Items {
			vmis.Items = append(vmis.Items, runningVmi(&vms.Items[i]))
		}
		return nil
	},
}

func newComputeManager(t *testing.T) (*manager, computeMocks, client.Client) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := computeMocks{
		flavour: flavourmock.NewMockManager(ctrl),
		image:   imagemock.NewMockManager(ctrl),
		network: networkmock.NewMockManager(ctrl),
	}
	cl := interceptor.NewClient(k8stest.NewIndexedClient(t, IdIndexes), startVmis)
	ns := testNamespace
	mgr, err := NewComputeManager(cl, cl, &config.K8sConfig{Namespace: &ns}, nil, m.flavour, m.image, m.network)
	require.NoError(t, err)
	return mgr, m, cl
}

func bridged(name string) *computev1.NetworkInterfaceData {
	return &computev1.NetworkInterfaceData{Name: name, NetworkId: networkId}
}

func sriov(name string) *computev1.NetworkInterfaceData {
	return &computev1.NetworkInterfaceData{
		Name:           name,
		NetworkId:      networkId,
		TypeVirtualNic: computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_SRIOV,
		IpAssignment:   computev1.IpAssignment_IP_ASSIGNMENT_NONE,
		MacAddress:     "02:00:00:aa:bb:02",
	}
}

func createRequest(ifaces ...*computev1.NetworkInterfaceData) *computev1.CreateComputeRequest {
	return &computev1.CreateComputeRequest{
		Compute: &computev1.Compute{
			ComputeName: "Router_1",
			FlavourId:   "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d",
			ImageId:     "5f0c9d2e-3a4b-4c1d-9e8f-0a1b2c3d4e5f",
			Metadata:    map[string]string{"osm.etsi.org/vdu": "router"},
		},
		NetworkInterfaces: ifaces,
	}
}

func bootFlavour(storage ...*commonv1.VirtualStorageData) *flavourv1.Flavour {
	if storage == nil {
		storage = []*commonv1.VirtualStorageData{{TypeOfStorage: commonv1.TypeOfStorage_TYPE_OF_STORAGE_VOLUME, SizeOfStorage: "10Gi", IsBoot: true}}
	}
	return &flavourv1.Flavour{
		Metadata: map[string]string{
			flavour.InstancetypeMetadataKey: "flavour-f1",
			flavour.PreferenceMetadataKey:   "flavour-pref-f1",
		},
		StorageAttributes: storage,
	}
}

func availableImage() *imagev1.Image {
	return &imagev1.Image{
		ImageId:  "5f0c9d2e-3a4b-4c1d-9e8f-0a1b2c3d4e5f",
		Status:   imagev1.ImageStatus_IMAGE_STATUS_AVAILABLE,
		Size:     "2Gi",
		Metadata: map[string]string{image.PvcMetadataKey: "image-5f0c"},
	}
}

// expectRefs makes the flavour, image and network lookups of a create succeed.
func expectRefs(m computeMocks) {
	m.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(bootFlavour(), nil).AnyTimes()
	m.image.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(availableImage(), nil).AnyTimes()
	m.network.EXPECT().Attachment(gomock.Any(), networkId, network.NicTypeBridge).
		Return(&network.Attachment{NetAttachName: "net-a", LogicalSwitch: "net-a", Cidrs: []string{"10.0.0.0/24"}}, nil).AnyTimes()
	m.network.EXPECT().Attachment(gomock.Any(), networkId, network.NicTypeSriov).
		Return(&network.Attachment{NetAttachName: "net-a-sriov", Cidrs: []string{"10.0.0.0/24"}}, nil).AnyTimes()
}

func getVm(t *testing.T, cl client.Client, computeId string) *kubevirtv1.VirtualMachine {
	t.Helper()
	vm := &kubevirtv1.VirtualMachine{}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "vm-" + computeId}, vm))
	return vm
}

func TestCreateCompute(t *testing.T) {
	t.Parallel()
	t.Run("builds the VM from flavour, image and interfaces", func(t *testing.T) {
		m, mocks, cl := newComputeManager(t)
		expectRefs(mocks)
		static := bridged("")
		static.IpAssignment = computev1.IpAssignment_IP_ASSIGNMENT_STATIC
		static.IpAddresses = []string{"10.0.0.5"}
		req := createRequest(static, sriov("data1"))
		req.UserData = &computev1.UserData{Content: "#cloud-config\n"}

		got, err := m.CreateCompute(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, "Router_1", got.ComputeName, "the consumer's name, unchanged")
		assert.Equal(t, "worker-1", got.HostId)
		assert.Equal(t, computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STARTING, got.RunningState)
		require.Len(t, got.NetworkInterfaces, 3)
		mgmt, nic0, data1 := got.NetworkInterfaces[0], got.NetworkInterfaces[1], got.NetworkInterfaces[2]
		assert.True(t, mgmt.IsManagement)
		assert.Equal(t, []string{"10.16.0.5"}, mgmt.IpAddresses)
		assert.Equal(t, commonv1.OperationalState_OPERATIONAL_STATE_ENABLED, mgmt.OperationalState)
		assert.Equal(t, "nic0", nic0.Name)
		assert.Equal(t, computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_BRIDGE, nic0.TypeVirtualNic)
		assert.Equal(t, computev1.NicModel_NIC_MODEL_VIRTIO, nic0.Model)
		assert.Equal(t, computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_SRIOV, data1.TypeVirtualNic)
		assert.Equal(t, computev1.NicModel_NIC_MODEL_UNSPECIFIED, data1.Model)
		assert.Equal(t, networkId, data1.NetworkId)

		vm := getVm(t, cl, got.ComputeId)
		assert.Equal(t, got.ComputeId, vm.Labels[compute.K8sComputeIdLabel])
		assert.Equal(t, "router-1", vm.Spec.Template.Spec.Hostname)
		assert.Equal(t, "flavour-f1", vm.Spec.Instancetype.Name)
		assert.Equal(t, "image-5f0c", vm.Spec.DataVolumeTemplates[0].Spec.Source.PVC.Name)
		assert.Equal(t, "10Gi", vm.Spec.DataVolumeTemplates[0].Spec.Storage.Resources.Requests.Storage().String())
		ifaces := vm.Spec.Template.Spec.Domain.Devices.Interfaces
		require.Len(t, ifaces, 3)
		assert.NotNil(t, ifaces[0].Masquerade)
		assert.NotNil(t, ifaces[1].Bridge)
		assert.NotNil(t, ifaces[2].SRIOV)
		assert.Equal(t, "02:00:00:aa:bb:02", ifaces[2].MacAddress)
		assert.Equal(t, "net-a-sriov", vm.Spec.Template.Spec.Networks[2].Multus.NetworkName)
		ann := vm.Spec.Template.ObjectMeta.Annotations
		assert.Equal(t, "net-a", ann["net-a."+testNamespace+".ovn.kubernetes.io/logical_switch"])
		assert.Equal(t, "10.0.0.5", ann["net-a."+testNamespace+".ovn.kubernetes.io/ip_address"])
		assert.NotContains(t, vm.Annotations[compute.K8sComputeAnnotation], "cloud-config", "user data is not stored")

		secret := &corev1.Secret{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: vm.Name + KubevirtVmCloudInitSecretSuffix}, secret))
	})

	t.Run("an image that is not available is an invalid state", func(t *testing.T) {
		m, mocks, _ := newComputeManager(t)
		img := availableImage()
		img.Status = imagev1.ImageStatus_IMAGE_STATUS_IMPORTING
		mocks.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(bootFlavour(), nil)
		mocks.image.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(img, nil)
		_, err := m.CreateCompute(context.Background(), createRequest())
		var invalidState *apperrors.ErrInvalidState
		assert.ErrorAs(t, err, &invalidState)
	})

	t.Run("flavour disks beyond the boot disk are unsupported", func(t *testing.T) {
		m, mocks, _ := newComputeManager(t)
		disk := &commonv1.VirtualStorageData{TypeOfStorage: commonv1.TypeOfStorage_TYPE_OF_STORAGE_VOLUME, SizeOfStorage: "10Gi"}
		mocks.flavour.EXPECT().GetFlavour(gomock.Any(), gomock.Any()).Return(bootFlavour(bootFlavour().StorageAttributes[0], disk), nil)
		mocks.image.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(availableImage(), nil)
		_, err := m.CreateCompute(context.Background(), createRequest())
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("a static address outside the network is rejected", func(t *testing.T) {
		m, mocks, _ := newComputeManager(t)
		expectRefs(mocks)
		static := bridged("data0")
		static.IpAssignment = computev1.IpAssignment_IP_ASSIGNMENT_STATIC
		static.IpAddresses = []string{"192.168.1.5"}
		_, err := m.CreateCompute(context.Background(), createRequest(static))
		var invalid *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &invalid)
	})
}

func TestGetListDeleteCompute(t *testing.T) {
	t.Parallel()
	m, mocks, cl := newComputeManager(t)
	expectRefs(mocks)
	req := createRequest(bridged("data0"))
	req.UserData = &computev1.UserData{Content: "#cloud-config\n"}
	created, err := m.CreateCompute(context.Background(), req)
	require.NoError(t, err)

	got, err := m.GetCompute(context.Background(), created.ComputeId)
	require.NoError(t, err)
	assert.Equal(t, created.ComputeId, got.ComputeId)
	assert.Equal(t, "router", got.Metadata["osm.etsi.org/vdu"])

	list, err := m.ListComputes(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, created.ComputeId, list[0].ComputeId)

	vmName := "vm-" + created.ComputeId
	require.NoError(t, m.DeleteCompute(context.Background(), created.ComputeId))
	err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: vmName}, &kubevirtv1.VirtualMachine{})
	assert.True(t, apierrors.IsNotFound(err), "VM should be gone")
	err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: vmName + KubevirtVmCloudInitSecretSuffix}, &corev1.Secret{})
	assert.True(t, apierrors.IsNotFound(err), "cloud-init secret should be gone")

	var notFound *apperrors.ErrNotFound
	_, err = m.GetCompute(context.Background(), created.ComputeId)
	assert.ErrorAs(t, err, &notFound)
}
