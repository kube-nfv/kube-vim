package cdi

import (
	"context"
	"fmt"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/misc"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	cdi "kubevirt.io/client-go/containerizeddataimporter"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	CDIVolumeImportSourceKind = "VolumeImportSource"

	K8sDataVolumeIdLabel = "cdi.image.kubevim.kubenfv.io/data-volume-id"
)

var (
	// 10 Gib Image size
	defaultImageSize = resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)
)

type cdiManager struct {
	admin.UnimplementedAdminServer

	cdiClient *cdi.Clientset
	k8sClient *kubernetes.Clientset
	cfg       *config.ImageConfig
}

func NewCDIImageManager(k8sConfig *rest.Config, cfg *config.ImageConfig) (*cdiManager, error) {
	if cfg.StorageClass == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config image.StorageClass", Reason: "can't be empty"}
	}
	cdiClient, err := cdi.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubevirt CDI k8s client: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return &cdiManager{
		cdiClient: cdiClient,
		k8sClient: k8sClient,
		cfg:       cfg,
	}, nil
}

func (m *cdiManager) GetImage(ctx context.Context, id *nfvcommon.Identifier) (*vivnfm.SoftwareImageInformation, error) {

	return nil, nil
}

func (m *cdiManager) ListImages(ctx context.Context) ([]*vivnfm.SoftwareImageInformation, error) {
	return nil, nil
}

func (m *cdiManager) DownloadImage(ctx context.Context, req *admin.DownloadImageRequest) (*admin.DownloadImageResponse, error) {
	if req == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "request", Reason: "can't be nil"}
	}
	imgName := req.Metadata.GetName()
	importSourceType, err := importSourceTypeFromImageSource(req.Source)
	if err != nil {
		return nil, fmt.Errorf("get CDI ImportSourceType from imageSource: %w", err)
	}
	volumeImportSource := &v1beta1.VolumeImportSource{
		ObjectMeta: v1.ObjectMeta{
			Name: imgName,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
				image.K8sIsUploadLabel:   "false",
			},
		},
		Spec: v1beta1.VolumeImportSourceSpec{
			Source: importSourceType,
		},
	}
	visInst, err := m.cdiClient.CdiV1beta1().VolumeImportSources(common.KubeNfvDefaultNamespace).Create(ctx, volumeImportSource, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create CDI VolumeImportSource: %w", err)
	}
	cleanupVolumeImportSource := func() error {
		return m.cdiClient.CdiV1beta1().VolumeImportSources(common.KubeNfvDefaultNamespace).Delete(ctx, imgName, v1.DeleteOptions{})
	}
	imageId := misc.UIDToIdentifier(visInst.GetUID())

	// Return non-instantiated image if LazyDownload option presents
	if req.Options == nil || (req.Options.LazyDownload != nil && *req.Options.LazyDownload == true) {
		return &admin.DownloadImageResponse{
			ImageId: imageId,
		}, nil
	}
	// Create DataVolume from VolumeImportSource
	storageClassName := *m.cfg.StorageClass
	if req.Options.StorageClass != nil && *req.Options.StorageClass != "" {
		storageClassName = *req.Options.StorageClass
	}
	storageClass, err := getStorageClass(ctx, storageClassName, m.k8sClient)
	if err != nil {
		return nil, fmt.Errorf("get storageClass: %w", err)
	}
	dvAnnotations := make(map[string]string)
	if storageClass.VolumeBindingMode != nil && *storageClass.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer {
		dvAnnotations["cdi.kubevirt.io/storage.bind.immediate.requested"] = "true"
	}

	imageSize := defaultImageSize
	// TODO: Add ImageSize pre-population.
	if req.Options.StorageSize != nil {
		reqSize, err := resource.ParseQuantity(*req.Options.StorageSize)
		if err == nil {
			imageSize = &reqSize
		}
	}

	dataVolume := v1beta1.DataVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: imgName,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
				image.K8sImageIdLabel:    string(visInst.GetUID()),
			},
			Annotations: dvAnnotations,
		},
		Spec: v1beta1.DataVolumeSpec{
			Storage: &v1beta1.StorageSpec{
				DataSourceRef: &corev1.TypedObjectReference{
					APIGroup: &v1beta1.CDIGroupVersionKind.Group,
					Kind:     CDIVolumeImportSourceKind,
					Name:     imgName,
				},
				AccessModes: []corev1.PersistentVolumeAccessMode{
					// TODO: Temporary solution to make it works with ReadWriteOnce sc.
					// Need to make it ReadOnlyMany since it is golden volume.
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *imageSize,
					},
				},
			},
		},
	}
	dvInst, err := m.cdiClient.CdiV1beta1().DataVolumes(common.KubeNfvDefaultNamespace).Create(ctx, &dataVolume, v1.CreateOptions{})
	if err != nil {
		cleanupVolumeImportSource()
		return nil, fmt.Errorf("create CDI DataVolume for image '%s': %w", imgName, err)
	}
	cleanupDataVolume := func() error {
		cleanupVolumeImportSource()
		return m.cdiClient.CdiV1beta1().DataVolumes(common.KubeNfvDefaultNamespace).Delete(ctx, imgName, v1.DeleteOptions{})
	}

	// Update label of volumeImportSource with dv instanceId
	visInst.ObjectMeta.Labels[K8sDataVolumeIdLabel] = string(dvInst.GetUID())
	if _, err = m.cdiClient.CdiV1beta1().VolumeImportSources(common.KubeNfvDefaultNamespace).Update(ctx, visInst, v1.UpdateOptions{}); err != nil {
		cleanupDataVolume()
		return nil, fmt.Errorf("update CDI VolumeImportSource label for image '%s': %w", imgName, err)
	}

	return &admin.DownloadImageResponse{
		ImageId: imageId,
	}, nil
}

func (m *cdiManager) GetImageDownloadStatus(ctx context.Context, req *admin.GetImageDownloadStatusRequest) (*admin.GetImageDownloadStatusResponse, error) {
	if req == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "request", Reason: "can't be nil"}
	}
	return nil, nil
}

func (m *cdiManager) SetupImageUploadProxy(ctx context.Context, req *admin.SetupImageUploadProxyRequest) (*admin.SetupImageUploadProxyResponse, error) {
	return nil, nil
}
