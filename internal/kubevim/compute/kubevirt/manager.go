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
	kubeVirtMetaIf, ok := flavourMeta[kubevirt_flavour.KubeVirtFlavourMetadataKeyName]
	if !ok {
		return nil, fmt.Errorf("kubevirt compute manager can only works with kubevirt flavour manager: %w", config.UnsupportedErr)
	}
	// TODO(dmalovan): Add the ability to works with different flavours providers/managers (eg. get flavours directly from the nova)
	_, ok = kubeVirtMetaIf.(*kubevirt_flavour.KubeVirtFlavourMetadata)
	if !ok {
		return nil, fmt.Errorf("failed to convert kubevirt flavour metadata. Invaid object type")
	}

	// Get Request related image and place it
	if req.VcImageId == nil || req.VcImageId.GetValue() == "" {
		return nil, fmt.Errorf("vcImageId can't be empty")
	}
    _, err = m.imageManager.GetImage(ctx, req.GetVcImageId())
    if err != nil {
        return nil, fmt.Errorf("failed to get image with id \"%s\": %w", req.GetVcImageId(), err)
    }
	return nil, nil
}


func initImageDataVolume(imageInfo *nfv.SoftwareImageInformation) (*kubevirtv1.DataVolumeTemplateSpec, error) {
    if imageInfo == nil {
        return nil, fmt.Errorf("nfv software image info can't be empty")
    }
    if imageInfo.Name == "" {
        return nil, fmt.Errorf("nfv software image info name can't be empty")
    }
    if imageInfo.Size == nil || imageInfo.GetSize().Equal(*resource.NewQuantity(0, resource.BinarySI)) {
        return nil, fmt.Errorf("nfv software image size can't be 0")
    }

    return &kubevirtv1.DataVolumeTemplateSpec{
        ObjectMeta: v1.ObjectMeta{
            Name: imageInfo.Name + "-dv",
            Labels: map[string]string{
                config.K8sManagedByLabel: config.KubeNfvName,
            },
        },
        Spec: v1beta1.DataVolumeSpec{
            PVC: &corev1.PersistentVolumeClaimSpec{
                DataSourceRef: &corev1.TypedObjectReference{
                    APIGroup: &v1beta1.CDIGroupVersionKind.Group,
                    Kind: v1beta1.VolumeImportSourceRef,
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

