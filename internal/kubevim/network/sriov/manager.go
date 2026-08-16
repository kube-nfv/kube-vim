package sriov

import (
	"context"
	"fmt"
	"strings"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"go.uber.org/zap"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type manager struct {
	logger *zap.Logger
	// client serves cache-backed reads and direct writes.
	client     client.Client
	k8sCfg     *config.K8sConfig
	socketFile string
}

func NewSriovNetworkManager(cl client.Client, k8sCfg *config.K8sConfig, sriovCfg *config.SriovNetworkConfig, logger *zap.Logger) (*manager, error) {
	if k8sCfg.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config k8s.Namespace", Reason: "can't be nil"}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	var socketFile string
	if sriovCfg != nil && sriovCfg.SocketFile != nil {
		socketFile = *sriovCfg.SocketFile
	}
	return &manager{
		logger:     logger,
		client:     cl,
		k8sCfg:     k8sCfg,
		socketFile: socketFile,
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

	resourceName := *networkData.ProviderNetwork
	if !strings.Contains(resourceName, "/") {
		resourceName = "openshift.io/" + resourceName
	}

	var vlan uint64
	if networkData.SegmentationId != nil {
		vlan = *networkData.SegmentationId
	}
	cniConfig, err := formatOvsCniConfig(name, vlan, m.socketFile)
	if err != nil {
		return nil, fmt.Errorf("build ovs-cni config for network '%s': %w", name, err)
	}

	nad := &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: *m.k8sCfg.Namespace,
			Annotations: map[string]string{
				nadResourceNameAnnotation: resourceName,
			},
			Labels: map[string]string{
				common.K8sManagedByLabel:    common.KubeNfvName,
				network.K8sNetworkTypeLabel: nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String(),
				network.K8sNetworkNameLabel: name,
			},
		},
		Spec: netattv1.NetworkAttachmentDefinitionSpec{
			Config: cniConfig,
		},
	}

	if err := m.client.Create(ctx, nad); err != nil {
		return nil, fmt.Errorf("create SR-IOV NetworkAttachmentDefinition '%s': %w", name, err)
	}
	return nadToNfvNetwork(nad)
}

func (m *manager) GetNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*vivnfm.VirtualNetwork, error) {
	cfg := network.ApplyGetNetworkOpts(opts...)

	if cfg.Name != "" {
		nad := &netattv1.NetworkAttachmentDefinition{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: *m.k8sCfg.Namespace, Name: cfg.Name}, nad); err != nil {
			if k8s_errors.IsNotFound(err) {
				return nil, &apperrors.ErrNotFound{Entity: fmt.Sprintf("SR-IOV network '%s'", cfg.Name)}
			}
			return nil, fmt.Errorf("get SR-IOV NetworkAttachmentDefinition '%s': %w", cfg.Name, err)
		}
		if nad.Labels[network.K8sNetworkTypeLabel] != nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String() {
			return nil, &apperrors.ErrNotFound{Entity: fmt.Sprintf("SR-IOV network '%s'", cfg.Name)}
		}
		return nadToNfvNetwork(nad)
	}
	if cfg.Uid != nil && cfg.Uid.Value != "" {
		list := &netattv1.NetworkAttachmentDefinitionList{}
		if err := m.client.List(ctx, list, client.InNamespace(*m.k8sCfg.Namespace), m.sriovSelector()); err != nil {
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

// sriovSelector matches kube-nfv-owned SR-IOV NetworkAttachmentDefinitions.
func (m *manager) sriovSelector() client.MatchingLabels {
	return client.MatchingLabels{
		common.K8sManagedByLabel:    common.KubeNfvName,
		network.K8sNetworkTypeLabel: nfvcommon.NetworkType_NETWORK_TYPE_SRIOV.String(),
	}
}

func (m *manager) ListNetworks(ctx context.Context) ([]*vivnfm.VirtualNetwork, error) {
	list := &netattv1.NetworkAttachmentDefinitionList{}
	if err := m.client.List(ctx, list, client.InNamespace(*m.k8sCfg.Namespace), m.sriovSelector()); err != nil {
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
	nad := &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{Name: *net.NetworkResourceName, Namespace: *m.k8sCfg.Namespace},
	}
	if err := m.client.Delete(ctx, nad); err != nil {
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
