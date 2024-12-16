package kubevirt

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/DiMalovanyy/kube-vim/internal/kubevim/flavour/kubevirt"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	kubevirtv1 "kubevirt.io/api/core/v1"
	kubevirt "kubevirt.io/client-go/generated/kubevirt/clientset/versioned"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
    // TODO(dmalovan): find a way to get the Kind from not initialized object. 
    // See: k8s.io/apimachinery/pkg/runtime/scheme.go:AddKnowTypes
    KubevirtVolumeImportSourceKind = "VolumeImportSource"
    KubevirtVirtualMachineInstanceTypeKind = "VirtualMachineInstanceType"
    KubevirtVirtualMachinePreferenceKind  = "VirtualMachinePreference"
)

// kubevirt manager for allocation and management of the compute resources.
type manager struct {
	kubevirtClient *kubevirt.Clientset
	flavourManager flavour.Manager
	imageManager   image.Manager
	networkManager network.Manager

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewComputeManager(
	k8sConfig *rest.Config,
	cfg *config.K8sConfig,
	flavourManager flavour.Manager,
	imageManager image.Manager,
	networkManager network.Manager) (*manager, error) {
	c, err := kubevirt.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-virt k8s client: %w", err)
	}
	return &manager{
		kubevirtClient: c,
		flavourManager: flavourManager,
		imageManager:   imageManager,
		networkManager: networkManager,
		cfg:            cfg,
	}, nil
}

func (m *manager) AllocateComputeResource(ctx context.Context, req *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error) {
	if req == nil {
		return nil, fmt.Errorf("request can't be empty")
	}

	// Get request related compute flavour
	if req.ComputeFlavourId == nil || req.ComputeFlavourId.GetValue() == "" {
		return nil, fmt.Errorf("computeFlavourId can't be empty")
	}
	_, flavourMeta, err := m.flavourManager.GetFlavour(ctx, req.ComputeFlavourId)
	if err != nil {
		return nil, fmt.Errorf("failed to retrive flavour with id \"%s\": %w", req.ComputeFlavourId.GetValue(), err)
	}
	if flavourMeta == nil {
		return nil, fmt.Errorf("flavour metadata can't be nil: %w", config.UnsupportedErr)
	}
	kubeVirtFlavourMetaIf, ok := flavourMeta[kubevirt_flavour.KubeVirtFlavourMetadataKeyName]
	if !ok {
		return nil, fmt.Errorf("kubevirt compute manager can only works with kubevirt flavour manager: %w", config.UnsupportedErr)
	}
	// TODO(dmalovan): Add the ability to works with different flavours providers/managers (eg. get flavours directly from the openstack nova)
    kubeVirtFlavourMeta, ok := kubeVirtFlavourMetaIf.(*kubevirt_flavour.KubeVirtFlavourMetadata)
	if !ok {
        return nil, fmt.Errorf("failed to convert kubevirt flavour metadata. Invaid object type: %w", config.UnsupportedErr)
	}
    instanceTypeMatcher, err := initVmInstanceTypeMatcher(kubeVirtFlavourMeta.VirtualMachineInstanceTypeName)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize kubevirt instance type matcher \"%s\": %w", kubeVirtFlavourMeta.VirtualMachineInstanceTypeName, err)
    }
    // Note(dmalovan): preference matcher can be nil if some errors are returned. (eg. missed preference name in meta)
    preferenceMatcher, _ := initVmPreferenceMatcher(kubeVirtFlavourMeta.VirtualMachinePreferenceName)

	// Get the Request related image and place it
	if req.VcImageId == nil || req.VcImageId.GetValue() == "" {
		return nil, fmt.Errorf("vcImageId can't be empty")
	}
    imgInfo, err := m.imageManager.GetImage(ctx, req.GetVcImageId())
    if err != nil {
        return nil, fmt.Errorf("failed to get image with id \"%s\": %w", req.GetVcImageId(), err)
    }
    dv, err := initImageDataVolume(imgInfo)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize kubevirt data volume: %w", err)
    }

	return nil, nil
}


func initVmInstanceTypeMatcher(instanceTypeName string) (*kubevirtv1.InstancetypeMatcher, error) {
    if instanceTypeName == "" {
        return nil, fmt.Errorf("instanceType name can't be empty")
    }
    return &kubevirtv1.InstancetypeMatcher{
        Kind: KubevirtVirtualMachineInstanceTypeKind,
        Name: instanceTypeName,
    }, nil
}

func initVmPreferenceMatcher(preferenceName string) (*kubevirtv1.PreferenceMatcher, error) {
    if preferenceName == "" {
        return nil, fmt.Errorf("preference name can't be empty")
    }
    return &kubevirtv1.PreferenceMatcher{
        Kind: KubevirtVirtualMachinePreferenceKind,
        Name: preferenceName,
    }, nil
}

func initImageDataVolume(imageInfo *nfv.SoftwareImageInformation) (*kubevirtv1.DataVolumeTemplateSpec, error) {
    if imageInfo == nil {
        return nil, fmt.Errorf("nfv software image info can't be empty")
    }
    if imageInfo.Name == "" {
        return nil, fmt.Errorf("nfv software image info name can't be empty")
    }
    // TODO(dmalovan): If more than one VM going to be created from the same Volume Import Source it might leads
    // to the dv naming conflicts. Make name depends on image name (probably hash or uid)
    dvName := imageInfo.Name + "-dv"
    if imageInfo.Size == nil || imageInfo.GetSize().Equal(*resource.NewQuantity(0, resource.BinarySI)) {
        return nil, fmt.Errorf("nfv software image size can't be 0")
    }

    return &kubevirtv1.DataVolumeTemplateSpec{
        ObjectMeta: v1.ObjectMeta{
            Name: dvName,
            Labels: map[string]string{
                config.K8sManagedByLabel: config.KubeNfvName,
            },
        },
        Spec: v1beta1.DataVolumeSpec{
            PVC: &corev1.PersistentVolumeClaimSpec{
                DataSourceRef: &corev1.TypedObjectReference{
                    APIGroup: &v1beta1.CDIGroupVersionKind.Group,
                    Kind: KubevirtVolumeImportSourceKind,
                    Name: imageInfo.Name,
                },
                AccessModes: []corev1.PersistentVolumeAccessMode{
                    // TODO: Temporary solution to make only readWriteOnce data volumes.
                    // Rewrite this with identifying accessmode from the storage class
                    corev1.ReadWriteOnce,
                },
                Resources: corev1.VolumeResourceRequirements{
                    Requests: corev1.ResourceList{
                        corev1.ResourceStorage: *imageInfo.GetSize(),
                    },
                },
            },
        },
    }, nil
}

func initVirtualMachineInstance(name string) (*kubevirtv1.VirtualMachineInstanceTemplateSpec, error) {

    return nil, nil
}
