package kubevirt

import (
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

func TestGetRunningState(t *testing.T) {
	tests := []struct {
		name string
		vm   *kubevirtv1.VirtualMachine
		vmi  *kubevirtv1.VirtualMachineInstance
		want nfvcommon.ComputeRunningState
	}{
		{
			name: "halted -> stopped",
			vm:   &kubevirtv1.VirtualMachine{Status: kubevirtv1.VirtualMachineStatus{RunStrategy: kubevirtv1.RunStrategyHalted}},
			vmi:  &kubevirtv1.VirtualMachineInstance{},
			want: nfvcommon.ComputeRunningState_STOPPED,
		},
		{
			name: "paused condition -> paused",
			vm:   &kubevirtv1.VirtualMachine{},
			vmi: &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{
				Conditions: []kubevirtv1.VirtualMachineInstanceCondition{{Type: kubevirtv1.VirtualMachineInstancePaused}},
			}},
			want: nfvcommon.ComputeRunningState_PAUSED,
		},
		{
			name: "terminating",
			vm:   &kubevirtv1.VirtualMachine{Status: kubevirtv1.VirtualMachineStatus{PrintableStatus: kubevirtv1.VirtualMachineStatusTerminating}},
			vmi:  &kubevirtv1.VirtualMachineInstance{},
			want: nfvcommon.ComputeRunningState_TERMINATING,
		},
		{
			name: "running",
			vm:   &kubevirtv1.VirtualMachine{Status: kubevirtv1.VirtualMachineStatus{Created: true, Ready: true}},
			vmi:  &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{Phase: kubevirtv1.Running}},
			want: nfvcommon.ComputeRunningState_RUNNING,
		},
		{
			name: "pending -> starting",
			vm:   &kubevirtv1.VirtualMachine{},
			vmi:  &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{Phase: kubevirtv1.Pending}},
			want: nfvcommon.ComputeRunningState_STARTING,
		},
		{
			name: "failed",
			vm:   &kubevirtv1.VirtualMachine{},
			vmi:  &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{Phase: kubevirtv1.Failed}},
			want: nfvcommon.ComputeRunningState_FAILED,
		},
		{
			name: "unknown",
			vm:   &kubevirtv1.VirtualMachine{},
			vmi:  &kubevirtv1.VirtualMachineInstance{},
			want: nfvcommon.ComputeRunningState_UNKNOWN,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, getRunningState(tt.vm, tt.vmi))
		})
	}
}

func TestGetFlavourFromInstanceSpec(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		vm := &kubevirtv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{flavour.K8sFlavourIdLabel: "flav-1"},
		}}
		id, err := getFlavourFromInstanceSpec(vm)
		require.NoError(t, err)
		assert.Equal(t, "flav-1", id.GetValue())
	})
	t.Run("missing", func(t *testing.T) {
		_, err := getFlavourFromInstanceSpec(&kubevirtv1.VirtualMachine{})
		assert.Error(t, err)
	})
}

func TestGetImageIdFromInstnceSpec(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		vm := &kubevirtv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{image.K8sImageIdLabel: "img-1"},
		}}
		id, err := getImageIdFromInstnceSpec(vm)
		require.NoError(t, err)
		assert.Equal(t, "img-1", id.GetValue())
	})
	t.Run("missing", func(t *testing.T) {
		_, err := getImageIdFromInstnceSpec(&kubevirtv1.VirtualMachine{})
		assert.Error(t, err)
	})
}

func TestIfaceBindingMethodToNfv(t *testing.T) {
	tests := []struct {
		name    string
		method  kubevirtv1.InterfaceBindingMethod
		want    nfvcommon.TypeVirtualNic
		wantErr bool
	}{
		{"bridge", kubevirtv1.InterfaceBindingMethod{Bridge: &kubevirtv1.InterfaceBridge{}}, nfvcommon.TypeVirtualNic_TYPE_VIRTUAL_NIC_BRIDGE, false},
		{"masquerade", kubevirtv1.InterfaceBindingMethod{Masquerade: &kubevirtv1.InterfaceMasquerade{}}, nfvcommon.TypeVirtualNic_TYPE_VIRTUAL_NIC_BRIDGE, false},
		{"sriov", kubevirtv1.InterfaceBindingMethod{SRIOV: &kubevirtv1.InterfaceSRIOV{}}, nfvcommon.TypeVirtualNic_TYPE_VIRTUAL_NIC_SRIOV, false},
		{"unknown", kubevirtv1.InterfaceBindingMethod{}, nfvcommon.TypeVirtualNic_TYPE_VIRTUAL_NIC_BRIDGE, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ifaceBindingMethodToNfv(tt.method)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetNetworkFromVm(t *testing.T) {
	vm := &kubevirtv1.VirtualMachine{Spec: kubevirtv1.VirtualMachineSpec{
		Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
			Spec: kubevirtv1.VirtualMachineInstanceSpec{
				Networks: []kubevirtv1.Network{{Name: "net1"}},
			},
		},
	}}
	t.Run("found", func(t *testing.T) {
		net, err := getNetworkFromVm("net1", vm)
		require.NoError(t, err)
		assert.Equal(t, "net1", net.Name)
	})
	t.Run("not found", func(t *testing.T) {
		_, err := getNetworkFromVm("missing", vm)
		assert.Error(t, err)
	})
	t.Run("nil template", func(t *testing.T) {
		_, err := getNetworkFromVm("net1", &kubevirtv1.VirtualMachine{})
		assert.Error(t, err)
	})
}

func TestGetInterfaceFromVm(t *testing.T) {
	vm := &kubevirtv1.VirtualMachine{Spec: kubevirtv1.VirtualMachineSpec{
		Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
			Spec: kubevirtv1.VirtualMachineInstanceSpec{
				Domain: kubevirtv1.DomainSpec{Devices: kubevirtv1.Devices{
					Interfaces: []kubevirtv1.Interface{{Name: "iface1"}},
				}},
			},
		},
	}}
	t.Run("found", func(t *testing.T) {
		iface, err := getInterfaceFromVm("iface1", vm)
		require.NoError(t, err)
		assert.Equal(t, "iface1", iface.Name)
	})
	t.Run("not found", func(t *testing.T) {
		_, err := getInterfaceFromVm("missing", vm)
		assert.Error(t, err)
	})
}
