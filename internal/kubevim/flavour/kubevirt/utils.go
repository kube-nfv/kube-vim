package kubevirt

import (
	"fmt"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/api/instancetype/v1beta1"
)

// checkSupported rejects flavour fields kube-vim does not apply yet.
func checkSupported(f *flavourv1.Flavour) error {
	cpu, mem := f.GetVirtualCpu(), f.GetVirtualMemory()
	unsupported := ""
	switch {
	case cpu.GetCpuArchitecture() != commonv1.CpuArchitecture_CPU_ARCHITECTURE_UNSPECIFIED:
		unsupported = "virtualCpu.cpuArchitecture"
	case cpu.GetVirtualCpuPinning() != nil:
		unsupported = "virtualCpu.virtualCpuPinning"
	case cpu.GetTopology() != nil:
		unsupported = "virtualCpu.topology"
	case mem.GetVirtualMemOversubscriptionPolicy() != "":
		unsupported = "virtualMemory.virtualMemOversubscriptionPolicy"
	case mem.NumaEnabled != nil:
		unsupported = "virtualMemory.numaEnabled"
	case mem.GetHugepageSize() != "":
		unsupported = "virtualMemory.hugepageSize"
	}
	if unsupported != "" {
		return fmt.Errorf("%s: %w", unsupported, apperrors.ErrUnsupported)
	}
	return nil
}

// kubeVirtObjects builds the instancetype and preference backing flavour id; the instancetype carries f.
func kubeVirtObjects(id string, f *flavourv1.Flavour) (*v1beta1.VirtualMachineInstancetype, *v1beta1.VirtualMachinePreference, error) {
	mem, err := resource.ParseQuantity(f.GetVirtualMemory().GetVirtualMemSize())
	if err != nil {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "virtualMemory.virtualMemSize", Reason: err.Error()}
	}
	stored := proto.CloneOf(f)
	stored.FlavourId = ""
	raw, err := protojson.Marshal(stored)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal flavour '%s': %w", id, err)
	}
	labels := map[string]string{
		common.K8sManagedByLabel:  common.KubeNfvName,
		flavour.K8sFlavourIdLabel: id,
	}
	instType := &v1beta1.VirtualMachineInstancetype{
		ObjectMeta: v1.ObjectMeta{
			Name:        flavourNameFromId(id),
			Labels:      labels,
			Annotations: map[string]string{flavour.K8sFlavourAnnotation: string(raw)},
		},
		Spec: v1beta1.VirtualMachineInstancetypeSpec{
			CPU:    v1beta1.CPUInstancetype{Guest: f.GetVirtualCpu().GetNumVirtualCpu()},
			Memory: v1beta1.MemoryInstancetype{Guest: mem},
		},
	}
	pref := &v1beta1.VirtualMachinePreference{
		ObjectMeta: v1.ObjectMeta{
			Name:   flavourPreferenceNameFromId(id),
			Labels: labels,
		},
	}
	return instType, pref, nil
}

// flavourFromInstancetype returns the flavour instType carries, with its id and kube-vim metadata keys.
func flavourFromInstancetype(instType *v1beta1.VirtualMachineInstancetype) (*flavourv1.Flavour, error) {
	raw, ok := instType.Annotations[flavour.K8sFlavourAnnotation]
	if !ok {
		return nil, fmt.Errorf("VirtualMachineInstancetype '%s' has no '%s' annotation: %w", instType.Name, flavour.K8sFlavourAnnotation, apperrors.ErrInternal)
	}
	f := &flavourv1.Flavour{}
	if err := protojson.Unmarshal([]byte(raw), f); err != nil {
		return nil, fmt.Errorf("unmarshal flavour of VirtualMachineInstancetype '%s': %w", instType.Name, err)
	}
	f.FlavourId = instType.Labels[flavour.K8sFlavourIdLabel]
	if f.Metadata == nil {
		f.Metadata = map[string]string{}
	}
	f.Metadata[flavour.InstancetypeMetadataKey] = instType.Name
	f.Metadata[flavour.PreferenceMetadataKey] = flavourPreferenceNameFromId(f.FlavourId)
	return f, nil
}

func flavourNameFromId(id string) string {
	return fmt.Sprintf("flavour-%s", id)
}

func flavourPreferenceNameFromId(id string) string {
	return fmt.Sprintf("flavour-pref-%s", id)
}
