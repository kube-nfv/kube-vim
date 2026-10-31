package kubeovn

import (
	"context"
	"fmt"
	"net"

	"github.com/google/uuid"
	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EnsureManagementNetwork provisions or reconciles the shared management
// network described by cfg. It is idempotent: existing resources are left
// untouched except for missing/wrong labels, which are patched in place. The
// subnet spec (CIDR, gateway) is never modified once the subnet exists, to
// avoid disrupting workloads already attached to it.
//
// cfg is expected to be already normalized (see config.Normalize): all
// derived fields (NetAttachDefName, NetAttachDefNamespace) must be set.
func (m *manager) EnsureManagementNetwork(ctx context.Context, cfg *config.ManagementNetworkConfig) error {
	if cfg == nil || cfg.Enabled == nil || !*cfg.Enabled {
		return nil
	}
	if cfg.Name == nil || *cfg.Name == "" {
		return fmt.Errorf("management network name is required")
	}
	if cfg.Cidr == nil || *cfg.Cidr == "" {
		return fmt.Errorf("management network cidr is required")
	}
	if _, _, err := net.ParseCIDR(*cfg.Cidr); err != nil {
		return fmt.Errorf("parse management network cidr '%s': %w", *cfg.Cidr, err)
	}
	if cfg.NetAttachDefName == nil || *cfg.NetAttachDefName == "" {
		return fmt.Errorf("management network netAttachDefName is required (Normalize should have set it)")
	}
	if cfg.NetAttachDefNamespace == nil || *cfg.NetAttachDefNamespace == "" {
		return fmt.Errorf("management network netAttachDefNamespace is required (Normalize should have set it)")
	}

	vpcName := *cfg.Name
	subnetName := formatSubnetName(vpcName, "0")
	nadName := *cfg.NetAttachDefName
	nadNamespace := *cfg.NetAttachDefNamespace

	log := m.logger.With(
		zap.String("vpc", vpcName),
		zap.String("subnet", subnetName),
		zap.String("netAttach", nadNamespace+"/"+nadName),
	)
	log.Debug("Ensuring kube-vim-managed management network")

	vpc, err := m.ensureMgmtVpc(ctx, log, vpcName)
	if err != nil {
		return fmt.Errorf("ensure management vpc '%s': %w", vpcName, err)
	}
	networkId := vpc.Labels[network.K8sNetworkIdLabel]
	if err := m.ensureMgmtSubnet(ctx, log, cfg, vpcName, networkId, subnetName, nadName, nadNamespace); err != nil {
		return fmt.Errorf("ensure management subnet '%s': %w", subnetName, err)
	}
	if err := m.ensureMgmtNetAttach(ctx, log, networkId, nadName, nadNamespace); err != nil {
		return fmt.Errorf("ensure management network-attachment-definition '%s/%s': %w", nadNamespace, nadName, err)
	}

	log.Info("Management network is ready")
	return nil
}

// idOrNew returns the id in labels[key], or a new one when there is none, so ids survive restarts.
func idOrNew(labels map[string]string, key string) string {
	if id := labels[key]; id != "" {
		return id
	}
	return uuid.NewString()
}

func mgmtVpcLabels(networkId string) map[string]string {
	labels := networkLabels(networkId)
	labels[network.K8sManagementNetworkLabel] = "true"
	return labels
}

func mgmtVpcAnnotations(vpcName string) (map[string]string, error) {
	return networkAnnotations(&networkv1.Network{NetworkName: vpcName, NetworkType: networkv1.NetworkType_NETWORK_TYPE_GENEVE})
}

func mgmtSubnetLabels(networkId, subnetId string) map[string]string {
	labels := networkLabels(networkId)
	labels[network.K8sSubnetIdIPv4Label] = subnetId
	return labels
}

func mgmtSubnetAnnotations(cidr string) (map[string]string, error) {
	raw, err := protojson.Marshal(&networkv1.Subnet{Data: &networkv1.SubnetData{IpVersion: commonv1.IpVersion_IP_VERSION_IPV4, Cidr: cidr}})
	if err != nil {
		return nil, fmt.Errorf("marshal management subnet: %w", err)
	}
	return map[string]string{network.K8sSubnetIPv4Annotation: string(raw)}, nil
}

// mergeMeta merges required labels and annotations into obj; it reports whether anything changed.
func mergeMeta(obj v1.Object, labels, annotations map[string]string) bool {
	labelsChanged, mergedLabels := misc.MergeLabels(obj.GetLabels(), labels)
	annotationsChanged, mergedAnnotations := misc.MergeLabels(obj.GetAnnotations(), annotations)
	obj.SetLabels(mergedLabels)
	obj.SetAnnotations(mergedAnnotations)
	return labelsChanged || annotationsChanged
}

func (m *manager) ensureMgmtVpc(ctx context.Context, log *zap.Logger, vpcName string) (*kubeovnv1.Vpc, error) {
	annotations, err := mgmtVpcAnnotations(vpcName)
	if err != nil {
		return nil, err
	}
	// Uncached read: the Vpc may pre-exist without the managed-by label (so it is
	// not in the cache) and this is exactly where we add that label.
	existing := &kubeovnv1.Vpc{}
	err = m.apiReader.Get(ctx, client.ObjectKey{Name: vpcName}, existing)
	if err == nil {
		base := existing.DeepCopy()
		if !mergeMeta(existing, mgmtVpcLabels(idOrNew(existing.Labels, network.K8sNetworkIdLabel)), annotations) {
			log.Debug("Vpc already exists with required labels")
			return existing, nil
		}
		if err := m.client.Patch(ctx, existing, client.MergeFrom(base)); err != nil {
			return nil, fmt.Errorf("patch vpc labels: %w", err)
		}
		log.Debug("Patched Vpc labels")
		return existing, nil
	}
	if !k8s_errors.IsNotFound(err) {
		return nil, fmt.Errorf("get vpc: %w", err)
	}

	vpc := &kubeovnv1.Vpc{
		ObjectMeta: v1.ObjectMeta{
			Name:        vpcName,
			Labels:      mgmtVpcLabels(uuid.NewString()),
			Annotations: annotations,
		},
		Spec: kubeovnv1.VpcSpec{},
	}
	if err := m.client.Create(ctx, vpc); err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			// Race: another caller created it. Re-read to get a stable UID.
			reread := &kubeovnv1.Vpc{}
			if err := m.apiReader.Get(ctx, client.ObjectKey{Name: vpcName}, reread); err != nil {
				return nil, fmt.Errorf("re-read vpc after AlreadyExists: %w", err)
			}
			return reread, nil
		}
		return nil, fmt.Errorf("create vpc: %w", err)
	}
	log.Debug("Created Vpc")
	return vpc, nil
}

func (m *manager) ensureMgmtSubnet(ctx context.Context, log *zap.Logger, cfg *config.ManagementNetworkConfig, vpcName, networkId, subnetName, nadName, nadNamespace string) error {
	existing := &kubeovnv1.Subnet{}
	err := m.apiReader.Get(ctx, client.ObjectKey{Name: subnetName}, existing)
	if err == nil {
		// The subnet spec is never changed, so the stored subnet follows its CIDR rather than cfg.
		annotations, err := mgmtSubnetAnnotations(existing.Spec.CIDRBlock)
		if err != nil {
			return err
		}
		base := existing.DeepCopy()
		if !mergeMeta(existing, mgmtSubnetLabels(networkId, idOrNew(existing.Labels, network.K8sSubnetIdIPv4Label)), annotations) {
			log.Debug("Subnet already exists with required labels",
				zap.String("cidr", existing.Spec.CIDRBlock))
			return nil
		}
		if err := m.client.Patch(ctx, existing, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("patch subnet labels: %w", err)
		}
		log.Debug("Patched Subnet labels", zap.String("cidr", existing.Spec.CIDRBlock))
		return nil
	}
	if !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("get subnet: %w", err)
	}

	annotations, err := mgmtSubnetAnnotations(*cfg.Cidr)
	if err != nil {
		return err
	}
	sub := &kubeovnv1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:        subnetName,
			Labels:      mgmtSubnetLabels(networkId, uuid.NewString()),
			Annotations: annotations,
		},
		Spec: kubeovnv1.SubnetSpec{
			Vpc:         vpcName,
			Protocol:    "IPv4",
			CIDRBlock:   *cfg.Cidr,
			EnableDHCP:  true,
			GatewayType: "distributed",
			NatOutgoing: true,
			Provider:    formatNetAttachKubeOvnProvider(nadName, nadNamespace),
		},
	}
	if cfg.Gateway != nil && *cfg.Gateway != "" {
		sub.Spec.Gateway = *cfg.Gateway
	}
	if cfg.ExcludeIps != nil && len(*cfg.ExcludeIps) > 0 {
		sub.Spec.ExcludeIps = append([]string(nil), (*cfg.ExcludeIps)...)
	}
	if err := m.client.Create(ctx, sub); err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create subnet: %w", err)
	}
	log.Debug("Created Subnet", zap.String("cidr", *cfg.Cidr))
	return nil
}

func (m *manager) ensureMgmtNetAttach(ctx context.Context, log *zap.Logger, networkId, nadName, nadNamespace string) error {
	required := withNicType(networkLabels(networkId), network.NicTypeBridge)
	nadKey := client.ObjectKey{Namespace: nadNamespace, Name: nadName}
	existing := &netattv1.NetworkAttachmentDefinition{}
	err := m.apiReader.Get(ctx, nadKey, existing)
	if err == nil {
		changed, merged := misc.MergeLabels(existing.Labels, required)
		if !changed {
			log.Debug("NetworkAttachmentDefinition already exists with required labels")
			return nil
		}
		base := existing.DeepCopy()
		existing.Labels = merged
		if err := m.client.Patch(ctx, existing, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("patch network-attachment-definition labels: %w", err)
		}
		log.Debug("Patched NetworkAttachmentDefinition labels")
		return nil
	}
	if !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("get network-attachment-definition: %w", err)
	}

	nad := &netattv1.NetworkAttachmentDefinition{
		ObjectMeta: v1.ObjectMeta{
			Name:      nadName,
			Namespace: nadNamespace,
			Labels:    required,
		},
		Spec: netattv1.NetworkAttachmentDefinitionSpec{
			Config: formatNetAttachConfig(nadName, nadNamespace),
		},
	}
	if err := m.client.Create(ctx, nad); err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create network-attachment-definition: %w", err)
	}
	log.Debug("Created NetworkAttachmentDefinition")
	return nil
}
