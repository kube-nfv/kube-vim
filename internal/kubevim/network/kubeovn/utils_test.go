package kubeovn

import (
	"encoding/json"
	"testing"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestFormatOvsCniConfig(t *testing.T) {
	t.Run("with vlan and mtu", func(t *testing.T) {
		raw, err := formatOvsCniConfig("net1", 100, 9000, "unix:/s.sock")
		require.NoError(t, err)
		var cfg ovsCniConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		require.NotNil(t, cfg.Vlan)
		assert.Equal(t, 100, *cfg.Vlan)
		assert.Equal(t, 9000, cfg.MTU)
		assert.Equal(t, "unix:/s.sock", cfg.SocketFile)
	})
	t.Run("vlan and mtu zero omitted", func(t *testing.T) {
		raw, err := formatOvsCniConfig("net1", 0, 0, "")
		require.NoError(t, err)
		assert.NotContains(t, raw, "vlan")
		assert.NotContains(t, raw, "mtu")
	})
}

func TestGatewayOf(t *testing.T) {
	assert.Equal(t, "10.0.0.1", gatewayOf("10.0.0.1,fd00::1", commonv1.IpVersion_IP_VERSION_IPV4))
	assert.Equal(t, "fd00::1", gatewayOf("10.0.0.1,fd00::1", commonv1.IpVersion_IP_VERSION_IPV6))
	assert.Equal(t, "", gatewayOf("10.0.0.1", commonv1.IpVersion_IP_VERSION_IPV6))
}

func TestKubeovnSubnetDhcpMustAgree(t *testing.T) {
	off := v6("fd00::/64")
	off.IsDhcpEnabled = proto.Bool(false)
	_, err := kubeovnSubnet("net-x", geneve(), []*networkv1.SubnetData{v4("10.0.0.0/24"), off}, []string{"a", "b"}, nil, "p")
	assert.ErrorIs(t, err, apperrors.ErrUnsupported)
}

func TestSubnetGatewayFromKubeovn(t *testing.T) {
	sub, err := kubeovnSubnet("net-x", geneve(), []*networkv1.SubnetData{v4("10.0.0.0/24")}, []string{"a"}, nil, "p")
	require.NoError(t, err)
	// Kube-OVN fills in the gateway when none was given.
	sub.Spec.Gateway = "10.0.0.1"
	got, err := subnetFromKubeovn(sub, commonv1.IpVersion_IP_VERSION_IPV4)
	require.NoError(t, err)
	assert.Equal(t, "a", got.SubnetId)
	assert.Equal(t, "10.0.0.1", got.GetData().GetGatewayIp())
}

func TestKubeovnSubnetGatewayMode(t *testing.T) {
	withMode := func(d *networkv1.SubnetData, m networkv1.GatewayMode) *networkv1.SubnetData {
		d.GatewayMode = m
		return d
	}
	tests := []struct {
		name             string
		l3               []*networkv1.SubnetData
		wantDisableCheck bool
		wantLogical      bool
		wantErr          error
	}{
		{name: "unset is none", l3: []*networkv1.SubnetData{v4("10.0.0.0/24")}, wantDisableCheck: true},
		{name: "none", l3: []*networkv1.SubnetData{withMode(v4("10.0.0.0/24"), networkv1.GatewayMode_GATEWAY_MODE_NONE)}, wantDisableCheck: true},
		{name: "external is checked", l3: []*networkv1.SubnetData{withMode(v4("10.0.0.0/24"), networkv1.GatewayMode_GATEWAY_MODE_EXTERNAL)}},
		{name: "vim is a logical gateway", l3: []*networkv1.SubnetData{withMode(v4("10.0.0.0/24"), networkv1.GatewayMode_GATEWAY_MODE_VIM)}, wantLogical: true},
		{name: "dual-stack modes must agree", l3: []*networkv1.SubnetData{
			withMode(v4("10.0.0.0/24"), networkv1.GatewayMode_GATEWAY_MODE_VIM), v6("fd00::/64"),
		}, wantErr: apperrors.ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := []string{"a", "b"}[:len(tt.l3)]
			sub, err := kubeovnSubnet("net-x", vlan(100), tt.l3, ids, nil, "p")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDisableCheck, sub.Spec.DisableGatewayCheck)
			assert.Equal(t, tt.wantLogical, sub.Spec.LogicalGateway)
		})
	}

	t.Run("geneve is untouched", func(t *testing.T) {
		sub, err := kubeovnSubnet("net-x", geneve(), []*networkv1.SubnetData{v4("10.0.0.0/24")}, []string{"a"}, nil, "p")
		require.NoError(t, err)
		assert.False(t, sub.Spec.DisableGatewayCheck)
		assert.False(t, sub.Spec.LogicalGateway)
	})
}
