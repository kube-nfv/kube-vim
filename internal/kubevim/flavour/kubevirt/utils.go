package kubevirt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/api/instancetype/v1beta1"
)

func kubeVirtInstanceTypePreferencesFromNfvFlavour(flavorId string, nfvFlavour *nfv.VirtualComputeFlavour) (*v1beta1.VirtualMachineInstancetype, *v1beta1.VirtualMachinePreference, error) {
	if nfvFlavour == nil {
		return nil, nil, fmt.Errorf("flavour can't be empty")
	}
	if nfvFlavour.VirtualCpu == nil {
		return nil, nil, fmt.Errorf("virtual cpu can't be empty")
	}
	if nfvFlavour.VirtualMemory == nil {
		return nil, nil, fmt.Errorf("virtual memory can't be empty")
	}

	if nfvFlavour.VirtualCpu.NumVirtualCpu == 0 {
		return nil, nil, fmt.Errorf("virtualCpu.NumVirtualCpu can't be 0")
	}
	vmInstTypeSpec := v1beta1.VirtualMachineInstancetypeSpec{}
	// TODO: Add CPUPinning, NUMA, etc.
	vmInstTypeSpec.CPU = v1beta1.CPUInstancetype{
		Guest: nfvFlavour.VirtualCpu.NumVirtualCpu,
	}
	if nfvFlavour.VirtualMemory.VirtualMemSize == 0 {
		return nil, nil, fmt.Errorf("virtual memory size can't be 0")
	}
	memQ := *resource.NewQuantity(int64(nfvFlavour.VirtualMemory.VirtualMemSize)*1024*1024, resource.BinarySI)

	vmInstTypeSpec.Memory = v1beta1.MemoryInstancetype{
		Guest: memQ,
	}
	// Temporary solution is to store serialized flavour volumes in the VirtualMachineInstancetype resource anno
	volumesJson, err := json.Marshal(nfvFlavour.StorageAttributes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal flavour storage attributes: %w", err)
	}
	return &v1beta1.VirtualMachineInstancetype{
			ObjectMeta: v1.ObjectMeta{
				Name: flavourNameFromId(flavorId),
				Labels: map[string]string{
					common.K8sManagedByLabel:  common.KubeNfvName,
					flavour.K8sFlavourIdLabel: flavorId,
				},
				Annotations: map[string]string{
					flavour.K8sVolumesAnnotation: string(volumesJson),
				},
			},
			Spec: vmInstTypeSpec,
		}, &v1beta1.VirtualMachinePreference{
			ObjectMeta: v1.ObjectMeta{
				Name: flavourPreferenceNameFromId(flavorId),
				Labels: map[string]string{
					common.K8sManagedByLabel:  common.KubeNfvName,
					flavour.K8sFlavourIdLabel: flavorId,
				},
			},
		}, nil
}

func nfvFlavourFromKubeVirtInstanceTypePreferences(flavourId string, instType *v1beta1.VirtualMachineInstancetype, pref *v1beta1.VirtualMachinePreference) (*nfv.VirtualComputeFlavour, error) {
	if instType == nil || pref == nil {
		return nil, fmt.Errorf("VirtualMachineInstancetype or VirtualMachinePreference can't be nil")
	}
	if !misc.IsObjectInstantiated(instType) || !misc.IsObjectInstantiated(pref) {
		return nil, fmt.Errorf("virtualmachineinstancetype or virtualmachinepreference is not from Kubernetes (likely created manually)")
	}
	if !misc.IsObjectManagedByKubeNfv(instType) || !misc.IsObjectManagedByKubeNfv(pref) {
		return nil, fmt.Errorf("virtualmachineinstancetype \"%s\" with uid \"%s\" or virtualmachinepreference \"%s\" with uid \"%s\" is not managed by the kube-nfv", instType.GetName(), instType.GetUID(), pref.GetName(), pref.GetUID())
	}
	virtualMem := &nfv.VirtualMemoryData{
		// Translate memory to the MiB
		VirtualMemSize: float32(instType.Spec.Memory.Guest.Value()) / (1024 * 1024),
	}

	virtualCpu := &nfv.VirtualCpuData{
		NumVirtualCpu: instType.Spec.CPU.Guest,
	}

	var storageAttributes []*nfv.VirtualStorageData
	if val, ok := instType.Annotations[flavour.K8sVolumesAnnotation]; ok {
		if err := json.Unmarshal([]byte(val), &storageAttributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal volumes from the virtualmachineinstancetype \"%s\" annotation \"%s\": %w", instType.Name, flavour.K8sVolumesAnnotation, err)
		}
	} else {
		return nil, fmt.Errorf("kubevirt virtualmachineinstancetype with name \"%s\" missing \"%s\" annotation", instType.Name, flavour.K8sVolumesAnnotation)
	}

	isPublic := false
	metadata := map[string]string{
		kubevirtv1.InstancetypeAnnotation: instType.GetName(),
		KubevirtInstanceTypeIdAnnotation:  string(instType.GetUID()),
		kubevirtv1.PreferenceAnnotation:   pref.GetName(),
		KubevirtPreferenceIdAnnotation:    string(pref.GetUID()),
		flavour.K8sFlavourSourceLabel:     KubevirtFlavourSource,
	}
	return &nfv.VirtualComputeFlavour{
		FlavourId: &nfv.Identifier{
			Value: flavourId,
		},
		IsPublic:          &isPublic,
		VirtualMemory:     virtualMem,
		VirtualCpu:        virtualCpu,
		StorageAttributes: storageAttributes,
		Metadata: &nfv.Metadata{
			Fields: metadata,
		},
	}, nil
}

func flavourNameFromId(id string) string {
	return fmt.Sprintf("flavour-%s", id)
}

func flavourPreferenceNameFromId(id string) string {
	return fmt.Sprintf("flavour-pref-%s", id)
}

func idFromFlavourName(flavourName string) (string, error) {
	const prefix = "flavour-"
	if !strings.HasPrefix(flavourName, prefix) {
		return "", fmt.Errorf("invalid flavour name \"%s\" format", flavourName)
	}
	id := strings.TrimPrefix(flavourName, prefix)
	if id == "" {
		return "", fmt.Errorf("empty id for flavour name \"%s\"", flavourName)
	}
	return id, nil
}
