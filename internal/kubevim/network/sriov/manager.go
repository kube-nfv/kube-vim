package sriov

import (
	"context"
	"fmt"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netatt_client "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"go.uber.org/zap"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type manager struct {
	logger          *zap.Logger
	netAttachClient *netatt_client.Clientset
	k8sCfg          *config.K8sConfig
}

func NewSriovNetworkManager(restConfig *rest.Config, k8sCfg *config.K8sConfig, logger *zap.Logger) (*manager, error) {
	if k8sCfg.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config k8s.Namespace", Reason: "can't be nil"}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	netAttC, err := netatt_client.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create multus network-attachment-definition k8s client: %w", err)
	}
	return &manager{
		logger:          logger,
		netAttachClient: netAttC,
		k8sCfg:          k8sCfg,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string, networkData *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	if networkData.GetNetworkType() != nfvcommon.NetworkType_NETWORK_TYPE_SRIOV {
		return nil, fmt.Errorf("create sriov network '%s': unexpected network type '%s': %w", name, networkData.GetNetworkType(), apperrors.ErrUnsupported)
	}
	if networkData.ProviderNetwork == nil || *networkData.ProviderNetwork == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "providerNetwork", Reason: "required for SR-IOV networks (device plugin resource name)"}
	}
	if len(networkData.Layer3Attributes) > 0 {
		return nil, &apperrors.ErrInvalidArgument{Field: "layer3Attributes", Reason: "SR-IOV networks do not support subnets"}
	}

	var vlan uint64
	if networkData.SegmentationId != nil {
		vlan = *networkData.SegmentationId
	}
	cniConfig, err := formatSriovCniConfig(name, vlan, networkData.Bandwidth)
	if err != nil {
		return nil, fmt.Errorf("build sriov-cni config for network '%s': %w", name, err)
	}

	nad := &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: *m.k8sCfg.Namespace,
			Annotations: map[string]string{
				nadResourceNameAnnotation: *networkData.ProviderNetwork,
			},
			Labels: map[string]string{
				common.K8sManagedByLabel:     common.KubeNfvName,
				network.K8sNetworkTypeLabel:  nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String(),
				network.K8sNetworkNameLabel:  name,
			},
		},
		Spec: netattv1.NetworkAttachmentDefinitionSpec{
			Config: cniConfig,
		},
	}

	created, err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(*m.k8sCfg.Namespace).Create(ctx, nad, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create SR-IOV NetworkAttachmentDefinition '%s': %w", name, err)
	}
	return nadToNfvNetwork(created)
}

func (m *manager) GetNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*vivnfm.VirtualNetwork, error) {
	cfg := network.ApplyGetNetworkOpts(opts...)

	labelSel := fmt.Sprintf("%s=%s,%s=%s",
		common.K8sManagedByLabel, common.KubeNfvName,
		network.K8sNetworkTypeLabel, nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String(),
	)

	if cfg.Name != "" {
		nad, err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(*m.k8sCfg.Namespace).Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				return nil, &apperrors.ErrNotFound{Entity: fmt.Sprintf("SR-IOV network '%s'", cfg.Name)}
			}
			return nil, fmt.Errorf("get SR-IOV NetworkAttachmentDefinition '%s': %w", cfg.Name, err)
		}
		return nadToNfvNetwork(nad)
	}
	if cfg.Uid != nil && cfg.Uid.Value != "" {
		list, err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(*m.k8sCfg.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSel})
		if err != nil {
			return nil, fmt.Errorf("list SR-IOV NetworkAttachmentDefinitions: %w", err)
		}
		for i := range list.Items {
			if string(list.Items[i].GetUID()) == cfg.Uid.Value {
				return nadToNfvNetwork(&list.Items[i])
			}
		}
		return nil, &apperrors.ErrNotFound{Entity: fmt.Sprintf("SR-IOV network with id '%s'", cfg.Uid.Value)}
	}
	return nil, &apperrors.ErrInvalidArgument{Field: "GetNetworkOpt", Reason: "name or uid required"}
}

func (m *manager) ListNetworks(ctx context.Context) ([]*vivnfm.VirtualNetwork, error) {
	labelSel := fmt.Sprintf("%s=%s,%s=%s",
		common.K8sManagedByLabel, common.KubeNfvName,
		network.K8sNetworkTypeLabel, nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String(),
	)
	list, err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(*m.k8sCfg.Namespace).List(ctx, v1.ListOptions{LabelSelector: labelSel})
	if err != nil {
		return nil, fmt.Errorf("list SR-IOV NetworkAttachmentDefinitions: %w", err)
	}
	result := make([]*vivnfm.VirtualNetwork, 0, len(list.Items))
	for i := range list.Items {
		net, err := nadToNfvNetwork(&list.Items[i])
		if err != nil {
			m.logger.Warn("skip malformed SR-IOV NAD during list", zap.String("name", list.Items[i].Name), zap.Error(err))
			continue
		}
		result = append(result, net)
	}
	return result, nil
}

func (m *manager) DeleteNetwork(ctx context.Context, opts ...network.GetNetworkOpt) error {
	net, err := m.GetNetwork(ctx, opts...)
	if err != nil {
		return fmt.Errorf("get SR-IOV network: %w", err)
	}
	if net.NetworkResourceName == nil {
		return &apperrors.ErrInvalidArgument{Field: "networkResourceName", Reason: "cannot be nil"}
	}
	if err := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(*m.k8sCfg.Namespace).Delete(ctx, *net.NetworkResourceName, v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete SR-IOV NetworkAttachmentDefinition '%s': %w", *net.NetworkResourceName, err)
	}
	return nil
}

func (m *manager) CreateSubnet(_ context.Context, _ string, _ *vivnfm.NetworkSubnetData) (*vivnfm.NetworkSubnet, error) {
	return nil, fmt.Errorf("SR-IOV networks do not support subnets: %w", apperrors.ErrUnsupported)
}

func (m *manager) GetSubnet(_ context.Context, _ ...network.GetSubnetOpt) (*vivnfm.NetworkSubnet, error) {
	return nil, fmt.Errorf("SR-IOV networks do not support subnets: %w", apperrors.ErrUnsupported)
}

func (m *manager) ListSubnets(_ context.Context) ([]*vivnfm.NetworkSubnet, error) {
	return nil, fmt.Errorf("SR-IOV networks do not support subnets: %w", apperrors.ErrUnsupported)
}

func (m *manager) DeleteSubnet(_ context.Context, _ ...network.GetSubnetOpt) error {
	return fmt.Errorf("SR-IOV networks do not support subnets: %w", apperrors.ErrUnsupported)
}

func (m *manager) EnsureManagementNetwork(_ context.Context, _ *config.ManagementNetworkConfig) error {
	// SR-IOV backend has no management-network concept.
	return nil
}

// isSriovNad returns true when the NAD was created by the sriov backend.
func isSriovNad(nad *netattv1.NetworkAttachmentDefinition) bool {
	labels := nad.GetLabels()
	return misc.IsObjectManagedByKubeNfv(nad) &&
		labels[network.K8sNetworkTypeLabel] == nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String()
}
