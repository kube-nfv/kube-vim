package http

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	cdi "kubevirt.io/client-go/generated/containerized-data-importer/clientset/versioned"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// http image manager provides the ability to download software image from the
// http(s) endpoints. Uploaded image should be able to stored either in pvc or in the
// kubevirt datavolume.
type manager struct {
	cdiClient *cdi.Clientset
}

// initialize new http image manager from the specified configuration
func NewHttpImageManager(k8sConfig *rest.Config, cfg *config.HttpImageConfig) (*manager, error) {
	c, err := cdi.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubevirt cdi k8s client: %w", err)
	}
	return &manager{
		cdiClient: c,
	}, nil
}

// get http image and store it in the kubevirt DV (Data Volume) or in the PV claimed by PVC.
// Note: For http image manager image Identifier should be full url path.
func (m *manager) GetImage(ctx context.Context, imageId *nfv.Identifier) (*nfv.SoftwareImageInformation, error) {
	if imageId == nil {
		return nil, fmt.Errorf("specified image id can't be empty")
	}
	url, err := url.Parse(imageId.GetValue())
	if err != nil {
		return nil, fmt.Errorf("valid url should be specified as image id for http image manager. id \"%s\" is not valid: %w", imageId.GetValue(), err)
	}
	dv, err := m.cdiClient.CdiV1beta1().DataVolumes(config.KubeNfvDefaultNamespace).Create(ctx, &v1beta1.DataVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: getK8sObjectNameFromUrl(url),
			Labels: map[string]string{
				config.K8sManagedByLabel: config.KubeNfvName,
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				HTTP: &v1beta1.DataVolumeSourceHTTP{
					URL: imageId.GetValue(),
				},
			},
			Storage: &v1beta1.StorageSpec{},
		},
	}, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s data volume for specified image with id \"%s\": %w", imageId.GetValue(), err)
	}
	return &nfv.SoftwareImageInformation{
		SoftwareImageId: &nfv.Identifier{Value: string(dv.GetUID())},
	}, nil
}

func (m *manager) GetImages(*nfv.Filter) ([]*nfv.SoftwareImageInformation, error) {

	return nil, nil
}

func (m *manager) UploadImage(context.Context, *nfv.Identifier, string /*location*/) error {

	return nil
}

func getUniqieNameFromUrl(url string, size uint64) string {
	hash := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x", hash[:size])
}

func getK8sObjectNameFromUrl(parsedUrl *url.URL) string {
	fileName := path.Base(parsedUrl.Path)
	leafName := strings.TrimSuffix(fileName, path.Ext(fileName))
	leafName = strings.ToLower(leafName)
	reg := regexp.MustCompile(`[^a-z0-9-]+`)
	leafName = reg.ReplaceAllString(leafName, "-")
	leafName = strings.Trim(leafName, "-")
	if len(leafName) > 253 {
		leafName = leafName[:253]
	}
	return leafName
}
