package glance

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/imagedata"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/images"
	"github.com/gophercloud/gophercloud/pagination"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
)

// Image manager for glance image storage
// Glance image manager uses global lock to protect shared resources (TODO: Rewrite with lock-free API)
type manager struct {
	glanceServiceClient *gophercloud.ServiceClient

	lock sync.Mutex
}

func NewGlanceImageManager(cfg *config.GlanceImageConfig) (*manager, error) {
	client, err := openstack.AuthenticatedClient(gophercloud.AuthOptions{
		// IdentityEndpoint: cfg.Identity.Endpoint,
		// Username:         cfg.Identity.Username,
		// Password:         cfg.Identity.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to create client to the openstack Identity service: %w", err)
	}

	glanceClient, err := openstack.NewImageServiceV2(client, gophercloud.EndpointOpts{
		// Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to create Glance Image service client: %w", err)
	}
	return &manager{
		glanceServiceClient: glanceClient,
		lock:                sync.Mutex{},
	}, nil
}

func (m *manager) GetImage(ctx context.Context, id *nfv.Identifier) (*nfv.SoftwareImageInformation, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if id == nil || id.Value == "" {
		return nil, fmt.Errorf("Id should not be empty")
	}
	getRes := images.Get(m.glanceServiceClient, id.Value)
	img, err := getRes.Extract()
	if err != nil {
		return nil, fmt.Errorf("Failed to get image with id \"%s\" from glance image service: %w", id.Value, err)
	}
	imgNfv, err := convertImage(img)
	if err != nil {
		return nil, fmt.Errorf("Failed to convert image with id \"%s\" from glance image service to internal struct: %w", id.Value, err)
	}
	return imgNfv, nil
}

func (m *manager) ListImages(ctx context.Context) ([]*nfv.SoftwareImageInformation, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	pager := images.List(m.glanceServiceClient, images.ListOpts{})
	if pager.Err != nil {
		return nil, fmt.Errorf("Failed to get images from the glance server: %w", pager.Err)
	}
	imagesRes := make([]*nfv.SoftwareImageInformation, 0)
	if err := pager.EachPage(func(p pagination.Page) (bool, error) {
		imgs, err := images.ExtractImages(p)
		if err != nil {
			return false, fmt.Errorf("Failed to extract images from the glance list image response: %w", err)
		}
		for _, img := range imgs {
			nfvImg, err := convertImage(&img)
			if err != nil {
				return false, fmt.Errorf("Failed to convert image to the internal structure: %w", err)
			}
			imagesRes = append(imagesRes, nfvImg)
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("Failed to iterate over the images provided by glance image service: %w", err)
	}
	return imagesRes, nil
}

func (m *manager) UploadImage(ctx context.Context, id *nfv.Identifier, location string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if id == nil || id.Value == "" {
		return fmt.Errorf("Id should not be empty")
	}
	img, err := imagedata.Download(m.glanceServiceClient, id.Value).Extract()
	if err != nil {
		return fmt.Errorf("Failed to to download image with id \"%s\" from the glance service: %w", id, err)
	}
	defer img.Close()

	file, err := os.OpenFile(location, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("Faile to open/create file %s to download image with id \"%s\" provided by the glance service: %w", id, location, err)
	}
	defer file.Close()
	_, err = io.Copy(file, img)
	if err != nil {
		return fmt.Errorf("Failed to read image data buffer for image with id \"%s\" to the file %s: %w", id, location, err)
	}
	return nil
}

func convertImage(img *images.Image) (*nfv.SoftwareImageInformation, error) {
	// TODO:
	return nil, nil
}
