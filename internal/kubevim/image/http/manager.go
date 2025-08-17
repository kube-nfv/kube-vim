package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/misc"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

var (
	// Untyped errors for simple cases
	ErrContentLengthMissing = errors.New("Content-Length header is missing")
)

// http image manager provides the ability to download software image from the
// http(s) endpoints. Uploaded image should be able to stored either in pvc or in the
// kubevirt datavolume.
type manager struct {
	cdiCtrl *image.CdiController
}

// initialize new http image manager from the specified configuration
func NewHttpImageManager(cdiCtrl *image.CdiController, cfg *config.HttpImageConfig) (*manager, error) {
	return &manager{
		cdiCtrl: cdiCtrl,
	}, nil
}

// get http image and store it in the kubevirt DV (Data Volume) or in the PV claimed by PVC.
// Note: For http image manager image Identifier should be full url path if image not exists yet.
// If image already created it might be identified by either DV name, DV UID or source url.
// TODO(dmaloval)
//
//	Add ability to works with different storage clases (as well as WaitForFirstConsumer mode)
func (m *manager) GetImage(ctx context.Context, imageId *nfv.Identifier) (*nfv.SoftwareImageInformation, error) {
	if imageId == nil || imageId.GetValue() == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "image id", Reason: "cannot be empty"}
	}
	isSource := false
	getDvOpts := []image.GetDvOrVisOpt{}
	if strings.HasPrefix(imageId.GetValue(), "http") || strings.HasPrefix(imageId.GetValue(), "https") {
		getDvOpts = append(getDvOpts, image.FindBySourceUrl(imageId.GetValue()))
		isSource = true
	} else if misc.IsUUID(imageId.GetValue()) {
		getDvOpts = append(getDvOpts, image.FindByUID(imageId.GetValue()))
	} else {
		getDvOpts = append(getDvOpts, image.FindByName(imageId.GetValue()))
	}
	vis, err := m.cdiCtrl.GetVolumeImportSource(ctx, getDvOpts...)
	if err == nil {
		return softwareImageInfoFromVolumeImportSource(vis)
	}
	var notFoundErr *apperrors.ErrNotFound
	if !k8s_errors.IsNotFound(err) && !errors.As(err, &notFoundErr) {
		return nil, fmt.Errorf("get k8s data volume for image '%s': %w", imageId.GetValue(), err)
	}
	// Data volume not found and need to be created.
	if !isSource {
		return nil, fmt.Errorf("initial image placement requires source URL as imageId: %w", apperrors.ErrUnsupported)
	}
	vis, err = m.cdiCtrl.CreateVolumeImportSource(ctx, &v1beta1.ImportSourceType{
		HTTP: &v1beta1.DataVolumeSourceHTTP{
			URL: imageId.GetValue(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create k8s VolumeImportSource resource: %w", err)
	}
	return softwareImageInfoFromVolumeImportSource(vis)
}

func (m *manager) ListImages(ctx context.Context) ([]*nfv.SoftwareImageInformation, error) {
	images, err := m.cdiCtrl.ListVolumeImportSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volume import sources: %w", err)
	}
	res := make([]*nfv.SoftwareImageInformation, 0, len(images))
	for idx := range images {
		imgRef := &images[idx]
		imgInfo, err := softwareImageInfoFromVolumeImportSource(imgRef)
		if err != nil {
			return nil, fmt.Errorf("convert volume import source to image info: %w", err)
		}
		res = append(res, imgInfo)
	}
	return res, nil
}

func (m *manager) UploadImage(context.Context, *nfv.Identifier, string /*location*/) error {

	return apperrors.ErrNotImplemented
}

// TODO: HTTP HEAD returns actual image size, while PVC need to be created with virtual.
// Add qemu-img size check
// Also some http enpoints not supports http HEAD (ex. S3)
func tryCalculeteContentLength(url string) (int64, error) {
	resp, err := http.Head(url)
	if err != nil {
		return 0, fmt.Errorf("make HEAD request to '%s': %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http request to '%s' failed with status: %s", url, resp.Status)
	}
	contentLength := resp.Header.Get("Content-Length")
	if contentLength == "" {
		return 0, ErrContentLengthMissing
	}
	size, err := strconv.ParseInt(contentLength, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse Content-Length header for '%s': %w", url, err)
	}
	return size, nil
}

func softwareImageInfoFromDv(dv *v1beta1.DataVolume) *nfv.SoftwareImageInformation {
	return &nfv.SoftwareImageInformation{
		SoftwareImageId: &nfv.Identifier{
			Value: string(dv.GetUID()),
		},
		Name: dv.Name,
		Size: dv.Spec.Storage.Resources.Requests.Storage(),
	}
}

func softwareImageInfoFromVolumeImportSource(vis *v1beta1.VolumeImportSource) (*nfv.SoftwareImageInformation, error) {
	if !misc.IsObjectInstantiated(vis) {
		return nil, &apperrors.ErrK8sObjectNotInstantiated{ObjectType: "VolumeImportSource"}
	}
	if !misc.IsObjectManagedByKubeNfv(vis) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VolumeImportSource", ObjectName: vis.Name, ObjectId: string(vis.GetUID())}
	}
	if source, ok := vis.Labels[image.K8sSourceLabel]; !ok || (source != string(image.HTTP) && source != string(image.HTTPS)) {
		return nil, fmt.Errorf("http image manager cannot convert image with '%s' source: %w", source, apperrors.ErrUnsupported)
	}
	metadata := &nfv.Metadata{
		Fields: vis.Labels,
	}
	for k, v := range vis.Annotations {
		metadata.Fields[k] = v
	}
	return &nfv.SoftwareImageInformation{
		SoftwareImageId: &nfv.Identifier{
			Value: string(vis.GetUID()),
		},
		Name:      vis.Name,
		CreatedAt: misc.ConvertK8sTimeToProtoTimestamp(vis.CreationTimestamp),
		UpdatedAt: misc.ConvertK8sTimeToProtoTimestamp(vis.CreationTimestamp),
		// Temportary solution to allocated 5 Gi to the image
		Size:     resource.NewQuantity(5*1024*1024*1024, resource.BinarySI),
		Metadata: metadata,
	}, nil
}
