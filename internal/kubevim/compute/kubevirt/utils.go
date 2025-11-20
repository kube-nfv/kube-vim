package kubevirt

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

func nfvVirtualComputeFromKubevirtVm(ctx context.Context, netMgr network.Manager, vm *kubevirtv1.VirtualMachine, vmi *kubevirtv1.VirtualMachineInstance) (*vivnfm.VirtualCompute, error) {
	if vmi == nil || vm == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "kubevirt resources", Reason: "virtualMachine and virtualMachineInstance cannot be nil"}
	}
	computeId := misc.UIDToIdentifier(vm.UID)
	flavId, err := getFlavourFromInstanceSpec(vm)
	if err != nil {
		return nil, fmt.Errorf("get flavour from kubevirt VM '%s' (uid: %s): %w", vm.Name, vm.UID, err)
	}
	imgId, err := getImageIdFromInstnceSpec(vm)
	if err != nil {
		return nil, fmt.Errorf("get image id from kubevirt VM '%s' (uid: %s): %w", vm.Name, vm.UID, err)
	}
	operState := nfvcommon.OperationalState_ENABLED
	if vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted {
		operState = nfvcommon.OperationalState_DISABLED
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

	netIfaces := make([]*vivnfm.VirtualNetworkInterface, 0, len(vmi.Status.Interfaces))
	for _, netSpec := range vmi.Spec.Networks {
		name := netSpec.Name
		netIfRes := &vivnfm.VirtualNetworkInterface{
			ResourceId: &nfvcommon.Identifier{
				Value: name,
			},
			OperationalState: nfvcommon.OperationalState_ENABLED,
			OwnerId:          computeId,
		}
		netMdFields := make(map[string]string)
		ifaceSpec, err := getInterfaceFromVmi(name, vmi)
		if err != nil {
			return nil, fmt.Errorf("get interface from VMI '%s' (uid: %s): %w", vm.Name, vm.UID, err)
		}
		vNicType, err := ifaceBindingMethodToNfv(ifaceSpec.InterfaceBindingMethod)
		if err != nil {
			return nil, fmt.Errorf("get virtual NIC type for VMI '%s' (uid: %s) interface '%s': %w", vm.Name, vm.UID, name, err)
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
				// Check if the error is due to missing NetworkAttachmentDefinition (race condition during deletion)
				var notFoundErr *apperrors.ErrNotFound
				isK8sNotFound := k8s_errors.IsNotFound(err)
				isKubeNfvNotFound := errors.As(err, &notFoundErr)

				if isK8sNotFound || isKubeNfvNotFound {
					// During deletion, NetworkAttachmentDefinition might be already deleted
					// Set placeholder values to allow VM deletion to proceed
					netIfRes.SubnetId = &nfvcommon.Identifier{Value: "deleted"}
					netIfRes.NetworkId = &nfvcommon.Identifier{Value: "deleted"}
					netIfRes.Bandwidth = 0
				} else {
					return nil, fmt.Errorf("get subnet from VM network '%s' network attachment definition '%s': %w", name, multusNet.NetworkName, err)
				}
			} else {
				netIfRes.SubnetId = subnet.ResourceId
				netIfRes.NetworkId = subnet.NetworkId
				netIfRes.Bandwidth = 0
			}
		} else {
			return nil, &apperrors.ErrInvalidArgument{Field: fmt.Sprintf("network '%s'", name), Reason: "must be either multus or pod type"}
		}
		ifaceStatus, err := getInterfaceStatusFromVmi(name, vmi)
		if err == nil && ifaceStatus != nil {
			ips := make([]*nfvcommon.IPAddress, 0, len(ifaceStatus.IPs))
			for _, ip := range ifaceStatus.IPs {
				ips = append(ips, &nfvcommon.IPAddress{
					Ip: ip,
				})
			}
			netIfRes.IpAddress = ips
			netIfRes.MacAddress = &nfvcommon.MacAddress{
				Mac: ifaceStatus.MAC,
			}
			netMdFields[KubevirtInterfaceReady] = "true"
		} else {
			netIfRes.MacAddress = &nfvcommon.MacAddress{
				Mac: "initializing",
			}
			netMdFields[KubevirtInterfaceReady] = "false"
		}

		netIfRes.Metadata = &nfvcommon.Metadata{
			Fields: netMdFields,
		}
		netIfaces = append(netIfaces, netIfRes)
	}

	return &vivnfm.VirtualCompute{
		ComputeId:               computeId,
		ComputeName:             &vm.Name,
		FlavourId:               flavId,
		VcImageId:               imgId,
		VirtualNetworkInterface: netIfaces,
		HostId: &nfvcommon.Identifier{
			Value: vmi.Status.NodeName,
		},
		OperationalState: operState,
		RunningState:     runningState,
		VirtualCpu:       &vivnfm.VirtualCpu{},
		VirtualMemory:    &vivnfm.VirtualMemory{},
		Metadata: &nfvcommon.Metadata{
			Fields: mdFields,
		},
	}, nil
}

// GetRunningState determines the high-level operational state of a VM
// the description string also can be set for some states
func getRunningState(vm *kubevirtv1.VirtualMachine, vmi *kubevirtv1.VirtualMachineInstance) nfvcommon.ComputeRunningState {
	// If VM is administratively stopped
	if vm.Status.RunStrategy == kubevirtv1.RunStrategyHalted {
		return nfvcommon.ComputeRunningState_STOPPED
	}
	// If VM is stopped by the user
	for _, cond := range vmi.Status.Conditions {
		if cond.Type == kubevirtv1.VirtualMachineInstancePaused {
			return nfvcommon.ComputeRunningState_PAUSED
		}
	}
	// If VM is in Terminating phase
	if vm.Status.PrintableStatus == kubevirtv1.VirtualMachineStatusTerminating {
		return nfvcommon.ComputeRunningState_TERMINATING
	}
	if vm.Status.Created && vm.Status.Ready && vmi.Status.Phase == kubevirtv1.Running {
		return nfvcommon.ComputeRunningState_RUNNING
	}
	if vmi.Status.Phase == kubevirtv1.Pending || vmi.Status.Phase == kubevirtv1.Scheduling || vmi.Status.Phase == kubevirtv1.Scheduled {
		return nfvcommon.ComputeRunningState_STARTING
	}
	if vmi.Status.Phase == kubevirtv1.Failed {
		return nfvcommon.ComputeRunningState_FAILED
	}
	// TODO: Suspeneded
	return nfvcommon.ComputeRunningState_UNKNOWN
}

func getFlavourFromInstanceSpec(vmSpec *kubevirtv1.VirtualMachine) (*nfvcommon.Identifier, error) {
	flavId, ok := vmSpec.Labels[flavour.K8sFlavourIdLabel]
	if !ok {
		return nil, &apperrors.ErrInvalidArgument{Field: "kubevirt VirtualMachine spec", Reason: "missing kube-nfv flavour id label"}
	}
	return &nfvcommon.Identifier{
		Value: flavId,
	}, nil
}

func getImageIdFromInstnceSpec(vmSpec *kubevirtv1.VirtualMachine) (*nfvcommon.Identifier, error) {
	imgId, ok := vmSpec.Labels[image.K8sImageIdLabel]
	if !ok {
		return nil, &apperrors.ErrInvalidArgument{Field: "kubevirt VirtualMachine spec", Reason: "missing kube-nfv image id label"}
	}
	return &nfvcommon.Identifier{
		Value: imgId,
	}, nil

}

// Returns the kubevirt network with specified name from the kubevirt vmi spec or nil if not found.
func getNetworkFromVmiSpec(netName string, vmiSpec *kubevirtv1.VirtualMachineInstanceSpec) (*kubevirtv1.Network, error) {
	if vmiSpec == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VMI spec", Reason: "cannot be empty"}
	}
	for _, net := range vmiSpec.Networks {
		if net.Name == netName {
			return &net, nil
		}
	}
	return nil, &apperrors.ErrNotFound{Entity: "network", Identifier: netName}
}

// Returns the kubevirt network with specified name from the kubevirt vm or nil if not found.
func getNetworkFromVm(netName string, vmSpec *kubevirtv1.VirtualMachine) (*kubevirtv1.Network, error) {
	if vmSpec == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VM", Reason: "cannot be empty"}
	}
	if vmSpec.Spec.Template == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VM template", Reason: "cannot be empty"}
	}
	if net, err := getNetworkFromVmiSpec(netName, &vmSpec.Spec.Template.Spec); err != nil {
		return nil, fmt.Errorf("get network from VM '%s' VMI template: %w", vmSpec.Name, err)
	} else {
		return net, nil
	}
}

// Returns the kubevirt interface with specified network name from the kubevirt domain spec or nil if not found.
func getInterfaceFromDomainSpec(ifaceName string, domSpec *kubevirtv1.DomainSpec) (*kubevirtv1.Interface, error) {
	if domSpec == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "domain spec", Reason: "cannot be empty"}
	}
	for _, iface := range domSpec.Devices.Interfaces {
		if iface.Name == ifaceName {
			return &iface, nil
		}
	}
	return nil, &apperrors.ErrNotFound{Entity: "interface", Identifier: ifaceName}
}

func getInterfaceFromVm(ifaceName string, vmSpec *kubevirtv1.VirtualMachine) (*kubevirtv1.Interface, error) {
	if vmSpec == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VM", Reason: "cannot be empty"}
	}
	if vmSpec.Spec.Template == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VM template", Reason: "cannot be empty"}
	}
	if iface, err := getInterfaceFromDomainSpec(ifaceName, &vmSpec.Spec.Template.Spec.Domain); err != nil {
		return nil, fmt.Errorf("get interface from VM '%s' VMI template domain spec: %w", vmSpec.Name, err)
	} else {
		return iface, nil
	}
}

func getInterfaceFromVmi(ifaceName string, vmi *kubevirtv1.VirtualMachineInstance) (*kubevirtv1.Interface, error) {
	if vmi == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VMI", Reason: "cannot be empty"}
	}
	if iface, err := getInterfaceFromDomainSpec(ifaceName, &vmi.Spec.Domain); err != nil {
		return nil, fmt.Errorf("get interface '%s' from VMI spec: %w", ifaceName, err)
	} else {
		return iface, nil
	}
}

func getInterfaceStatusFromVmi(ifaceName string, vmi *kubevirtv1.VirtualMachineInstance) (*kubevirtv1.VirtualMachineInstanceNetworkInterface, error) {
	if vmi == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VMI", Reason: "cannot be empty"}
	}
	for _, iface := range vmi.Status.Interfaces {
		if iface.Name == ifaceName {
			return &iface, nil
		}
	}
	return nil, &apperrors.ErrNotFound{Entity: "interface", Identifier: ifaceName}
}

func ifaceBindingMethodToNfv(method kubevirtv1.InterfaceBindingMethod) (nfvcommon.TypeVirtualNic, error) {
	switch {
	case method.Bridge != nil:
		return nfvcommon.TypeVirtualNic_BRIDGE, nil
	case method.Masquerade != nil:
		return nfvcommon.TypeVirtualNic_BRIDGE, nil
	case method.SRIOV != nil:
		return nfvcommon.TypeVirtualNic_SRIOV, nil
	default:
		return nfvcommon.TypeVirtualNic_BRIDGE, fmt.Errorf("unknown interface binding method: %w", apperrors.ErrUnsupported)
	}
}
