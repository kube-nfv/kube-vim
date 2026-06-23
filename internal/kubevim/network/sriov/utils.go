package sriov

import (
	"encoding/json"
	"fmt"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
)

const (
	nadResourceNameAnnotation = "k8s.v1.cni.cncf.io/resourceName"
	sriovCniVersion           = "0.3.1"
)

// ovsCniConfig is the subset of the ovs-cni (type: ovs) configuration kube-vim
// renders for SR-IOV networks. The VF representor is attached to an OVS bridge
// so VF traffic is hardware-offloaded by the NIC. The VF itself (vfio or
// netdevice) is delivered to the VM by Multus from the device-plugin resource
// referenced by the NAD's resourceName annotation.
type ovsCniConfig struct {
	CniVersion string `json:"cniVersion"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	// Vlan is the access VLAN applied to the OVS port (representor side).
	Vlan *int `json:"vlan,omitempty"`
	// SocketFile is the OVSDB socket ovs-cni connects to.
	SocketFile string `json:"socket_file,omitempty"`
}

// formatOvsCniConfig renders the NAD config. The OVS bridge is intentionally
// not set: ovs-cni auto-discovers it from the VF's PF, which is required for
// multi-PF hosts where each PF is attached to its own bridge.
func formatOvsCniConfig(name string, vlan uint64, socketFile string) (string, error) {
	cfg := ovsCniConfig{
		CniVersion: sriovCniVersion,
		Name:       name,
		Type:       "ovs",
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

func nadToNfvNetwork(nad *netattv1.NetworkAttachmentDefinition) (*vivnfm.VirtualNetwork, error) {
	if nad == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "nad", Reason: "cannot be nil"}
	}
	uid := nad.GetUID()
	if len(uid) == 0 {
		return nil, &apperrors.ErrInvalidArgument{Field: "nad UID", Reason: "cannot be empty"}
	}
	if !misc.IsObjectManagedByKubeNfv(nad) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{
			ObjectType: "NetworkAttachmentDefinition",
			ObjectName: nad.Name,
			ObjectId:   string(uid),
		}
	}

	name := nad.GetName()
	resourceName := nad.Annotations[nadResourceNameAnnotation]

	// Parse vlan back from the stored config. ovs-cni has no per-port rate
	// limiting, so bandwidth is not represented in the NAD.
	var cfg ovsCniConfig
	var vlan uint64
	if err := json.Unmarshal([]byte(nad.Spec.Config), &cfg); err == nil {
		if cfg.Vlan != nil {
			vlan = uint64(*cfg.Vlan)
		}
	}

	networkType := nfvcommon.NetworkType_NETWORK_TYPE_SRIOV
	return &vivnfm.VirtualNetwork{
		NetworkResourceId:   misc.UIDToIdentifier(uid),
		NetworkResourceName: &name,
		NetworkType:         networkType,
		ProviderNetwork:     &resourceName,
		SegmentationId:      &vlan,
		Bandwidth:           0,
		IsShared:            false,
		OperationalState:    nfvcommon.OperationalState_ENABLED,
		Metadata: &nfvcommon.Metadata{
			Fields: map[string]string{
				network.K8sNetworkNetAttachNameLabel: name,
			},
		},
	}, nil
}
