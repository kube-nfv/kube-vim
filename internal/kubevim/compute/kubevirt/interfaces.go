package kubevirt

import (
	"context"
	"fmt"
	"net"
	"strings"

	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

// nameInterfaces gives every interface without a name the first free "nic<N>".
func nameInterfaces(ifaces []*computev1.NetworkInterfaceData) {
	used := map[string]bool{KubevirtVmMgmtNetworkName: true}
	for _, iface := range ifaces {
		used[iface.GetName()] = true
	}
	next := 0
	for _, iface := range ifaces {
		if iface.GetName() != "" {
			continue
		}
		for used[fmt.Sprintf("nic%d", next)] {
			next++
		}
		iface.Name = fmt.Sprintf("nic%d", next)
		used[iface.Name] = true
	}
}

func isSriov(iface *computev1.NetworkInterfaceData) bool {
	return iface.GetTypeVirtualNic() == computev1.TypeVirtualNic_TYPE_VIRTUAL_NIC_SRIOV
}

// buildInterfaces returns the VM's networks and interfaces, the pod (management) network first,
// and the Kube-OVN annotations of its bridged interfaces.
func (m *manager) buildInterfaces(ctx context.Context, ifaces []*computev1.NetworkInterfaceData) ([]kubevirtv1.Network, []kubevirtv1.Interface, map[string]string, error) {
	networks := []kubevirtv1.Network{{
		Name:          KubevirtVmMgmtNetworkName,
		NetworkSource: kubevirtv1.NetworkSource{Pod: &kubevirtv1.PodNetwork{}},
	}}
	interfaces := []kubevirtv1.Interface{{
		Name:                   KubevirtVmMgmtNetworkName,
		InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{Masquerade: &kubevirtv1.InterfaceMasquerade{}},
	}}
	annotations := map[string]string{}
	for _, iface := range ifaces {
		nicType := network.NicTypeBridge
		if isSriov(iface) {
			nicType = network.NicTypeSriov
		}
		att, err := m.networkManager.Attachment(ctx, iface.GetNetworkId(), nicType)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("interface '%s': %w", iface.GetName(), err)
		}
		networks = append(networks, kubevirtv1.Network{
			Name:          iface.GetName(),
			NetworkSource: kubevirtv1.NetworkSource{Multus: &kubevirtv1.MultusNetwork{NetworkName: att.NetAttachName}},
		})
		vmIface := kubevirtv1.Interface{Name: iface.GetName()}
		if nicType == network.NicTypeSriov {
			vmIface.SRIOV = &kubevirtv1.InterfaceSRIOV{}
			// KubeVirt forwards the MAC to the CNI through the network selection request.
			vmIface.MacAddress = iface.GetMacAddress()
		} else {
			vmIface.Bridge = &kubevirtv1.InterfaceBridge{}
			if err := m.bridgeAnnotations(iface, att, annotations); err != nil {
				return nil, nil, nil, fmt.Errorf("interface '%s': %w", iface.GetName(), err)
			}
		}
		interfaces = append(interfaces, vmIface)
	}
	return networks, interfaces, annotations, nil
}

// bridgeAnnotations adds the Kube-OVN annotations that bind a bridged interface to its logical switch,
// static addresses and MAC.
func (m *manager) bridgeAnnotations(iface *computev1.NetworkInterfaceData, att *network.Attachment, annotations map[string]string) error {
	prefix := fmt.Sprintf("%s.%s.ovn.kubernetes.io", att.NetAttachName, *m.cfg.Namespace)
	annotations[prefix+"/logical_switch"] = att.LogicalSwitch
	if iface.GetIpAssignment() == computev1.IpAssignment_IP_ASSIGNMENT_STATIC {
		if len(iface.GetIpAddresses()) != len(att.Cidrs) {
			return fmt.Errorf("static addresses for only some IP versions of a dual-stack network: %w", apperrors.ErrUnsupported)
		}
		for _, ip := range iface.GetIpAddresses() {
			if !inAnyCidr(ip, att.Cidrs) {
				return &apperrors.ErrInvalidArgument{Field: "ipAddresses", Reason: fmt.Sprintf("'%s' is in none of the network's subnets %v", ip, att.Cidrs)}
			}
		}
		annotations[prefix+"/ip_address"] = strings.Join(iface.GetIpAddresses(), ",")
	}
	if iface.GetMacAddress() != "" {
		annotations[prefix+"/mac_address"] = iface.GetMacAddress()
	}
	return nil
}

func inAnyCidr(ip string, cidrs []string) bool {
	addr := net.ParseIP(ip)
	for _, c := range cidrs {
		if _, ipNet, err := net.ParseCIDR(c); err == nil && ipNet.Contains(addr) {
			return true
		}
	}
	return false
}
