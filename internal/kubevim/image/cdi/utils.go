package cdi

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/misc"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// TODO: Add additional image import sources
func importSourceTypeFromImageSource(imgSource *admin.ImageSource) (*v1beta1.ImportSourceType, error) {
	if imgSource == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "imageSource", Reason: "can't be nil"}
	}
	res := &v1beta1.ImportSourceType{}
	var err error

	switch imgSource.Type {
	case admin.ImageSourceType_HTTP:
		if res.HTTP, err = convertToHttpDataVolumeSource(imgSource.Http); err != nil {
			return nil, fmt.Errorf("convert to http data volume source: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown imageSource type '%s': %w", imgSource.Type, apperrors.ErrUnsupported)
	}
	return res, nil
}

// TODO: Also add secrets from HTTP population
func convertToHttpDataVolumeSource(http *admin.HttpSource) (*v1beta1.DataVolumeSourceHTTP, error) {
	if http == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "httpSource", Reason: "can't be nil"}
	}
	res := &v1beta1.DataVolumeSourceHTTP{}
	res.URL = http.GetUrl()

	extraHeaders := make([]string, 0, len(http.Headers))
	for hdr, val := range http.Headers {
		extraHeaders = append(extraHeaders, fmt.Sprintf("%s: %s", hdr, val))
	}
	res.ExtraHeaders = extraHeaders
	return res, nil
}

// GetStorageClass retrieves a StorageClass by name.
// If the name is "default", it returns the cluster's default StorageClass.
func getStorageClass(ctx context.Context, name string, clientset *kubernetes.Clientset) (*storagev1.StorageClass, error) {
	if name == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "name", Reason: "can't be empty"}
	}
	storageClient := clientset.StorageV1().StorageClasses()

	if name != "default" {
		// Get StorageClass by exact name
		sc, err := storageClient.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get storage class %q: %w", name, err)
		}
		return sc, nil
	}
	// If "default", find the StorageClass annotated as default
	scList, err := storageClient.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list storage classes: %w", err)
	}

	for _, sc := range scList.Items {
		annotations := sc.Annotations
		if annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
			return &sc, nil
		}
	}

	return nil, &apperrors.ErrNotFound{Entity: "storageClass", Identifier: "default"}
}

func nfvImageFromCdiDataVolumeVis(dv *v1beta1.DataVolume, vis *v1beta1.VolumeImportSource) (*vivnfm.SoftwareImageInformation, error) {
	if dv == nil || vis == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "dataVolume or volumeImportSource", Reason: "can't be nil"}
	}
	if !misc.IsObjectInstantiated(dv) {
		return nil, &apperrors.ErrK8sObjectNotInstantiated{ObjectType: dv.Kind, Identifier: dv.Name}
	}
	if !misc.IsObjectInstantiated(vis) {
		return nil, &apperrors.ErrK8sObjectNotInstantiated{ObjectType: vis.Kind, Identifier: vis.Name}
	}
	if !misc.IsObjectManagedByKubeNfv(dv) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: dv.Kind, ObjectName: dv.Name, ObjectId: string(dv.GetUID())}
	}
	if !misc.IsObjectManagedByKubeNfv(vis) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: vis.Kind, ObjectName: vis.Name, ObjectId: string(vis.GetUID())}
	}

	imgId := misc.UIDToIdentifier(vis.GetUID())
	imgName := vis.GetName()
	srcTypeName, err := sourceNameFromImportSourceType(vis.Spec.Source)
	if err != nil {
		return nil, fmt.Errorf("get sourceName from ImportSourceType: %w", err)
	}

	metadata := map[string]string{
		image.K8sImageIdLabel: imgId.GetValue(),
		image.K8sSourceLabel:  string(srcTypeName),
		K8sDataVolumeIdLabel:  string(dv.GetUID()),
		K8sDataVolumePhase:    string(dv.Status.Phase),
	}

	for _, dvCond := range dv.Status.Conditions {
		switch dvCond.Type {
		case v1beta1.DataVolumeBound:
			if dvCond.Status == v1.ConditionTrue {
				metadata[image.K8sIsImageBoundToPvc] = "true"
			} else {
				metadata[image.K8sIsImageBoundToPvc] = "false"
			}
		}
	}
	crtTime := misc.ConvertToProtoTimestamp(misc.GetCreationTimestamp(vis))
	updtTime := crtTime
	lstUpdtTime := misc.GetLastUpdateTime(dv)
	if lstUpdtTime != nil {
		updtTime = misc.ConvertToProtoTimestamp(*lstUpdtTime)
	}

	// TODO: For now each image is uploaded
	isUploaded := true
	status := "ready"

	metadata[image.K8sIsUploadLabel] = strconv.FormatBool(isUploaded)

	res := &vivnfm.SoftwareImageInformation{
		SoftwareImageId: imgId,
		Name:            imgName,
		CreatedAt:       crtTime,
		UpdatedAt:       updtTime,
		Size:            dv.Spec.Storage.Resources.Requests.Storage(),
		Status:          status,
		Metadata: &apis.Metadata{
			Fields: metadata,
		},
	}
	return res, nil
}

func sourceNameFromImportSourceType(source *v1beta1.ImportSourceType) (image.SourceType, error) {
	if source == nil {
		return "", &apperrors.ErrInvalidArgument{Field: "source", Reason: "can't be nil"}
	}
	if source.HTTP != nil {
		if source.HTTP.CertConfigMap != "" || source.HTTP.SecretRef != "" {
			return image.HTTPS, nil
		}
		return image.HTTP, nil
	}
	return "", fmt.Errorf("unsupported source: %w", apperrors.ErrUnsupported)
}
