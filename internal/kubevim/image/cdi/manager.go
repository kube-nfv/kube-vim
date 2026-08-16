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
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CDIVolumeImportSourceKind = "VolumeImportSource"

	K8sDataVolumeIdLabel = "cdi.image.kubevim.kubenfv.io/data-volume-id"
	K8sDataVolumePhase   = "cdi.image.kubevim.kubenfv.io/data-volume-phase"
)

var (
	defaultImageSize = resource.MustParse("10Gi")
)

type cdiManager struct {
	admin.UnimplementedAdminServer

	// client serves cache-backed reads and direct writes.
	client client.Client
	// apiReader is uncached; used for cluster-scoped/unowned reads (StorageClass).
	apiReader client.Reader
	cfg       *config.ImageConfig
	k8sCfg    *config.K8sConfig
}

func NewCDIImageManager(cl client.Client, apiReader client.Reader, cfg *config.ImageConfig, k8sCfg *config.K8sConfig) (*cdiManager, error) {
	if cfg.StorageClass == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config image.StorageClass", Reason: "can't be empty"}
	}
	if k8sCfg.Namespace == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "config k8s.Namespace", Reason: "can't be nil"}
	}
	return &cdiManager{
		client:    cl,
		apiReader: apiReader,
		cfg:       cfg,
		k8sCfg:    k8sCfg,
	}, nil
}

func (m *cdiManager) GetImage(ctx context.Context, id *nfvcommon.Identifier) (*vivnfm.SoftwareImageInformation, error) {
	if id == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "id", Reason: "can't be nil"}
	}
	ns := client.InNamespace(*m.k8sCfg.Namespace)
	managed := client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}
	visList := &v1beta1.VolumeImportSourceList{}
	if err := m.client.List(ctx, visList, ns, managed); err != nil {
		return nil, fmt.Errorf("list CDI VolumeImportSources: %w", err)
	}
	var imageVis *v1beta1.VolumeImportSource
	for idx := range visList.Items {
		if misc.IdentifierToUID(id) == visList.Items[idx].GetUID() {
			imageVis = &visList.Items[idx]
			break
		}
	}
	if imageVis == nil {
		return nil, &apperrors.ErrNotFound{Entity: "software image", Identifier: id.Value}
	}
	imgName := imageVis.Name
	dv := &v1beta1.DataVolume{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: *m.k8sCfg.Namespace, Name: imgName}, dv); err != nil {
		return nil, fmt.Errorf("get CDI DataVolume from image '%s' (id: %s): %w", imgName, id.Value, err)
	}

	nfvImg, err := nfvImageFromCdiDataVolumeVis(dv, imageVis)
	if err != nil {
		return nil, fmt.Errorf("convert CDI DataVolume and VolumeImportSource to NFV SoftwareImageInformation from image '%s' (id: %s): %w", imgName, id.Value, err)
	}
	return nfvImg, nil
}

func (m *cdiManager) ListImages(ctx context.Context) ([]*vivnfm.SoftwareImageInformation, error) {
	ns := client.InNamespace(*m.k8sCfg.Namespace)
	managed := client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}
	visList := &v1beta1.VolumeImportSourceList{}
	if err := m.client.List(ctx, visList, ns, managed); err != nil {
		return nil, fmt.Errorf("list CDI VolumeImportSources: %w", err)
	}
	dvList := &v1beta1.DataVolumeList{}
	if err := m.client.List(ctx, dvList, ns, managed); err != nil {
		return nil, fmt.Errorf("list CDI DataVolumes: %w", err)
	}
	dataVolumesIdx := make(map[string]*v1beta1.DataVolume)
	for idx := range dvList.Items {
		dvRef := &dvList.Items[idx]
		dataVolumesIdx[dvRef.Name] = dvRef
	}
	res := make([]*vivnfm.SoftwareImageInformation, 0, len(visList.Items))
	for idx := range visList.Items {
		img := &visList.Items[idx]
		imgDv, ok := dataVolumesIdx[img.Name]
		if !ok {
			continue
		}
		nfvImg, err := nfvImageFromCdiDataVolumeVis(imgDv, img)
		if err != nil {
			continue
		}
		res = append(res, nfvImg)
	}
	return res, nil
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
	volumeImportSource.Namespace = *m.k8sCfg.Namespace
	if err := m.client.Create(ctx, volumeImportSource); err != nil {
		return nil, fmt.Errorf("create CDI VolumeImportSource: %w", err)
	}
	// Cleanup must run even when the request ctx has been canceled (that is often
	// why we are rolling back), so it uses a ctx detached from cancellation.
	cleanupCtx := context.WithoutCancel(ctx)
	cleanupVolumeImportSource := func() error {
		return m.client.Delete(cleanupCtx, &v1beta1.VolumeImportSource{
			ObjectMeta: v1.ObjectMeta{Name: imgName, Namespace: *m.k8sCfg.Namespace},
		})
	}
	imageId := misc.UIDToIdentifier(volumeImportSource.GetUID())

	// Return non-instantiated image if LazyDownload option presents
	if req.Options != nil && (req.Options.LazyDownload != nil && *req.Options.LazyDownload == true) {
		return &admin.DownloadImageResponse{
			ImageId: imageId,
		}, nil
	}
	// Create DataVolume from VolumeImportSource
	storageClassName := *m.cfg.StorageClass
	if req.Options != nil && (req.Options.StorageClass != nil && *req.Options.StorageClass != "") {
		storageClassName = *req.Options.StorageClass
	}
	storageClass, err := getStorageClass(ctx, storageClassName, m.apiReader)
	if err != nil {
		cleanupVolumeImportSource()
		return nil, fmt.Errorf("get storageClass: %w", err)
	}
	dvAnnotations := make(map[string]string)
	if storageClass.VolumeBindingMode != nil && *storageClass.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer {
		dvAnnotations["cdi.kubevirt.io/storage.bind.immediate.requested"] = "true"
	}

	imageSize := defaultImageSize
	// TODO: Add ImageSize pre-population.
	if req.Options != nil && req.Options.StorageSize != nil {
		reqSize, err := resource.ParseQuantity(*req.Options.StorageSize)
		if err == nil {
			imageSize = reqSize
		}
	}
	imageSize.Format = resource.BinarySI

	dataVolume := v1beta1.DataVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: imgName,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
				image.K8sImageIdLabel:    string(volumeImportSource.GetUID()),
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
						corev1.ResourceStorage: imageSize,
					},
				},
				StorageClassName: &storageClassName,
			},
		},
	}
	dataVolume.Namespace = *m.k8sCfg.Namespace
	if err := m.client.Create(ctx, &dataVolume); err != nil {
		cleanupVolumeImportSource()
		return nil, fmt.Errorf("create CDI DataVolume for image '%s': %w", imgName, err)
	}
	cleanupDataVolume := func() error {
		cleanupVolumeImportSource()
		return m.client.Delete(cleanupCtx, &v1beta1.DataVolume{
			ObjectMeta: v1.ObjectMeta{Name: imgName, Namespace: *m.k8sCfg.Namespace},
		})
	}

	// Label the VolumeImportSource with the DataVolume id. Patch (not Update) to
	// avoid a resourceVersion conflict on the object we just created.
	visBase := volumeImportSource.DeepCopy()
	volumeImportSource.Labels[K8sDataVolumeIdLabel] = string(dataVolume.GetUID())
	if err := m.client.Patch(ctx, volumeImportSource, client.MergeFrom(visBase)); err != nil {
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
