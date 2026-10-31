package kubevirt

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"strings"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

// checkSupported rejects request fields kube-vim does not apply yet.
func checkSupported(req *computev1.CreateComputeRequest) error {
	unsupported := ""
	switch {
	case len(req.GetCompute().GetPlacementGroupIds()) > 0:
		unsupported = "compute.placementGroupIds"
	case req.Start != nil && !req.GetStart():
		unsupported = "start"
	}
	for _, iface := range req.GetNetworkInterfaces() {
		if unsupported != "" {
			break
		}
		switch {
		case iface.GetName() == KubevirtVmMgmtNetworkName:
			return &apperrors.ErrInvalidArgument{Field: "networkInterfaces.name", Reason: fmt.Sprintf("'%s' is the management interface", KubevirtVmMgmtNetworkName)}
		case iface.GetModel() != computev1.NicModel_NIC_MODEL_UNSPECIFIED && iface.GetModel() != computev1.NicModel_NIC_MODEL_VIRTIO:
			unsupported = "networkInterfaces.model"
		case iface.GetBandwidth() != 0:
			unsupported = "networkInterfaces.bandwidth"
		case iface.GetIpAssignment() == computev1.IpAssignment_IP_ASSIGNMENT_NONE && !isSriov(iface):
			unsupported = "networkInterfaces.ipAssignment IP_ASSIGNMENT_NONE on a BRIDGE interface"
		}
	}
	if unsupported != "" {
		return fmt.Errorf("%s: %w", unsupported, apperrors.ErrUnsupported)
	}
	return nil
}

// storedRequest returns the parts of req kept in the VM annotation: no user data, no outputs.
func storedRequest(req *computev1.CreateComputeRequest) *computev1.CreateComputeRequest {
	c := req.GetCompute()
	ifaces := make([]*computev1.NetworkInterfaceData, len(req.GetNetworkInterfaces()))
	for i, iface := range req.GetNetworkInterfaces() {
		ifaces[i] = proto.CloneOf(iface)
	}
	return &computev1.CreateComputeRequest{
		Compute: &computev1.Compute{
			ComputeName: c.GetComputeName(),
			FlavourId:   c.GetFlavourId(),
			ImageId:     c.GetImageId(),
			Metadata:    c.GetMetadata(),
		},
		NetworkInterfaces: ifaces,
	}
}

// launcherInfo is what the virt-launcher pod adds to a compute.
type launcherInfo struct {
	podName       string
	hostPciByVnic map[string]string // vNIC name -> host PCI address (SR-IOV / pass-through)
}

// computeFromVm returns the compute vm and vmi run, from the request stored on vm.
func computeFromVm(vm *kubevirtv1.VirtualMachine, vmi *kubevirtv1.VirtualMachineInstance, launcher launcherInfo) (*computev1.Compute, error) {
	raw, ok := vm.Annotations[compute.K8sComputeAnnotation]
	if !ok {
		return nil, fmt.Errorf("VirtualMachine '%s' has no '%s' annotation: %w", vm.Name, compute.K8sComputeAnnotation, apperrors.ErrInternal)
	}
	stored := &computev1.CreateComputeRequest{}
	if err := protojson.Unmarshal([]byte(raw), stored); err != nil {
		return nil, fmt.Errorf("unmarshal compute of VirtualMachine '%s': %w", vm.Name, err)
	}
	c := stored.GetCompute()
	c.ComputeId = vm.Labels[compute.K8sComputeIdLabel]
	c.HostId = vmi.Status.NodeName
	c.OperationalState = commonv1.OperationalState_OPERATIONAL_STATE_ENABLED
	if vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted {
		c.OperationalState = commonv1.OperationalState_OPERATIONAL_STATE_DISABLED
	}
	c.RunningState, c.StatusDetail = runningState(vm, vmi)
	if cpu := vmi.Spec.Domain.CPU; cpu != nil {
		c.VirtualCpu = &computev1.VirtualCpu{NumVirtualCpu: atLeastOne(cpu.Sockets) * atLeastOne(cpu.Cores) * atLeastOne(cpu.Threads)}
	}
	if mem := vmi.Spec.Domain.Memory; mem != nil && mem.Guest != nil {
		c.VirtualMemory = &computev1.VirtualMemory{VirtualMemSize: mem.Guest.String()}
	}
	if c.Metadata == nil {
		c.Metadata = map[string]string{}
	}
	if launcher.podName != "" {
		c.Metadata[compute.PodNameMetadataKey] = launcher.podName
	}

	mgmt := &computev1.VirtualNetworkInterface{
		Name:           KubevirtVmMgmtNetworkName,
		TypeVirtualNic: computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_MASQUERADE,
		IsManagement:   true,
	}
	c.NetworkInterfaces = []*computev1.VirtualNetworkInterface{mgmt}
	for _, req := range stored.GetNetworkInterfaces() {
		iface := &computev1.VirtualNetworkInterface{
			Name:           req.GetName(),
			NetworkId:      req.GetNetworkId(),
			TypeVirtualNic: computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_BRIDGE,
			Model:          computev1.NicModel_NIC_MODEL_VIRTIO,
			HostPciAddress: launcher.hostPciByVnic[req.GetName()],
		}
		if isSriov(req) {
			iface.TypeVirtualNic = computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_SRIOV
			iface.Model = computev1.NicModel_NIC_MODEL_UNSPECIFIED
		}
		c.NetworkInterfaces = append(c.NetworkInterfaces, iface)
	}
	for _, iface := range c.NetworkInterfaces {
		iface.ComputeId = c.ComputeId
		iface.OperationalState = commonv1.OperationalState_OPERATIONAL_STATE_DISABLED
		for _, st := range vmi.Status.Interfaces {
			if st.Name == iface.Name {
				iface.IpAddresses = st.IPs
				iface.MacAddress = st.MAC
				iface.GuestInterfaceName = st.InterfaceName
				iface.OperationalState = commonv1.OperationalState_OPERATIONAL_STATE_ENABLED
			}
		}
	}
	return c, nil
}

func atLeastOne(v uint32) uint32 {
	return max(v, 1)
}

// runningState maps the KubeVirt state of vm and vmi to a running state, with a reason when it failed.
func runningState(vm *kubevirtv1.VirtualMachine, vmi *kubevirtv1.VirtualMachineInstance) (computev1.ComputeRunningState, string) {
	phase := vmi.Status.Phase
	switch {
	case vm.DeletionTimestamp != nil || vm.Status.PrintableStatus == kubevirtv1.VirtualMachineStatusTerminating:
		return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_TERMINATING, ""
	case vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted && phase == kubevirtv1.Running:
		return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STOPPING, ""
	case vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted:
		return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STOPPED, ""
	case phase == kubevirtv1.Failed:
		return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_FAILED, cmp.Or(vmi.Status.Reason, string(vm.Status.PrintableStatus))
	}
	for _, cond := range vmi.Status.Conditions {
		if cond.Type == kubevirtv1.VirtualMachineInstancePaused {
			return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_PAUSED, ""
		}
	}
	switch {
	case vm.Status.Ready && phase == kubevirtv1.Running:
		return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STARTED, ""
	case phase == kubevirtv1.Pending || phase == kubevirtv1.Scheduling || phase == kubevirtv1.Scheduled || phase == kubevirtv1.Running:
		return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_STARTING, ""
	}
	return computev1.ComputeRunningState_COMPUTE_RUNNING_STATE_UNSPECIFIED, ""
}

// maxVmNameLen is the DNS-1123 label limit; the VM name is also used as a label value.
const maxVmNameLen = 63

// formatVmName turns a caller-supplied compute name into a valid DNS-1123 label: lowercase,
// runs of other characters collapsed to '-', edges trimmed. Names over the limit are
// truncated and suffixed with a hash of the original so they stay unique.
func formatVmName(name string) (string, error) {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "", &apperrors.ErrInvalidArgument{Field: "compute name", Reason: fmt.Sprintf("'%s' has no characters valid in a Kubernetes name", name)}
	}
	if len(res) > maxVmNameLen {
		h := fnv.New32a()
		h.Write([]byte(name))
		suffix := fmt.Sprintf("-%08x", h.Sum32())
		res = strings.TrimRight(res[:maxVmNameLen-len(suffix)], "-") + suffix
	}
	return res, nil
}
