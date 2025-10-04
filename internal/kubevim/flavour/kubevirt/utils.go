package kubevirt

import (
	"encoding/json"
	"fmt"
	"strings"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/api/instancetype/v1beta1"
)

func kubeVirtInstanceTypePreferencesFromNfvFlavour(flavorId string, nfvFlavour *vivnfm.VirtualComputeFlavour) (*v1beta1.VirtualMachineInstancetype, *v1beta1.VirtualMachinePreference, error) {
	if nfvFlavour == nil {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "flavour", Reason: "cannot be empty"}
	}
	if nfvFlavour.VirtualCpu == nil {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "virtual cpu", Reason: "cannot be empty"}
	}
	if nfvFlavour.VirtualMemory == nil {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "virtual memory", Reason: "cannot be empty"}
	}

	if nfvFlavour.VirtualCpu.NumVirtualCpu == 0 {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "virtual CPU count", Reason: "cannot be 0"}
	}
	vmInstTypeSpec := v1beta1.VirtualMachineInstancetypeSpec{}
	// TODO: Add CPUPinning, NUMA, etc.
	vmInstTypeSpec.CPU = v1beta1.CPUInstancetype{
		Guest: nfvFlavour.VirtualCpu.NumVirtualCpu,
	}
	if nfvFlavour.VirtualMemory.VirtualMemSize == 0 {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "virtual memory size", Reason: "cannot be 0"}
	}
	memQ := *resource.NewQuantity(int64(nfvFlavour.VirtualMemory.VirtualMemSize)*1024*1024, resource.BinarySI)

	vmInstTypeSpec.Memory = v1beta1.MemoryInstancetype{
		Guest: memQ,
	}
	// Temporary solution is to store serialized flavour volumes in the VirtualMachineInstancetype resource anno
	volumesJson, err := json.Marshal(nfvFlavour.StorageAttributes)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal storage attributes for flavour '%s': %w", flavorId, err)
	}

	labels := map[string]string{
		common.K8sManagedByLabel:  common.KubeNfvName,
		flavour.K8sFlavourIdLabel: flavorId,
	}
	ann := map[string]string{
		flavour.K8sVolumesAnnotation: string(volumesJson),
	}

	if nfvFlavour.Metadata != nil {
		// Maybe some annotations needs to be present in labels
		for k, v := range nfvFlavour.Metadata.Fields {
			ann[k] = v
		}
	}

	return &v1beta1.VirtualMachineInstancetype{
			ObjectMeta: v1.ObjectMeta{
				Name:        flavourNameFromId(flavorId),
				Labels:      labels,
				Annotations: ann,
			},
			Spec: vmInstTypeSpec,
		}, &v1beta1.VirtualMachinePreference{
			ObjectMeta: v1.ObjectMeta{
				Name:        flavourPreferenceNameFromId(flavorId),
				Labels:      labels,
				Annotations: ann,
			},
		}, nil
}

func nfvFlavourFromKubeVirtInstanceTypePreferences(flavourId string, instType *v1beta1.VirtualMachineInstancetype, pref *v1beta1.VirtualMachinePreference) (*vivnfm.VirtualComputeFlavour, error) {
	if instType == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "VirtualMachineInstancetype", Reason: "cannot be nil"}
	}
	if !misc.IsObjectInstantiated(instType) {
		return nil, &apperrors.ErrK8sObjectNotInstantiated{ObjectType: "VirtualMachineInstancetype"}
	}
	if pref != nil && !misc.IsObjectInstantiated(pref) {
		return nil, &apperrors.ErrK8sObjectNotInstantiated{ObjectType: "VirtualMachinePreference"}
	}
	if !misc.IsObjectManagedByKubeNfv(instType) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachineInstancetype", ObjectName: instType.GetName(), ObjectId: string(instType.GetUID())}
	}
	if pref != nil && !misc.IsObjectManagedByKubeNfv(pref) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachinePreference", ObjectName: pref.GetName(), ObjectId: string(pref.GetUID())}
	}
	virtualMem := &vivnfm.VirtualMemoryData{
		// Translate memory to the MiB
		VirtualMemSize: float32(instType.Spec.Memory.Guest.Value()) / (1024 * 1024),
	}

	virtualCpu := &vivnfm.VirtualCpuData{
		NumVirtualCpu: instType.Spec.CPU.Guest,
	}

	var storageAttributes []*vivnfm.VirtualStorageData
	if val, ok := instType.Annotations[flavour.K8sVolumesAnnotation]; ok {
		if err := json.Unmarshal([]byte(val), &storageAttributes); err != nil {
			return nil, fmt.Errorf("unmarshal storage attributes from instancetype '%s' (id: %s): %w", instType.Name, instType.GetUID(), err)
		}
	} else {
		return nil, fmt.Errorf("VirtualMachineInstancetype '%s' (id: %s) missing storage attributes annotation", instType.Name, instType.GetUID())
	}

	isPublic := false
	metadata := map[string]string{
		kubevirtv1.InstancetypeAnnotation: instType.GetName(),
		KubevirtInstanceTypeIdAnnotation:  string(instType.GetUID()),
		flavour.K8sFlavourSourceLabel:     KubevirtFlavourSource,
	}
	if pref != nil {
		metadata[kubevirtv1.PreferenceAnnotation] = pref.GetName()
		metadata[KubevirtPreferenceIdAnnotation] = string(pref.GetUID())
	}
	if val, ok := instType.Annotations[flavour.K8sFlavourAttNameAnnotation]; ok {
		metadata[flavour.K8sFlavourAttNameAnnotation] = val
	}

	return &vivnfm.VirtualComputeFlavour{
		FlavourId: &nfvcommon.Identifier{
			Value: flavourId,
		},
		IsPublic:          &isPublic,
		VirtualMemory:     virtualMem,
		VirtualCpu:        virtualCpu,
		StorageAttributes: storageAttributes,
		Metadata: &nfvcommon.Metadata{
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
		return "", &apperrors.ErrInvalidArgument{Field: "flavour name", Reason: fmt.Sprintf("invalid format: %s", flavourName)}
	}
	id := strings.TrimPrefix(flavourName, prefix)
	if id == "" {
		return "", &apperrors.ErrInvalidArgument{Field: "flavour id", Reason: "empty id in name"}
	}
	return id, nil
}
