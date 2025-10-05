package cdi

import (
	"context"
	"fmt"

	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
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
