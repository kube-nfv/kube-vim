package kubevirt

import (
	"testing"

	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

func TestRunningState(t *testing.T) {
	halted := kubevirtv1.VirtualMachineStatus{RunStrategy: kubevirtv1.RunStrategyHalted}
	phase := func(p kubevirtv1.VirtualMachineInstancePhase) *kubevirtv1.VirtualMachineInstance {
		return &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{Phase: p}}
	}
	tests := []struct {
		name       string
		vm         *kubevirtv1.VirtualMachine
		vmi        *kubevirtv1.VirtualMachineInstance
		want       computev1.ComputeRunningState
		wantDetail string
	}{
		{name: "halted -> stopped", vm: &kubevirtv1.VirtualMachine{Status: halted}, vmi: &kubevirtv1.VirtualMachineInstance{}, want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STOPPED},
		{name: "halted while running -> stopping", vm: &kubevirtv1.VirtualMachine{Status: halted}, vmi: phase(kubevirtv1.Running), want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STOPPING},
		{
			name: "paused condition -> paused",
			vm:   &kubevirtv1.VirtualMachine{},
			vmi: &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{
				Conditions: []kubevirtv1.VirtualMachineInstanceCondition{{Type: kubevirtv1.VirtualMachineInstancePaused}},
			}},
			want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_PAUSED,
		},
		{
			name: "terminating",
			vm:   &kubevirtv1.VirtualMachine{Status: kubevirtv1.VirtualMachineStatus{PrintableStatus: kubevirtv1.VirtualMachineStatusTerminating}},
			vmi:  &kubevirtv1.VirtualMachineInstance{},
			want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_TERMINATING,
		},
		{
			name: "running and ready -> started",
			vm:   &kubevirtv1.VirtualMachine{Status: kubevirtv1.VirtualMachineStatus{Created: true, Ready: true}},
			vmi:  phase(kubevirtv1.Running),
			want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STARTED,
		},
		{name: "pending -> starting", vm: &kubevirtv1.VirtualMachine{}, vmi: phase(kubevirtv1.Pending), want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STARTING},
		{
			name: "failed with its reason",
			vm:   &kubevirtv1.VirtualMachine{},
			vmi: &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{
				Phase: kubevirtv1.Failed, Reason: "PodTerminating",
			}},
			want:       computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_FAILED,
			wantDetail: "PodTerminating",
		},
		{name: "unknown", vm: &kubevirtv1.VirtualMachine{}, vmi: &kubevirtv1.VirtualMachineInstance{}, want: computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, detail := runningState(tt.vm, tt.vmi)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantDetail, detail)
		})
	}
}

func TestCheckSupported(t *testing.T) {
	unsupported := map[string]func(r *computev1.CreateComputeRequest){
		"placement groups": func(r *computev1.CreateComputeRequest) { r.Compute.PlacementGroupIds = []string{"g"} },
		"start stopped":    func(r *computev1.CreateComputeRequest) { r.Start = proto.Bool(false) },
		"e1000 model": func(r *computev1.CreateComputeRequest) {
			r.NetworkInterfaces[0].Model = computev1.NicModel_NIC_MODEL_E1000
		},
		"bandwidth": func(r *computev1.CreateComputeRequest) { r.NetworkInterfaces[0].Bandwidth = 100 },
		"no addresses on a bridged interface": func(r *computev1.CreateComputeRequest) {
			r.NetworkInterfaces[0].IpAssignment = computev1.IpAssignment_IP_ASSIGNMENT_NONE
		},
	}
	for name, set := range unsupported {
		t.Run(name, func(t *testing.T) {
			req := createRequest(bridged("data0"))
			set(req)
			assert.ErrorIs(t, checkSupported(req), apperrors.ErrUnsupported)
		})
	}

	var invalid *apperrors.ErrInvalidArgument
	assert.ErrorAs(t, checkSupported(createRequest(bridged(KubevirtVmMgmtNetworkName))), &invalid, "the management interface name is reserved")
	assert.NoError(t, checkSupported(createRequest(bridged("data0"), sriov("data1"))))
}

func TestNameInterfaces(t *testing.T) {
	ifaces := []*computev1.NetworkInterfaceData{bridged(""), bridged("nic0"), bridged("")}
	nameInterfaces(ifaces)
	assert.Equal(t, []string{"nic1", "nic0", "nic2"}, []string{ifaces[0].Name, ifaces[1].Name, ifaces[2].Name})
}

func TestFormatVmName(t *testing.T) {
	long := "ns-with-a-very-long-name-member-vnf-index-and-an-even-longer-vdu-name-0"
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "valid name unchanged", in: "myvm", want: "myvm"},
		{name: "uppercase lowered", in: "ret37-min-1-min-vnf-min-VM-0", want: "ret37-min-1-min-vnf-min-vm-0"},
		{name: "invalid runs become one dash", in: "My__VM.1", want: "my-vm-1"},
		{name: "edge dashes trimmed", in: "-_vm_-", want: "vm"},
		{name: "nothing valid left", in: "__", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatVmName(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("long name is truncated to a unique 63-char label", func(t *testing.T) {
		got, err := formatVmName(long)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(got), 63)
		other, err := formatVmName(long + "1")
		require.NoError(t, err)
		assert.NotEqual(t, got, other)
		again, err := formatVmName(long)
		require.NoError(t, err)
		assert.Equal(t, got, again, "must be deterministic")
	})
}
