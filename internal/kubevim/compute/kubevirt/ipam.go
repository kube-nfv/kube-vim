package kubevirt

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

// ipamResolver turns the ETSI VirtualNetworkInterfaceData/IPAM request into the
// kubevirt Networks + Interfaces (plus kube-ovn annotations) for a VM. It is
// split out of manager.go because the overlay/underlay/SR-IOV x static/dynamic
// resolution is the densest logic in the compute manager and is exercised in
// isolation. It holds the network manager and target namespace so the per-call
// plumbing does not thread them through every helper.
type ipamResolver struct {
	netManager network.Manager
	namespace  string
}

func newIpamResolver(netManager network.Manager, namespace string) *ipamResolver {
	return &ipamResolver{netManager: netManager, namespace: namespace}
}

// resolveInterfaces builds the VM's networks and interfaces. The pod (management)
// network is always prepended; each VirtualNetworkInterfaceData then contributes
// one more interface resolved from its network/subnet id and IPAM.
func (r *ipamResolver) resolveInterfaces(ctx context.Context, networksData []*vivnfm.VirtualNetworkInterfaceData, networkIpam []*vivnfm.VirtualNetworkInterfaceIPAM) ([]kubevirtv1.Network, []kubevirtv1.Interface, map[string]string, error) {
	networks := make([]kubevirtv1.Network, 0, len(networksData)+1 /*+ podNetwork*/)
	interfaces := make([]kubevirtv1.Interface, 0, len(networksData)+1 /*+ podNetwork*/)
	annotations := make(map[string]string)
	// Add pod network
	networks = append(networks, kubevirtv1.Network{
		Name: KubevirtVmMgmtNetworkName,
		NetworkSource: kubevirtv1.NetworkSource{
			Pod: &kubevirtv1.PodNetwork{},
		},
	})
	interfaces = append(interfaces, kubevirtv1.Interface{
		Name: KubevirtVmMgmtNetworkName,
		InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
			Masquerade: &kubevirtv1.InterfaceMasquerade{},
		},
	})
	// There are might be few different network types that should be handeled.
	// 1. Overlay network
	//    a. Have an subnetId (which is used to identify the IPAM). IPAM might be empty -> dynamic IPAM allocation (eg.DHCP)
	//    b. Only networkId. Try to find IPAM with networkId.
	//       - If found and IPAM has an static IP, try to find the subnet where port should belong to which include that static IP
	//       - If not found and NetworkInterfaceData has an compute.kubevim.kubenfv.io/network.subnet.assignment=random
	//           in metadata return the IPAM with a first subnet with dynamic IP.
	// 2. Underlay network
	//    a. Might have an subnetId. In that case IPAM identified using subnetId.
	//    b. Only networkId. Try to find IPAM with networkId.
	//       - If found and IPAM has an static IP, try to find the subnet where port should belong to which include that static IP
	//       - If not found return the IPAM with a first subnet with dynamic IP (most cases for the underlay network).
	for netIdx, netData := range networksData {
		hasNetworkId := netData.NetworkId != nil && netData.NetworkId.Value != ""
		hasSubnetId := netData.SubnetId != nil && netData.SubnetId.Value != ""
		if !hasNetworkId && !hasSubnetId {
			return nil, nil, nil, &apperrors.ErrInvalidArgument{
				Field:  fmt.Sprintf("VM interface index %d", netIdx),
				Reason: "either networkId or subnetId must be defined to identify the VirtualNetworkInterfaceData related network",
			}
		}
		var net *kubevirtv1.Network
		var iface *kubevirtv1.Interface
		ann := make(map[string]string)
		if hasSubnetId {
			subnetIdVal := netData.SubnetId.GetValue()
			subInst, err := r.netManager.GetSubnet(ctx, network.GetSubnetByUid(netData.SubnetId))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("get subnet '%s' referenced in VirtualNetworkInterfaceData: %w", subnetIdVal, err)
			}
			// Check if the subnet belongs to the same network if both are specified.
			if hasNetworkId {
				if subInst.NetworkId == nil {
					return nil, nil, nil, fmt.Errorf("subnet '%s' has no networkId but it is specified in the request as '%s'", subnetIdVal, netData.NetworkId.Value)
				}
				if subInst.NetworkId.Value != netData.NetworkId.Value {
					return nil, nil, nil, fmt.Errorf("subnet '%s' references network '%s' but different network '%s' is specified in the request", subnetIdVal, subInst.NetworkId.Value, netData.NetworkId.Value)
				}
			}
			ipam, err := r.subnetIpam(ctx, netData.SubnetId, networkIpam)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("get IPAM for subnet '%s': %w", subnetIdVal, err)
			}
			net, iface, ann, err = r.initNetwork(ctx, ipam)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("initialize kubevirt network and interface from IPAM for subnet '%s': %w", subnetIdVal, err)
			}
			// If VirtualNetworkInterfaceData has an subnetId, networkId will just ignored since subnetId contains enough info.
		} else if hasNetworkId /* no subnetId */ {
			netInst, err := r.netManager.GetNetwork(ctx, network.GetNetworkByUid(netData.NetworkId))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("get network '%s' referenced in VirtualNetworkInterfaceData: %w", netData.NetworkId.Value, err)
			}

			if netInst.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_SRIOV {
				net, iface, ann, err = initSriovNetwork(netInst, networkIpam)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("initialize SR-IOV interface for network '%s': %w", netData.NetworkId.Value, err)
				}
			} else {
				var ipam *vivnfm.VirtualNetworkInterfaceIPAM
				if netInst.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_UNDERLAY {
					ipam, err = r.networkIpam(ctx, netData.NetworkId, networkIpam, false)
				} else if netInst.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_OVERLAY {
					returnOnMiss := true
					if netData.Metadata != nil {
						ann, ok := netData.Metadata.Fields[compute.KubenfvVmNetworkSubnetAssignmentAnnotation]
						allocateRandom := ok && ann == "random"
						returnOnMiss = !allocateRandom
					}
					ipam, err = r.networkIpam(ctx, netData.NetworkId, networkIpam, returnOnMiss)
				}
				if err != nil {
					return nil, nil, nil, fmt.Errorf(
						"get IPAM for network '%s': %w",
						netData.NetworkId.Value,
						err,
					)
				}
				net, iface, ann, err = r.initNetwork(ctx, ipam)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("initialize kubevirt network and interface from IPAM for network '%s': %w", netData.NetworkId.Value, err)
				}
			}
		}
		networks = append(networks, *net)
		interfaces = append(interfaces, *iface)
		for k, v := range ann {
			annotations[k] = v
		}
	}
	return networks, interfaces, annotations, nil
}

// subnetIpam returns the IP/MAC allocation for a subnet.
// If IPAM not configured: returns random allocatable IP/MAC.
// If IPAM is configured with a static IP, checks the address belongs to the subnet.
func (r *ipamResolver) subnetIpam(ctx context.Context, subnetId *nfvcommon.Identifier, netIPAMs []*vivnfm.VirtualNetworkInterfaceIPAM) (*vivnfm.VirtualNetworkInterfaceIPAM, error) {
	var netIpam *vivnfm.VirtualNetworkInterfaceIPAM = nil
	for _, ipam := range netIPAMs {
		if ipam.SubnetId != nil && ipam.SubnetId.Value == subnetId.Value {
			netIpam = ipam
			break
		}
	}
	// If no IPAM set for subnetId return just the default IPAM with dynamic IP, MAC that is reference that subnetId
	if netIpam == nil {
		return &vivnfm.VirtualNetworkInterfaceIPAM{
			NetworkId:  nil, // Might be empty if since subnetId is going to used by the caller.
			SubnetId:   subnetId,
			IpAddress:  nil, // Dynamic Ip
			MacAddress: nil, // Dynamic MAC
		}, nil
	}
	if netIpam.IpAddress == nil {
		return netIpam, nil
	}
	// Check if the static IP Address belongs to the subnet referenced by the subnetId.
	sub, err := r.netManager.GetSubnet(ctx, network.GetSubnetByUid(subnetId))
	if err != nil {
		return nil, fmt.Errorf("get subnet '%s': %w", subnetId.Value, err)
	}
	if sub.Cidr == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "subnet CIDR", Reason: fmt.Sprintf("cannot be nil for subnet '%s'", sub.ResourceId.Value)}
	}
	if !network.IpBelongsToCidr(netIpam.IpAddress, sub.Cidr) {
		return nil, fmt.Errorf("IP address '%s' not in subnet CIDR '%s'", netIpam.IpAddress.Ip, sub.Cidr.Cidr)
	}
	return netIpam, nil
}

// networkIpam finds the IPAM for a network identified by networkId (no subnetId).
//  1. If IPAM exists for the network:
//     a. if subnetId exists in IPAM: resolve from the subnetId.
//     b. IPAM has a static IP: find the subnet in the VPC that holds the IP.
//     Returns ErrIPAMConfigurationMissing if "returnIfNoIpam" is set and neither applies.
//  2. If no IPAM for the network: return the first subnet in the VPC with dynamic IP/MAC.
func (r *ipamResolver) networkIpam(ctx context.Context, networkId *nfvcommon.Identifier, netIPAMs []*vivnfm.VirtualNetworkInterfaceIPAM, returnIfNoIpam bool) (*vivnfm.VirtualNetworkInterfaceIPAM, error) {
	var netIpam *vivnfm.VirtualNetworkInterfaceIPAM = nil
	for _, ipam := range netIPAMs {
		if ipam.NetworkId != nil && ipam.NetworkId.Value == networkId.Value {
			netIpam = ipam
			break
		}
	}
	if netIpam != nil && netIpam.SubnetId != nil {
		return r.subnetIpam(ctx, netIpam.SubnetId, netIPAMs)
	}
	if netIpam != nil && netIpam.IpAddress != nil {
		sub, err := r.netManager.GetSubnet(ctx, network.GetSubnetByNetworkIP(networkId, netIpam.IpAddress))
		if err != nil {
			return nil, fmt.Errorf("get subnet with networkId '%s' and IP '%s': %w", networkId.Value, netIpam.IpAddress.Ip, err)
		}
		netIpam.SubnetId.Value = sub.ResourceId.Value
		return r.subnetIpam(ctx, sub.ResourceId, netIPAMs)
	}
	if returnIfNoIpam {
		return nil, ErrIPAMConfigurationMissing
	}
	net, err := r.netManager.GetNetwork(ctx, network.GetNetworkByUid(networkId))
	if err != nil {
		return nil, fmt.Errorf("get network '%s': %w", networkId.Value, err)
	}
	if len(net.SubnetId) == 0 {
		return nil, &apperrors.ErrInvalidArgument{Field: "network subnets", Reason: fmt.Sprintf("network '%s' has no subnets", networkId.Value)}
	}
	fstSubId := net.SubnetId[0]

	if netIpam != nil {
		netIpam.SubnetId = fstSubId
	} else {
		netIPAMs = append(netIPAMs, &vivnfm.VirtualNetworkInterfaceIPAM{
			NetworkId:  networkId,
			SubnetId:   fstSubId,
			IpAddress:  nil, // dynamic ip
			MacAddress: nil, // dynamic mac
		})
	}
	return r.subnetIpam(ctx, fstSubId, netIPAMs)
}

// initSriovNetwork builds the multus network + SR-IOV interface for a network
// that maps directly to a NAD (no subnet). It is stateless (needs no network
// manager or namespace), so it stays a free function.
func initSriovNetwork(netInst *vivnfm.VirtualNetwork, networkIpams []*vivnfm.VirtualNetworkInterfaceIPAM) (*kubevirtv1.Network, *kubevirtv1.Interface, map[string]string, error) {
	if netInst.Metadata == nil {
		return nil, nil, nil, fmt.Errorf("SR-IOV network has no metadata: %w", apperrors.ErrUnsupported)
	}
	nadName, ok := netInst.Metadata.Fields[network.K8sNetworkNetAttachNameLabel]
	if !ok {
		netId := ""
		if netInst.NetworkResourceId != nil {
			netId = netInst.NetworkResourceId.Value
		}
		return nil, nil, nil, fmt.Errorf("SR-IOV network '%s' missing label '%s': %w",
			netId, network.K8sNetworkNetAttachNameLabel, apperrors.ErrUnsupported)
	}
	iface := &kubevirtv1.Interface{
		Name: nadName,
		InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
			SRIOV: &kubevirtv1.InterfaceSRIOV{},
		},
	}
	// Set MAC on the interface; KubeVirt forwards it to sriov-cni via the network selection request.
	for _, ipam := range networkIpams {
		if ipam.NetworkId != nil && netInst.NetworkResourceId != nil && ipam.NetworkId.Value == netInst.NetworkResourceId.Value {
			if ipam.MacAddress != nil && ipam.MacAddress.Mac != "" {
				iface.MacAddress = ipam.MacAddress.Mac
			}
			break
		}
	}
	return &kubevirtv1.Network{
		Name: nadName,
		NetworkSource: kubevirtv1.NetworkSource{
			Multus: &kubevirtv1.MultusNetwork{NetworkName: nadName},
		},
	}, iface, nil, nil
}

// initNetwork returns the kubevirt network and interface from the IPAM. The IPAM
// must reference a subnet.
func (r *ipamResolver) initNetwork(ctx context.Context, networkIpam *vivnfm.VirtualNetworkInterfaceIPAM) (*kubevirtv1.Network, *kubevirtv1.Interface, map[string]string, error) {
	if networkIpam.SubnetId == nil || networkIpam.SubnetId.Value == "" {
		return nil, nil, nil, &apperrors.ErrInvalidArgument{Field: "network IPAM", Reason: "must have a subnetId reference"}
	}
	getSubnetOpts := make([]network.GetSubnetOpt, 0)
	if misc.IsUUID(networkIpam.SubnetId.Value) {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByUid(networkIpam.GetSubnetId()))
	} else {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByName(networkIpam.GetSubnetId().Value))
	}
	subnet, err := r.netManager.GetSubnet(ctx, getSubnetOpts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get subnet '%s': %w", networkIpam.GetSubnetId().Value, err)
	}
	netAttachName, ok := subnet.Metadata.Fields[network.K8sSubnetNetAttachNameLabel]
	if !ok {
		return nil, nil, nil, fmt.Errorf("network subnet '%s' missing label '%s' to identify subnet name: %w", networkIpam.GetSubnetId().Value, network.K8sSubnetNetAttachNameLabel, apperrors.ErrUnsupported)
	}
	subnetName, ok := subnet.Metadata.Fields[network.K8sSubnetNameLabel]
	if !ok {
		return nil, nil, nil, fmt.Errorf("network subnet '%s' missing label '%s' to identify subnet name: %w", networkIpam.GetSubnetId().Value, network.K8sSubnetNameLabel, apperrors.ErrUnsupported)
	}
	// If multiple interfaces use the same subnet it will cause a problem if interface named the same as a subnet name.
	// Generate the unique UID for each network interface and combine it with a subnet-name
	ifaceUid, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate UUID for interface in subnet '%s': %w", networkIpam.GetSubnetId().Value, err)
	}
	ann := make(map[string]string)
	if networkIpam.IpAddress != nil && networkIpam.IpAddress.Ip != "" {
		ann[fmt.Sprintf("%s.%s.ovn.kubernetes.io/ip_address", netAttachName, r.namespace)] = networkIpam.IpAddress.Ip
	}
	if networkIpam.MacAddress != nil && networkIpam.MacAddress.Mac != "" {
		ann[fmt.Sprintf("%s.%s.ovn.kubernetes.io/mac_address", netAttachName, r.namespace)] = networkIpam.MacAddress.Mac
	}

	ann[fmt.Sprintf("%s.%s.ovn.kubernetes.io/logical_switch", netAttachName, r.namespace)] = subnetName

	ifaceName := fmt.Sprintf("%s-%s", subnetName, ifaceUid)
	return &kubevirtv1.Network{
			Name: ifaceName,
			NetworkSource: kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					//Note(dmalovan): Ignore the namespace for the networkAttachmentDefinition name since it will use VMI namesapce
					NetworkName: netAttachName,
				},
			},
		}, &kubevirtv1.Interface{
			Name: ifaceName,
			InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
				Bridge: &kubevirtv1.InterfaceBridge{},
			},
		}, ann, nil
}
