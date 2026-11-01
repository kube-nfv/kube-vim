package kubeovn

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	nadResourceNameAnnotation = "k8s.v1.cni.cncf.io/resourceName"
	ovsCniVersion             = "0.3.1"
)

// checkSupported rejects network fields kube-vim does not apply yet.
func checkSupported(n *networkv1.Network) error {
	for _, l3 := range n.GetLayer3Attributes() {
		unsupported := ""
		switch {
		case len(l3.GetAddressPools()) > 0:
			unsupported = "layer3Attributes.addressPools"
		case len(l3.GetDnsServers()) > 0:
			unsupported = "layer3Attributes.dnsServers"
		}
		if unsupported != "" {
			return fmt.Errorf("%s: %w", unsupported, apperrors.ErrUnsupported)
		}
	}
	return nil
}

func formatNetworkName(networkId string) string {
	return "net-" + networkId
}

// formatVpcName names a GENEVE network's Vpc; Kube-OVN rejects a subnet named like its Vpc.
func formatVpcName(networkName string) string {
	return networkName + "-vpc"
}

func formatSubnetName(networkName string, subnetName string) string {
	return fmt.Sprintf("%s-subnet-%s", networkName, subnetName)
}

func formatNetAttachName(subnetName string) string {
	return subnetName + "-netattach"
}

func formatSriovNetAttachName(networkName string) string {
	return networkName + "-sriov"
}

func formatNetAttachConfig(netAttachName string, namespace string) string {
	return fmt.Sprintf(`{
"cniVersion": "0.3.0",
"type": "kube-ovn",
"server_socket": "/run/openvswitch/kube-ovn-daemon.sock",
"provider": "%s"
}`, formatNetAttachKubeOvnProvider(netAttachName, namespace))
}

func formatNetAttachKubeOvnProvider(netAttachName, namespace string) string {
	return fmt.Sprintf("%s.%s.ovn", netAttachName, namespace)
}

// ovsCniConfig is the subset of the ovs-cni (type: ovs) configuration kube-vim
// renders for SR-IOV interfaces. The VF representor is attached to an OVS bridge
// so VF traffic is hardware-offloaded by the NIC. The VF itself is delivered to
// the VM by Multus from the device-plugin resource named in the NAD's
// resourceName annotation.
type ovsCniConfig struct {
	CniVersion string `json:"cniVersion"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	// Vlan is the access VLAN applied to the OVS port (representor side).
	Vlan *int `json:"vlan,omitempty"`
	MTU  int  `json:"mtu,omitempty"`
	// SocketFile is the OVSDB socket ovs-cni connects to.
	SocketFile string `json:"socket_file,omitempty"`
}

// formatOvsCniConfig renders the NAD config. The OVS bridge is intentionally
// not set: ovs-cni auto-discovers it from the VF's PF, which is required for
// multi-PF hosts where each PF is attached to its own bridge.
func formatOvsCniConfig(name string, vlan uint32, mtu uint32, socketFile string) (string, error) {
	cfg := ovsCniConfig{
		CniVersion: ovsCniVersion,
		Name:       name,
		Type:       "ovs",
		MTU:        int(mtu),
		SocketFile: socketFile,
	}
	if vlan != 0 {
		v := int(vlan)
		cfg.Vlan = &v
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal ovs cni config: %w", err)
	}
	return string(b), nil
}

func networkLabels(networkId string) map[string]string {
	return map[string]string{
		common.K8sManagedByLabel:  common.KubeNfvName,
		network.K8sNetworkIdLabel: networkId,
	}
}

// networkAnnotations stores n without its id, subnets and outputs.
func networkAnnotations(n *networkv1.Network) (map[string]string, error) {
	stored := proto.CloneOf(n)
	stored.NetworkId = ""
	stored.SubnetIds = nil
	stored.Layer3Attributes = nil
	stored.OperationalState = commonv1.OperationalState_OPERATIONAL_STATE_UNSPECIFIED
	raw, err := protojson.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal network: %w", err)
	}
	return map[string]string{network.K8sNetworkAnnotation: string(raw)}, nil
}

// primaryObject builds the object that carries a network: a Vpc for GENEVE, a Vlan on the provider network otherwise.
func primaryObject(name string, n *networkv1.Network, labels, annotations map[string]string) (*kubeovnv1.Vpc, *kubeovnv1.Vlan) {
	meta := v1.ObjectMeta{Name: name, Labels: labels, Annotations: annotations}
	if n.GetNetworkType() == networkv1.NetworkType_NETWORK_TYPE_GENEVE {
		meta.Name = formatVpcName(name)
		return &kubeovnv1.Vpc{ObjectMeta: meta}, nil
	}
	return nil, &kubeovnv1.Vlan{
		ObjectMeta: meta,
		// FLAT networks are untagged: VLAN 0 on the provider network.
		Spec: kubeovnv1.VlanSpec{ID: int(n.GetSegmentationId()), Provider: n.GetProviderNetwork()},
	}
}

// subnetIdLabel and subnetAnnotation return the label and annotation of an IP version.
func subnetIdLabel(v commonv1.IpVersion) string {
	if v == commonv1.IpVersion_IP_VERSION_IPV6 {
		return network.K8sSubnetIdIPv6Label
	}
	return network.K8sSubnetIdIPv4Label
}

func subnetAnnotation(v commonv1.IpVersion) string {
	if v == commonv1.IpVersion_IP_VERSION_IPV6 {
		return network.K8sSubnetIPv6Annotation
	}
	return network.K8sSubnetIPv4Annotation
}

// kubeovnSubnet builds the network's single Kube-OVN subnet, named like the network's Vlan, holding one
// address family per entry of l3; subnetIds gives the id of each entry.
func kubeovnSubnet(name string, n *networkv1.Network, l3 []*networkv1.SubnetData, subnetIds []string, labels map[string]string, provider string) (*kubeovnv1.Subnet, error) {
	sub := &kubeovnv1.Subnet{
		ObjectMeta: v1.ObjectMeta{Name: name, Labels: map[string]string{}, Annotations: map[string]string{}},
		Spec: kubeovnv1.SubnetSpec{
			Default:  false,
			Provider: provider,
			Mtu:      n.GetMtu(),
			// Only distributed gateway currently supported
			GatewayType: "distributed",
			// Enable outgoing NAT by default
			NatOutgoing: true,
		},
	}
	for k, v := range labels {
		sub.Labels[k] = v
	}
	if n.GetNetworkType() == networkv1.NetworkType_NETWORK_TYPE_GENEVE {
		sub.Spec.Vpc = formatVpcName(name)
	} else {
		sub.Spec.Vlan = name
	}
	var cidrs, gateways []string
	dhcp := map[bool]bool{}
	for idx, d := range l3 {
		cidrs = append(cidrs, d.GetCidr())
		if d.GetGatewayIp() != "" {
			gateways = append(gateways, d.GetGatewayIp())
		}
		// DHCP is on unless the subnet turns it off.
		dhcp[d.IsDhcpEnabled == nil || d.GetIsDhcpEnabled()] = true
		raw, err := protojson.Marshal(&networkv1.Subnet{Data: d})
		if err != nil {
			return nil, fmt.Errorf("marshal subnet: %w", err)
		}
		sub.Labels[subnetIdLabel(d.GetIpVersion())] = subnetIds[idx]
		sub.Annotations[subnetAnnotation(d.GetIpVersion())] = string(raw)
	}
	if len(dhcp) > 1 {
		return nil, fmt.Errorf("layer3Attributes.isDhcpEnabled differing between IP versions: %w", apperrors.ErrUnsupported)
	}
	if len(gateways) != 0 && len(gateways) != len(l3) {
		return nil, &apperrors.ErrInvalidArgument{Field: "layer3Attributes.gatewayIp", Reason: "set on every subnet of a dual-stack network or on none"}
	}
	sub.Spec.EnableDHCP = dhcp[true]
	sub.Spec.CIDRBlock = strings.Join(cidrs, ",")
	sub.Spec.Gateway = strings.Join(gateways, ",")
	switch len(l3) {
	case 1:
		sub.Spec.Protocol = kubeovnProtocol(l3[0].GetIpVersion())
	default:
		sub.Spec.Protocol = "Dual"
	}
	return sub, nil
}

func kubeovnProtocol(v commonv1.IpVersion) string {
	if v == commonv1.IpVersion_IP_VERSION_IPV6 {
		return "IPv6"
	}
	return "IPv4"
}

// bridgeNetAttach builds the kube-ovn NetworkAttachmentDefinition BRIDGE interfaces attach through.
func bridgeNetAttach(name, namespace string, labels map[string]string) *netattv1.NetworkAttachmentDefinition {
	return &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: namespace, Labels: withNicType(labels, network.NicTypeBridge)},
		Spec:       netattv1.NetworkAttachmentDefinitionSpec{Config: formatNetAttachConfig(name, namespace)},
	}
}

// sriovNetAttach builds the ovs-cni NetworkAttachmentDefinition SRIOV interfaces attach through.
func sriovNetAttach(name, namespace string, n *networkv1.Network, labels map[string]string, socketFile string) (*netattv1.NetworkAttachmentDefinition, error) {
	cfg, err := formatOvsCniConfig(name, n.GetSegmentationId(), n.GetMtu(), socketFile)
	if err != nil {
		return nil, err
	}
	return &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      withNicType(labels, network.NicTypeSriov),
			Annotations: map[string]string{nadResourceNameAnnotation: n.GetSriov().GetPool()},
		},
		Spec: netattv1.NetworkAttachmentDefinitionSpec{Config: cfg},
	}, nil
}

func withNicType(labels map[string]string, nicType string) map[string]string {
	res := map[string]string{network.K8sNicTypeLabel: nicType}
	for k, v := range labels {
		res[k] = v
	}
	return res
}

// networkFromObjects returns the network stored on primary (its Vpc or Vlan) with its subnet ids from sub (nil when it has none).
func networkFromObjects(primary v1.Object, sub *kubeovnv1.Subnet) (*networkv1.Network, error) {
	raw, ok := primary.GetAnnotations()[network.K8sNetworkAnnotation]
	if !ok {
		return nil, fmt.Errorf("'%s' has no '%s' annotation: %w", primary.GetName(), network.K8sNetworkAnnotation, apperrors.ErrInternal)
	}
	n := &networkv1.Network{}
	if err := protojson.Unmarshal([]byte(raw), n); err != nil {
		return nil, fmt.Errorf("unmarshal network of '%s': %w", primary.GetName(), err)
	}
	n.NetworkId = primary.GetLabels()[network.K8sNetworkIdLabel]
	n.OperationalState = commonv1.OperationalState_OPERATIONAL_STATE_ENABLED
	if sub != nil {
		for _, label := range []string{network.K8sSubnetIdIPv4Label, network.K8sSubnetIdIPv6Label} {
			if id := sub.Labels[label]; id != "" {
				n.SubnetIds = append(n.SubnetIds, id)
			}
		}
	}
	return n, nil
}

// subnetFromKubeovn returns the subnet of IP version v that sub carries, with the gateway Kube-OVN chose when none was given.
func subnetFromKubeovn(sub *kubeovnv1.Subnet, v commonv1.IpVersion) (*networkv1.Subnet, error) {
	raw, ok := sub.Annotations[subnetAnnotation(v)]
	if !ok {
		return nil, fmt.Errorf("kube-ovn subnet '%s' has no '%s' annotation: %w", sub.Name, subnetAnnotation(v), apperrors.ErrInternal)
	}
	s := &networkv1.Subnet{}
	if err := protojson.Unmarshal([]byte(raw), s); err != nil {
		return nil, fmt.Errorf("unmarshal subnet of kube-ovn subnet '%s': %w", sub.Name, err)
	}
	s.SubnetId = sub.Labels[subnetIdLabel(v)]
	s.NetworkId = sub.Labels[network.K8sNetworkIdLabel]
	if s.Data != nil && s.Data.GatewayIp == "" {
		s.Data.GatewayIp = gatewayOf(sub.Spec.Gateway, v)
	}
	return s, nil
}

// gatewayOf picks the gateway of IP version v from a Kube-OVN gateway list ("v4" or "v4,v6").
func gatewayOf(gateways string, v commonv1.IpVersion) string {
	for _, gw := range strings.Split(gateways, ",") {
		ip := net.ParseIP(gw)
		if ip == nil {
			continue
		}
		if (ip.To4() != nil) == (v == commonv1.IpVersion_IP_VERSION_IPV4) {
			return gw
		}
	}
	return ""
}

// subnetVersions returns the IP versions sub carries subnets for.
func subnetVersions(sub *kubeovnv1.Subnet) []commonv1.IpVersion {
	var res []commonv1.IpVersion
	for _, v := range []commonv1.IpVersion{commonv1.IpVersion_IP_VERSION_IPV4, commonv1.IpVersion_IP_VERSION_IPV6} {
		if sub.Labels[subnetIdLabel(v)] != "" {
			res = append(res, v)
		}
	}
	return res
}
