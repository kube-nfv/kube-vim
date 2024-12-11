package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/DiMalovanyy/kube-vim/internal/kubevim/image"
	"github.com/google/uuid"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

var (
	contentLengthMissingErr = fmt.Errorf("Content-Length header is missing")
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
		return nil, fmt.Errorf("specified image id can't be empty")
	}
	isSource := false
	getDvOpts := []image.GetDvOpt{}
	if strings.HasPrefix(imageId.GetValue(), "http") || strings.HasPrefix(imageId.GetValue(), "https") {
		getDvOpts = append(getDvOpts, image.FindBySourceUrl(imageId.GetValue()))
		isSource = true
	} else if _, err := uuid.Parse(imageId.GetValue()); err != nil {
		getDvOpts = append(getDvOpts, image.FindByUID(imageId.GetValue()))
	} else {
		getDvOpts = append(getDvOpts, image.FindByName(imageId.GetValue()))
	}
	dv, err := m.cdiCtrl.GetDv(ctx, getDvOpts...)
	if err == nil {
		return softwareImageInfoFromDv(dv), nil
	}
	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("can't get k8s Data Volume specified by the imageId \"%s\": %w", imageId.GetValue(), err)
	}
	// Data volume not found and need to be created.
	if !isSource {
		return nil, fmt.Errorf("initial image placement should be done using image source as imageId: %w", config.UnsupportedErr)
	}

	createDvOpts := []image.CreateDvOpt{}
	contentLength, err := tryCalculeteContentLength(imageId.GetValue())
	if err == nil {
		contentLengthEpsilon := float64(contentLength) * 0.2
		contentLength = contentLength + int64(contentLengthEpsilon)
		createDvOpts = append(createDvOpts,
			image.CreateWithSize(resource.NewQuantity(contentLength, resource.BinarySI)))
	}
	dv, err = m.cdiCtrl.CreateDv(ctx, &v1beta1.DataVolumeSource{
		HTTP: &v1beta1.DataVolumeSourceHTTP{
			URL: imageId.GetValue(),
		},
	}, createDvOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s Data Volume resource: %w", err)
	}
	return softwareImageInfoFromDv(dv), nil
}

func (m *manager) GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error) {

	return nil, config.NotImplementedErr
}

func (m *manager) UploadImage(context.Context, *nfv.Identifier, string /*location*/) error {

	return config.NotImplementedErr
}

func tryCalculeteContentLength(url string) (int64, error) {
	resp, err := http.Head(url)
	if err != nil {
		return 0, fmt.Errorf("failed to make HEAD request to the \"%s\"", url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http request failed with status: %s", resp.Status)
	}
	contentLength := resp.Header.Get("Content-Length")
	if contentLength == "" {
		return 0, contentLengthMissingErr
	}
	size, err := strconv.ParseInt(contentLength, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse Content-Length header: %w", err)
	}
	return size, nil
}

func softwareImageInfoFromDv(dv *v1beta1.DataVolume) *nfv.SoftwareImageInformation {
	return &nfv.SoftwareImageInformation{
		SoftwareImageId: &nfv.Identifier{
			Value: string(dv.GetUID()),
		},
        Name: dv.Name,
		Size: dv.Spec.Storage.Resources.Requests.Memory(),
	}
}
