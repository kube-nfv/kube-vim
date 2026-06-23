package composite

import (
	"context"
	"errors"
	"fmt"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
)

type manager struct {
	ovn   network.Manager
	sriov network.Manager
}

func NewManager(ovn, sriov network.Manager) network.Manager {
	return &manager{ovn: ovn, sriov: sriov}
}

func (m *manager) CreateNetwork(ctx context.Context, name string, data *vivnfm.VirtualNetworkData) (*vivnfm.VirtualNetwork, error) {
	if data.NetworkType != nil && *data.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_SRIOV {
		return m.sriov.CreateNetwork(ctx, name, data)
	}
	return m.ovn.CreateNetwork(ctx, name, data)
}

func (m *manager) GetNetwork(ctx context.Context, opts ...network.GetNetworkOpt) (*vivnfm.VirtualNetwork, error) {
	net, err := m.ovn.GetNetwork(ctx, opts...)
	if err == nil {
		return net, nil
	}
	if !isNotFound(err) {
		return nil, err
	}
	net, sriovErr := m.sriov.GetNetwork(ctx, opts...)
	if sriovErr == nil {
		return net, nil
	}
	if !isNotFound(sriovErr) {
		return nil, sriovErr
	}
	return nil, err
}

func (m *manager) ListNetworks(ctx context.Context) ([]*vivnfm.VirtualNetwork, error) {
	ovnNets, err := m.ovn.ListNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ovn networks: %w", err)
	}
	sriovNets, err := m.sriov.ListNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sriov networks: %w", err)
	}
	return append(ovnNets, sriovNets...), nil
}

func (m *manager) DeleteNetwork(ctx context.Context, opts ...network.GetNetworkOpt) error {
	err := m.ovn.DeleteNetwork(ctx, opts...)
	if err == nil {
		return nil
	}
	if !isNotFound(err) {
		return err
	}
	sriovErr := m.sriov.DeleteNetwork(ctx, opts...)
	if sriovErr == nil {
		return nil
	}
	if !isNotFound(sriovErr) {
		return sriovErr
	}
	return err
}

func (m *manager) CreateSubnet(ctx context.Context, name string, data *vivnfm.NetworkSubnetData) (*vivnfm.NetworkSubnet, error) {
	if data.NetworkId != nil {
		net, err := m.GetNetwork(ctx, network.GetNetworkByUid(data.NetworkId))
		if err != nil {
			return nil, fmt.Errorf("resolve network for subnet creation: %w", err)
		}
		if net.NetworkType == nfvcommon.NetworkType_NETWORK_TYPE_SRIOV {
			return m.sriov.CreateSubnet(ctx, name, data)
		}
	}
	return m.ovn.CreateSubnet(ctx, name, data)
}

func (m *manager) GetSubnet(ctx context.Context, opts ...network.GetSubnetOpt) (*vivnfm.NetworkSubnet, error) {
	return m.ovn.GetSubnet(ctx, opts...)
}

func (m *manager) ListSubnets(ctx context.Context) ([]*vivnfm.NetworkSubnet, error) {
	return m.ovn.ListSubnets(ctx)
}

func (m *manager) DeleteSubnet(ctx context.Context, opts ...network.GetSubnetOpt) error {
	return m.ovn.DeleteSubnet(ctx, opts...)
}

func (m *manager) EnsureManagementNetwork(ctx context.Context, cfg *config.ManagementNetworkConfig) error {
	return m.ovn.EnsureManagementNetwork(ctx, cfg)
}

func isNotFound(err error) bool {
	var notFound *apperrors.ErrNotFound
	return errors.As(err, &notFound)
}
