package kubevirt

import (
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
	memQ, err := resource.ParseQuantity(fmt.Sprintf("%fM", nfvFlavour.VirtualMemory.VirtualMemSize))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert %fM to the k8s Quantity: %w", nfvFlavour.VirtualMemory.VirtualMemSize, err)
	}
	vmInstTypeSpec.Memory = v1beta1.MemoryInstancetype{
		Guest: memQ,
	}
	return &v1beta1.VirtualMachineInstancetype{
			ObjectMeta: v1.ObjectMeta{
				Name: flavourNameFromId(flavorId),
				Labels: map[string]string{
					common.K8sManagedByLabel:  common.KubeNfvName,
					flavour.K8sFlavourIdLabel: flavorId,
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
	virtualMem := &nfv.VirtualMemoryData{}
	// TODO:

	virtualCpu := &nfv.VirtualCpuData{}
	// TODO:

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
		IsPublic:      &isPublic,
		VirtualMemory: virtualMem,
		VirtualCpu:    virtualCpu,
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
