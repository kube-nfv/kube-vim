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

type sriovCniConfig struct {
	CniVersion string `json:"cniVersion"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Vlan       *int   `json:"vlan,omitempty"`
	SpoofChk   string `json:"spoofchk"`
	Trust      string `json:"trust"`
	LinkState  string `json:"link_state"`
	MinTxRate  *int   `json:"min_tx_rate,omitempty"`
}

func formatSriovCniConfig(name string, vlan uint64, minTxRateMbps float32) (string, error) {
	cfg := sriovCniConfig{
		CniVersion: sriovCniVersion,
		Name:       name,
		Type:       "sriov",
		SpoofChk:   "off",
		Trust:      "on",
		LinkState:  "auto",
	}
	if vlan != 0 {
		v := int(vlan)
		cfg.Vlan = &v
	}
	if minTxRateMbps > 0 {
		r := int(minTxRateMbps)
		cfg.MinTxRate = &r
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal sriov cni config: %w", err)
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

	// Parse vlan and min_tx_rate back from the stored config.
	var cfg sriovCniConfig
	var vlan uint64
	var bandwidth float32
	if err := json.Unmarshal([]byte(nad.Spec.Config), &cfg); err == nil {
		if cfg.Vlan != nil {
			vlan = uint64(*cfg.Vlan)
		}
		if cfg.MinTxRate != nil {
			bandwidth = float32(*cfg.MinTxRate)
		}
	}

	networkType := nfvcommon.NetworkType_NETWORK_TYPE_SRIOV
	return &vivnfm.VirtualNetwork{
		NetworkResourceId:   misc.UIDToIdentifier(uid),
		NetworkResourceName: &name,
		NetworkType:         networkType,
		ProviderNetwork:     &resourceName,
		SegmentationId:      &vlan,
		Bandwidth:           bandwidth,
		IsShared:            false,
		OperationalState:    nfvcommon.OperationalState_ENABLED,
		Metadata: &nfvcommon.Metadata{
			Fields: map[string]string{
				network.K8sNetworkNetAttachNameLabel: name,
			},
		},
	}, nil
}
