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
