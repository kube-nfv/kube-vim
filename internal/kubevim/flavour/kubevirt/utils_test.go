package kubevirt

import (
	"testing"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestCheckSupported(t *testing.T) {
	unsupported := map[string]func(f *flavourv1.Flavour){
		"cpu architecture": func(f *flavourv1.Flavour) {
			f.VirtualCpu.CpuArchitecture = commonv1.CpuArchitecture_CPU_ARCHITECTURE_X86_64
		},
		"cpu pinning":      func(f *flavourv1.Flavour) { f.VirtualCpu.VirtualCpuPinning = &flavourv1.VirtualCpuPinningData{} },
		"cpu topology":     func(f *flavourv1.Flavour) { f.VirtualCpu.Topology = &flavourv1.CpuTopology{} },
		"oversubscription": func(f *flavourv1.Flavour) { f.VirtualMemory.VirtualMemOversubscriptionPolicy = "2:1" },
		"numa":             func(f *flavourv1.Flavour) { f.VirtualMemory.NumaEnabled = proto.Bool(false) },
		"hugepages":        func(f *flavourv1.Flavour) { f.VirtualMemory.HugepageSize = "1Gi" },
	}
	for name, set := range unsupported {
		t.Run(name, func(t *testing.T) {
			f := newFlavour()
			set(f)
			assert.ErrorIs(t, checkSupported(f), apperrors.ErrUnsupported)
		})
	}
	assert.NoError(t, checkSupported(newFlavour()))
}

// TestFlavourRoundTrip stores a flavour in kubevirt objects and reads it back unchanged.
func TestFlavourRoundTrip(t *testing.T) {
	in := newFlavour()
	in.FlavourId = "ignored"
	instType, pref, err := kubeVirtObjects("abc", in)
	require.NoError(t, err)
	assert.Equal(t, "flavour-abc", instType.Name)
	assert.Equal(t, "flavour-pref-abc", pref.Name)

	got, err := flavourFromInstancetype(instType)
	require.NoError(t, err)
	assert.Equal(t, "abc", got.FlavourId)
	assert.Equal(t, in.VirtualMemory.VirtualMemSize, got.VirtualMemory.VirtualMemSize)
	assert.Equal(t, in.VirtualCpu.NumVirtualCpu, got.VirtualCpu.NumVirtualCpu)
	assert.Equal(t, "osm", got.Metadata["owner"])
}

func TestKubeVirtObjectsInvalidMemory(t *testing.T) {
	f := newFlavour()
	f.VirtualMemory.VirtualMemSize = "lots"
	_, _, err := kubeVirtObjects("abc", f)
	var target *apperrors.ErrInvalidArgument
	assert.ErrorAs(t, err, &target)
}
