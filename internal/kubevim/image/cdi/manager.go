package cdi

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/misc"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CDIVolumeImportSourceKind = "VolumeImportSource"
)

var (
	defaultImageSize = resource.MustParse("10Gi")
)

// IdIndexes are the cache indexes the manager looks images up by.
var IdIndexes = []k8s.LabelIndex{
	{Obj: &v1beta1.VolumeImportSource{}, Label: image.K8sImageIdLabel},
}

type cdiManager struct {
	// client serves cache-backed reads and direct writes.
	client client.Client
	// apiReader is uncached; used for cluster-scoped/unowned reads (StorageClass).
	apiReader client.Reader
	cfg       *config.ImageConfig
	k8sCfg    *config.K8sConfig
	// sources are the enabled image sources, by image.SourceKind.
	sources map[string]image.Source
}

func NewCDIImageManager(cl client.Client, apiReader client.Reader, cfg *config.ImageConfig, k8sCfg *config.K8sConfig, sources map[string]image.Source) (*cdiManager, error) {
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
		sources:   sources,
	}, nil
}

func (m *cdiManager) CreateImage(ctx context.Context, img *imagev1.Image) (*imagev1.Image, error) {
	if err := checkSupported(img); err != nil {
		return nil, err
	}
	kind := image.SourceKind(img.GetSource())
	src, ok := m.sources[kind]
	if !ok {
		return nil, fmt.Errorf("source '%s': %w", kind, apperrors.ErrUnsupported)
	}
	importSource, err := src.ImportSource(ctx, img.GetSource())
	if err != nil {
		return nil, fmt.Errorf("import source: %w", err)
	}
	imageId := uuid.NewString()
	imgName := "image-" + imageId
	stored, err := marshalImage(storedImage(img, src))
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		common.K8sManagedByLabel: common.KubeNfvName,
		image.K8sImageIdLabel:    imageId,
	}
	volumeImportSource := &v1beta1.VolumeImportSource{
		ObjectMeta: v1.ObjectMeta{
			Name:        imgName,
			Namespace:   *m.k8sCfg.Namespace,
			Labels:      labels,
			Annotations: map[string]string{image.K8sImageAnnotation: stored},
		},
		Spec: v1beta1.VolumeImportSourceSpec{
			Source: importSource,
		},
	}
	if err := m.client.Create(ctx, volumeImportSource); err != nil {
		return nil, fmt.Errorf("create CDI VolumeImportSource: %w", err)
	}
	// Cleanup must run even when the request ctx has been canceled (that is often
	// why we are rolling back), so it uses a ctx detached from cancellation.
	cleanupCtx := context.WithoutCancel(ctx)
	cleanupVolumeImportSource := func() {
		_ = m.client.Delete(cleanupCtx, &v1beta1.VolumeImportSource{
			ObjectMeta: v1.ObjectMeta{Name: imgName, Namespace: *m.k8sCfg.Namespace},
		})
	}

	storageClassName := *m.cfg.StorageClass
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
	imageSize.Format = resource.BinarySI

	dataVolume := &v1beta1.DataVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:        imgName,
			Namespace:   *m.k8sCfg.Namespace,
			Labels:      labels,
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
	if err := m.client.Create(ctx, dataVolume); err != nil {
		cleanupVolumeImportSource()
		return nil, fmt.Errorf("create CDI DataVolume for image '%s': %w", imgName, err)
	}
	return imageFromCdi(volumeImportSource, dataVolume)
}

func (m *cdiManager) GetImage(ctx context.Context, imageId string) (*imagev1.Image, error) {
	visList := &v1beta1.VolumeImportSourceList{}
	if err := m.client.List(ctx, visList, client.InNamespace(*m.k8sCfg.Namespace),
		client.MatchingFields{image.K8sImageIdLabel: imageId}); err != nil {
		return nil, fmt.Errorf("list CDI VolumeImportSources for image '%s': %w", imageId, err)
	}
	switch len(visList.Items) {
	case 0:
		return nil, &apperrors.ErrNotFound{Entity: "software image", Identifier: imageId}
	case 1:
	default:
		return nil, fmt.Errorf("%d VolumeImportSources carry image id '%s': %w", len(visList.Items), imageId, apperrors.ErrInternal)
	}
	vis := &visList.Items[0]
	if !misc.IsObjectManagedByKubeNfv(vis) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: CDIVolumeImportSourceKind, ObjectName: vis.Name, ObjectId: string(vis.GetUID())}
	}
	dv := &v1beta1.DataVolume{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: vis.Namespace, Name: vis.Name}, dv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("image '%s' has no DataVolume '%s': %w", imageId, vis.Name, apperrors.ErrInternal)
		}
		return nil, fmt.Errorf("get CDI DataVolume of image '%s': %w", imageId, err)
	}
	return imageFromCdi(vis, dv)
}

func (m *cdiManager) ListImages(ctx context.Context) ([]*imagev1.Image, error) {
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
	res := make([]*imagev1.Image, 0, len(visList.Items))
	for idx := range visList.Items {
		vis := &visList.Items[idx]
		// An image whose DataVolume is gone is not listed.
		dv, ok := dataVolumesIdx[vis.Name]
		if !ok {
			continue
		}
		img, err := imageFromCdi(vis, dv)
		if err != nil {
			return nil, err
		}
		res = append(res, img)
	}
	return res, nil
}
