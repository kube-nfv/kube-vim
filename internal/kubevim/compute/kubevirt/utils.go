package kubevirt

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

func nfvVirtualComputeFromKubevirtVm(ctx context.Context, netMgr network.Manager, vm *kubevirtv1.VirtualMachine, vmi *kubevirtv1.VirtualMachineInstance) (*nfv.VirtualCompute, error) {
	if vmi == nil || vm == nil {
		return nil, fmt.Errorf("virtualMachine or virtualMachineInstance can't be nil")
	}
	computeId := misc.UIDToIdentifier(vm.UID)
	flavId, err := getFlavourFromInstanceSpec(vm)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavor from the instantiated kubevirt vm: %w", err)
	}
	imgId, err := getImageIdFromInstnceSpec(vm)
	if err != nil {
		return nil, fmt.Errorf("failed to get image id from the instantiated kubevirt vm: %w", err)
	}
	operState := nfv.OperationalState_ENABLED
	if vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted {
		operState = nfv.OperationalState_DISABLED
	}

	runningState := getRunningState(vm, vmi)
	mdFields := make(map[string]string)
	mdFields[KubevirtVmStatusCreated] = strconv.FormatBool(vm.Status.Created)
	mdFields[KubevirtVmStatusReady] = strconv.FormatBool(vm.Status.Ready)
	mdFields[KubevirtVmPrintableStatus] = string(vm.Status.PrintableStatus)
	mdFields[KubevirtVmRunStategy] = string(vm.Status.RunStrategy)
	mdFields[KubevirtVmiStatusPhase] = string(vmi.Status.Phase)
	if vmi.Status.Reason != "" {
		mdFields[KubevirtVmiStatusReason] = vmi.Status.Reason
	}

	netIfaces := make([]*nfv.VirtualNetworkInterface, 0, len(vmi.Status.Interfaces))
	for _, netSpec := range vmi.Spec.Networks {
		name := netSpec.Name
		netIfRes := &nfv.VirtualNetworkInterface{
			ResourceId: &nfv.Identifier{
				Value: name,
			},
			OperationalState: nfv.OperationalState_ENABLED,
			OwnerId:          computeId,
		}
		netMdFields := make(map[string]string)
		ifaceSpec, err := getInterfaceFromVmi(name, vmi)
		if err != nil {
			return nil, fmt.Errorf("failed to get interface from \"%s\" vmi: %w", vm.Name, err)
		}
		vNicType, err := ifaceBindingMethodToNfv(ifaceSpec.InterfaceBindingMethod)
		if err != nil {
			return nil, fmt.Errorf("failed to get virtual nic type for vmi \"%s\" interface \"%s\": %w", vm.Name, name, err)
		}
		netIfRes.TypeVirtualNic = vNicType
		if netSpec.NetworkSource.Pod != nil {
			netMdFields[KubevirtVmNetworkManagement] = "true"
		} else if netSpec.NetworkSource.Multus != nil {
			multusNet := netSpec.NetworkSource.Multus
			if multusNet.Default {
				netMdFields[KubevirtVmNetworkManagement] = "true"
			} else {
				netMdFields[KubevirtVmNetworkManagement] = "false"
			}
			// TODO: Add logic to split the NetworkAttachmentDefinition from namespace (if it exists).
			subnet, err := netMgr.GetSubnet(ctx, network.GetSubnetByNetAttachName(multusNet.NetworkName))
			if err != nil {
				return nil, fmt.Errorf("failed to get subnet from vm network \"%s\" network attachment defintion with name \"%s\": %w", name, multusNet.NetworkName, err)
			}
			netIfRes.SubnetId = subnet.ResourceId
			netIfRes.NetworkId = subnet.NetworkId
			netIfRes.Bandwidth = 0
		} else {
			return nil, fmt.Errorf("network \"%s\" should be either multus or pod", name)
		}
		ifaceStatus, err := getInterfaceStatusFromVmi(name, vmi)
		if err == nil && ifaceStatus != nil {
			ips := make([]*nfv.IPAddress, 0, len(ifaceStatus.IPs))
			for _, ip := range ifaceStatus.IPs {
				ips = append(ips, &nfv.IPAddress{
					Ip: ip,
				})
			}
			netIfRes.IpAddress = ips
			netIfRes.MacAddress = &nfv.MacAddress{
				Mac: ifaceStatus.MAC,
			}
			netMdFields[KubevirtInterfaceReady] = "true"
		} else {
			netMdFields[KubevirtInterfaceReady] = "false"
		}

		netIfRes.Metadata = &nfv.Metadata{
			Fields: netMdFields,
		}
		netIfaces = append(netIfaces, netIfRes)
	}

	return &nfv.VirtualCompute{
		ComputeId:               computeId,
		ComputeName:             &vm.Name,
		FlavourId:               flavId,
		VcImageId:               imgId,
		VirtualNetworkInterface: netIfaces,
		HostId: &nfv.Identifier{
			Value: vmi.Status.NodeName,
		},
		OperationalState: operState,
		RunningState:     runningState,
		VirtualCpu: &nfv.VirtualCpu{},
		VirtualMemory: &nfv.VirtualMemory{},
		Metadata: &nfv.Metadata{
			Fields: mdFields,
		},
	}, nil
}

// GetRunningState determines the high-level operational state of a VM
// the description string also can be set for some states
func getRunningState(vm *kubevirtv1.VirtualMachine, vmi *kubevirtv1.VirtualMachineInstance) nfv.ComputeRunningState {
	// If VM is administratively stopped
	if vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted {
		return nfv.ComputeRunningState_STOPPED
	}
	// If VM is stopped by the user
	for _, cond := range vmi.Status.Conditions {
		if cond.Type == kubevirtv1.VirtualMachineInstancePaused {
			return nfv.ComputeRunningState_PAUSED
		}
	}
	// If VM is in Terminating phase
	if vm.Status.PrintableStatus == kubevirtv1.VirtualMachineStatusTerminating {
		return nfv.ComputeRunningState_TERMINATING
	}
	if vm.Status.Created && vm.Status.Ready && vmi.Status.Phase == kubevirtv1.Running {
		return nfv.ComputeRunningState_RUNNING
	}
	if vmi.Status.Phase == kubevirtv1.Pending || vmi.Status.Phase == kubevirtv1.Scheduling || vmi.Status.Phase == kubevirtv1.Scheduled {
		return nfv.ComputeRunningState_STARTING
	}
	if vmi.Status.Phase == kubevirtv1.Failed {
		return nfv.ComputeRunningState_FAILED
	}
	// TODO: Suspeneded
	return nfv.ComputeRunningState_UNKNOWN
}

func getFlavourFromInstanceSpec(vmSpec *kubevirtv1.VirtualMachine) (*nfv.Identifier, error) {
	flavId, ok := vmSpec.Labels[flavour.K8sFlavourIdLabel]
	if !ok {
		return nil, fmt.Errorf("kubevirt virtualMachine spec missing kube-nfv flavour id label")
	}
	return &nfv.Identifier{
		Value: flavId,
	}, nil
}

func getImageIdFromInstnceSpec(vmSpec *kubevirtv1.VirtualMachine) (*nfv.Identifier, error) {
	imgId, ok := vmSpec.Labels[image.K8sImageIdLabel]
	if !ok {
		return nil, fmt.Errorf("kubevirt virtualMachine spec missing kube-nfv image id label")
	}
	return &nfv.Identifier{
		Value: imgId,
	}, nil

}

// Returns the kubevirt network with specified name from the kubevirt vmi spec or nil if not found.
func getNetworkFromVmiSpec(netName string, vmiSpec *kubevirtv1.VirtualMachineInstanceSpec) (*kubevirtv1.Network, error) {
	if vmiSpec == nil {
		return nil, fmt.Errorf("vmi spec is empty")
	}
	for _, net := range vmiSpec.Networks {
		if net.Name == netName {
			return &net, nil
		}
	}
	return nil, fmt.Errorf("network \"%s\" not found in vmi spec: %w", netName, common.NotFoundErr)
}

// Returns the kubevirt network with specified name from the kubevirt vm or nil if not found.
func getNetworkFromVm(netName string, vmSpec *kubevirtv1.VirtualMachine) (*kubevirtv1.Network, error) {
	if vmSpec == nil {
		return nil, fmt.Errorf("vm is empty")
	}
	if vmSpec.Spec.Template == nil {
		return nil, fmt.Errorf("vm template is empty")
	}
	if net, err := getNetworkFromVmiSpec(netName, &vmSpec.Spec.Template.Spec); err != nil {
		return nil, fmt.Errorf("failed to get network from vm \"%s\" vmi template: %w", vmSpec.Name, err)
	} else {
		return net, nil
	}
}


// Returns the kubevirt interface with specified network name from the kubevirt domain spec or nil if not found.
func getInterfaceFromDomainSpec(ifaceName string, domSpec *kubevirtv1.DomainSpec) (*kubevirtv1.Interface, error) {
	if domSpec == nil {
		return nil, fmt.Errorf("domain spec is empty")
	}
	for _, iface := range domSpec.Devices.Interfaces {
		if iface.Name == ifaceName {
			return &iface, nil
		}
	}
	return nil, fmt.Errorf("interface with name \"%s\" not found in domain spec: %w", ifaceName, common.NotFoundErr)
}

func getInterfaceFromVm(ifaceName string, vmSpec *kubevirtv1.VirtualMachine) (*kubevirtv1.Interface, error) {
	if vmSpec == nil {
		return nil, fmt.Errorf("vm is empty")
	}
	if vmSpec.Spec.Template == nil {
		return nil, fmt.Errorf("vm template is empty")
	}
	if iface, err := getInterfaceFromDomainSpec(ifaceName, &vmSpec.Spec.Template.Spec.Domain); err != nil {
		return nil, fmt.Errorf("failed to get iface from vm \"%s\" vmi template domain spec: %w", vmSpec.Name, err)
	} else {
		return iface, nil
	}
}

func getInterfaceFromVmi(ifaceName string, vmi *kubevirtv1.VirtualMachineInstance) (*kubevirtv1.Interface, error) {
	if vmi == nil {
		return nil, fmt.Errorf("vmi is empty")
	}
	if iface, err := getInterfaceFromDomainSpec(ifaceName, &vmi.Spec.Domain); err != nil {
		return nil, fmt.Errorf("failed to get iface \"%s\" from vmi spec: %w", ifaceName, err)
	} else {
		return iface, nil
	}
}

func getInterfaceStatusFromVmi(ifaceName string, vmi *kubevirtv1.VirtualMachineInstance) (*kubevirtv1.VirtualMachineInstanceNetworkInterface, error) {
	if vmi == nil {
		return nil, fmt.Errorf("vmi is empty")
	}
	for _, iface := range vmi.Status.Interfaces {
		if iface.Name == ifaceName {
			return &iface, nil
		}
	}
	return nil, fmt.Errorf("iface \"%s\" not found in vmi status: %w", ifaceName, common.NotFoundErr)
}

func ifaceBindingMethodToNfv(method kubevirtv1.InterfaceBindingMethod) (nfv.TypeVirtualNic, error) {
	switch {
	case method.Bridge != nil:
		return nfv.TypeVirtualNic_BRIDGE, nil
	case method.Masquerade != nil:
		return nfv.TypeVirtualNic_BRIDGE, nil
	case method.SRIOV != nil:
		return nfv.TypeVirtualNic_SRIOV, nil
	default:
		return nfv.TypeVirtualNic_BRIDGE, fmt.Errorf("unknown interface binding method")
	}
}
