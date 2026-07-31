package kubevirt

import (
	"testing"
	"time"

	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestFlavourNameIdRoundTrip(t *testing.T) {
	name := flavourNameFromId("abc")
	assert.Equal(t, "flavour-abc", name)
	assert.Equal(t, "flavour-pref-abc", flavourPreferenceNameFromId("abc"))

	id, err := idFromFlavourName(name)
	require.NoError(t, err)
	assert.Equal(t, "abc", id)
}

func TestIdFromFlavourNameErrors(t *testing.T) {
	_, err := idFromFlavourName("badprefix-abc")
	assert.Error(t, err)

	_, err = idFromFlavourName("flavour-")
	assert.Error(t, err)
}

func newNfvFlavour() *vivnfm.VirtualComputeFlavour {
	mem := resource.MustParse("2Gi")
	return &vivnfm.VirtualComputeFlavour{
		VirtualCpu:    &vivnfm.VirtualCpuData{NumVirtualCpu: 4},
		VirtualMemory: &vivnfm.VirtualMemoryData{VirtualMemSize: &mem},
	}
}

func TestKubeVirtInstanceTypePreferencesFromNfvFlavour(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		instType, pref, err := kubeVirtInstanceTypePreferencesFromNfvFlavour("abc", newNfvFlavour())
		require.NoError(t, err)
		assert.Equal(t, "flavour-abc", instType.Name)
		assert.Equal(t, "flavour-pref-abc", pref.Name)
		assert.Equal(t, uint32(4), instType.Spec.CPU.Guest)
		assert.Equal(t, "2Gi", instType.Spec.Memory.Guest.String())
		assert.Equal(t, common.KubeNfvName, instType.Labels[common.K8sManagedByLabel])
	})

	errCases := map[string]*vivnfm.VirtualComputeFlavour{
		"nil flavour": nil,
		"nil cpu":     {VirtualMemory: &vivnfm.VirtualMemoryData{}},
		"nil memory":  {VirtualCpu: &vivnfm.VirtualCpuData{NumVirtualCpu: 1}},
		"zero cpu":    {VirtualCpu: &vivnfm.VirtualCpuData{NumVirtualCpu: 0}, VirtualMemory: &vivnfm.VirtualMemoryData{}},
	}
	for name, flavour := range errCases {
		t.Run(name, func(t *testing.T) {
			_, _, err := kubeVirtInstanceTypePreferencesFromNfvFlavour("abc", flavour)
			assert.Error(t, err)
		})
	}
}

// TestFlavourRoundTrip converts an nfv flavour to kubevirt objects and back,
// asserting the essential attributes survive the round trip.
func TestFlavourRoundTrip(t *testing.T) {
	instType, pref, err := kubeVirtInstanceTypePreferencesFromNfvFlavour("abc", newNfvFlavour())
	require.NoError(t, err)

	// nfvFlavourFromKubeVirtInstanceTypePreferences requires instantiated objects.
	instantiate := func(m *metav1.ObjectMeta) {
		m.UID = types.UID("uid-" + m.Name)
		m.ResourceVersion = "1"
		m.CreationTimestamp = metav1.NewTime(time.Now())
	}
	instantiate(&instType.ObjectMeta)
	instantiate(&pref.ObjectMeta)

	got, err := nfvFlavourFromKubeVirtInstanceTypePreferences("abc", instType, pref)
	require.NoError(t, err)
	assert.Equal(t, "abc", got.FlavourId.GetValue())
	assert.Equal(t, uint32(4), got.VirtualCpu.NumVirtualCpu)
	require.NotNil(t, got.VirtualMemory.VirtualMemSize)
	assert.Equal(t, "2Gi", got.VirtualMemory.VirtualMemSize.String())
}

func TestNfvFlavourFromKubeVirtErrors(t *testing.T) {
	t.Run("nil instancetype", func(t *testing.T) {
		_, err := nfvFlavourFromKubeVirtInstanceTypePreferences("abc", nil, nil)
		assert.Error(t, err)
	})
	t.Run("not instantiated", func(t *testing.T) {
		instType, _, err := kubeVirtInstanceTypePreferencesFromNfvFlavour("abc", newNfvFlavour())
		require.NoError(t, err)
		_, err = nfvFlavourFromKubeVirtInstanceTypePreferences("abc", instType, nil)
		assert.Error(t, err)
	})
}
