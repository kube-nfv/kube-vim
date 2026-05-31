package kubeovn

import (
	"context"
	"fmt"
	"net"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"go.uber.org/zap"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	vpcUID := string(vpc.GetUID())
	if err := m.ensureMgmtSubnet(ctx, log, cfg, vpcName, vpcUID, subnetName, nadName); err != nil {
		return fmt.Errorf("ensure management subnet '%s': %w", subnetName, err)
	}
	if err := m.ensureMgmtNetAttach(ctx, log, subnetName, nadName, nadNamespace); err != nil {
		return fmt.Errorf("ensure management network-attachment-definition '%s/%s': %w", nadNamespace, nadName, err)
	}

	log.Info("Management network is ready")
	return nil
}

func mgmtVpcLabels() map[string]string {
	return map[string]string{
		common.K8sManagedByLabel: common.KubeNfvName,
	}
}

func mgmtSubnetLabels(vpcName, vpcUID, subnetName, nadName string) map[string]string {
	labels := map[string]string{
		common.K8sManagedByLabel:            common.KubeNfvName,
		network.K8sSubnetNameLabel:          subnetName,
		network.K8sSubnetNetAttachNameLabel: nadName,
		network.K8sNetworkNameLabel:         vpcName,
		network.K8sNetworkTypeLabel:         nfvcommon.NetworkType_OVERLAY.String(),
	}
	// vpcUID is empty only on transient Vpc states where Create returned a
	// partially-populated object; skip the label so we don't write an empty
	// value. The next reconcile pass will fill it in.
	if vpcUID != "" {
		labels[network.K8sNetworkIdLabel] = vpcUID
	}
	return labels
}

func mgmtNetAttachLabels(subnetName string) map[string]string {
	return map[string]string{
		common.K8sManagedByLabel:   common.KubeNfvName,
		network.K8sSubnetNameLabel: subnetName,
	}
}

func (m *manager) ensureMgmtVpc(ctx context.Context, log *zap.Logger, vpcName string) (*kubeovnv1.Vpc, error) {
	existing, err := m.kubeOvnClient.KubeovnV1().Vpcs().Get(ctx, vpcName, v1.GetOptions{})
	if err == nil {
		changed, merged := misc.MergeLabels(existing.Labels, mgmtVpcLabels())
		if !changed {
			log.Debug("Vpc already exists with required labels")
			return existing, nil
		}
		existing.Labels = merged
		updated, err := m.kubeOvnClient.KubeovnV1().Vpcs().Update(ctx, existing, v1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("patch vpc labels: %w", err)
		}
		log.Debug("Patched Vpc labels")
		return updated, nil
	}
	if !k8s_errors.IsNotFound(err) {
		return nil, fmt.Errorf("get vpc: %w", err)
	}

	vpc := &kubeovnv1.Vpc{
		ObjectMeta: v1.ObjectMeta{
			Name:   vpcName,
			Labels: mgmtVpcLabels(),
		},
		Spec: kubeovnv1.VpcSpec{},
	}
	created, err := m.kubeOvnClient.KubeovnV1().Vpcs().Create(ctx, vpc, v1.CreateOptions{})
	if err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			// Race: another caller created it. Re-read to get a stable UID.
			return m.kubeOvnClient.KubeovnV1().Vpcs().Get(ctx, vpcName, v1.GetOptions{})
		}
		return nil, fmt.Errorf("create vpc: %w", err)
	}
	log.Debug("Created Vpc")
	return created, nil
}

func (m *manager) ensureMgmtSubnet(ctx context.Context, log *zap.Logger, cfg *config.ManagementNetworkConfig, vpcName, vpcUID, subnetName, nadName string) error {
	required := mgmtSubnetLabels(vpcName, vpcUID, subnetName, nadName)
	existing, err := m.kubeOvnClient.KubeovnV1().Subnets().Get(ctx, subnetName, v1.GetOptions{})
	if err == nil {
		changed, merged := misc.MergeLabels(existing.Labels, required)
		if !changed {
			log.Debug("Subnet already exists with required labels",
				zap.String("cidr", existing.Spec.CIDRBlock))
			return nil
		}
		existing.Labels = merged
		if _, err := m.kubeOvnClient.KubeovnV1().Subnets().Update(ctx, existing, v1.UpdateOptions{}); err != nil {
			return fmt.Errorf("patch subnet labels: %w", err)
		}
		log.Debug("Patched Subnet labels", zap.String("cidr", existing.Spec.CIDRBlock))
		return nil
	}
	if !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("get subnet: %w", err)
	}

	sub := &kubeovnv1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:   subnetName,
			Labels: required,
		},
		Spec: kubeovnv1.SubnetSpec{
			Vpc:         vpcName,
			Protocol:    "IPv4",
			CIDRBlock:   *cfg.Cidr,
			EnableDHCP:  true,
			GatewayType: "distributed",
			NatOutgoing: true,
			Provider:    "ovn",
		},
	}
	if cfg.Gateway != nil && *cfg.Gateway != "" {
		sub.Spec.Gateway = *cfg.Gateway
	}
	if cfg.ExcludeIps != nil && len(*cfg.ExcludeIps) > 0 {
		sub.Spec.ExcludeIps = append([]string(nil), (*cfg.ExcludeIps)...)
	}
	if _, err := m.kubeOvnClient.KubeovnV1().Subnets().Create(ctx, sub, v1.CreateOptions{}); err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create subnet: %w", err)
	}
	log.Debug("Created Subnet", zap.String("cidr", *cfg.Cidr))
	return nil
}

func (m *manager) ensureMgmtNetAttach(ctx context.Context, log *zap.Logger, subnetName, nadName, nadNamespace string) error {
	required := mgmtNetAttachLabels(subnetName)
	nadClient := m.netAttachClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(nadNamespace)
	existing, err := nadClient.Get(ctx, nadName, v1.GetOptions{})
	if err == nil {
		changed, merged := misc.MergeLabels(existing.Labels, required)
		if !changed {
			log.Debug("NetworkAttachmentDefinition already exists with required labels")
			return nil
		}
		existing.Labels = merged
		if _, err := nadClient.Update(ctx, existing, v1.UpdateOptions{}); err != nil {
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
	if _, err := nadClient.Create(ctx, nad, v1.CreateOptions{}); err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create network-attachment-definition: %w", err)
	}
	log.Debug("Created NetworkAttachmentDefinition")
	return nil
}
