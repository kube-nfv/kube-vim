package sriov

import (
	"encoding/json"
	"testing"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFormatOvsCniConfig(t *testing.T) {
	t.Run("with vlan", func(t *testing.T) {
		out, err := formatOvsCniConfig("net1", 100, "/run/ovn.sock")
		require.NoError(t, err)
		var cfg ovsCniConfig
		require.NoError(t, json.Unmarshal([]byte(out), &cfg))
		assert.Equal(t, sriovCniVersion, cfg.CniVersion)
		assert.Equal(t, "net1", cfg.Name)
		assert.Equal(t, "ovs", cfg.Type)
		require.NotNil(t, cfg.Vlan)
		assert.Equal(t, 100, *cfg.Vlan)
		assert.Equal(t, "/run/ovn.sock", cfg.SocketFile)
	})
	t.Run("vlan zero omitted", func(t *testing.T) {
		out, err := formatOvsCniConfig("net1", 0, "/run/ovn.sock")
		require.NoError(t, err)
		assert.NotContains(t, out, "vlan")
		var cfg ovsCniConfig
		require.NoError(t, json.Unmarshal([]byte(out), &cfg))
		assert.Nil(t, cfg.Vlan)
	})
}

func TestNadToNfvNetwork(t *testing.T) {
	config, err := formatOvsCniConfig("net1", 100, "/run/ovn.sock")
	require.NoError(t, err)

	newNad := func(managed bool) *netattv1.NetworkAttachmentDefinition {
		labels := map[string]string{}
		if managed {
			labels[common.K8sManagedByLabel] = common.KubeNfvName
		}
		return &netattv1.NetworkAttachmentDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "net1",
				UID:         "uid-1",
				Labels:      labels,
				Annotations: map[string]string{nadResourceNameAnnotation: "intel.com/sriov"},
			},
			Spec: netattv1.NetworkAttachmentDefinitionSpec{Config: config},
		}
	}

	t.Run("valid", func(t *testing.T) {
		got, err := nadToNfvNetwork(newNad(true))
		require.NoError(t, err)
		assert.Equal(t, nfvcommon.NetworkType_NETWORK_TYPE_SRIOV, got.NetworkType)
		assert.Equal(t, "net1", got.GetNetworkResourceName())
		assert.Equal(t, "intel.com/sriov", got.GetProviderNetwork())
		assert.Equal(t, uint64(100), got.GetSegmentationId())
		assert.Equal(t, "uid-1", got.NetworkResourceId.GetValue())
	})
	t.Run("nil", func(t *testing.T) {
		_, err := nadToNfvNetwork(nil)
		assert.Error(t, err)
	})
	t.Run("no uid", func(t *testing.T) {
		nad := newNad(true)
		nad.UID = ""
		_, err := nadToNfvNetwork(nad)
		assert.Error(t, err)
	})
	t.Run("not managed", func(t *testing.T) {
		_, err := nadToNfvNetwork(newNad(false))
		assert.Error(t, err)
	})
}
