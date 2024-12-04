package http

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	cdi "kubevirt.io/client-go/generated/containerized-data-importer/clientset/versioned"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

var (
	contentLengthMissingErr = fmt.Errorf("Content-Length header is missing")
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
// TODO(dmalovan): Add ability to getImage that already downloaded.
//	Add ability to query image by UID like: http://<uid>
//  Add ability to works with different storage clases (as well as WaitForFirstConsumer mode)
func (m *manager) GetImage(ctx context.Context, imageId *nfv.Identifier) (*nfv.SoftwareImageInformation, error) {
	if imageId == nil {
		return nil, fmt.Errorf("specified image id can't be empty")
	}
	url, err := url.Parse(imageId.GetValue())
	if err == nil && url.Scheme != "http" && url.Scheme != "https" {
		err = fmt.Errorf("url should has http or https scheme")
	}
	if err != nil {
		return nil, fmt.Errorf("valid url should be specified as image id for http image manager. id \"%s\" is not valid: %w", imageId.GetValue(), err)
	}
	_, err = tryCalculeteContentLength(url)
	if err != nil {
		// Failed to calculate content length. So the size will be approximate for downloaded image
		return nil, fmt.Errorf("failed to calculate size from the HEAD Content-Length header: %w", err)
	}
	// Add 10% to the size to make it more flexible
	// adjSize := size + size/10
	// sizeQuantity := resource.NewQuantity(int64(adjSize), resource.BinarySI)
    sizeQuantity, _ := resource.ParseQuantity("64Mi")
	dv, err := m.cdiClient.CdiV1beta1().DataVolumes(config.KubeNfvDefaultNamespace).Create(ctx, &v1beta1.DataVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: getK8sObjectNameFromUrl(url),
			Labels: map[string]string{
				config.K8sManagedByLabel: config.KubeNfvName,
			},
            Annotations: map[string]string{
                // Temporary solution to imidiately bind PVC to the storage class with WaitForFirstConsumer option
                "cdi.kubevirt.io/storage.bind.immediate.requested": "true",
            },
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				HTTP: &v1beta1.DataVolumeSourceHTTP{
					URL: imageId.GetValue(),
				},
			},
            ContentType: "kubevirt",
			Storage: &v1beta1.StorageSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: sizeQuantity,
					},
				},
                AccessModes: []corev1.PersistentVolumeAccessMode{
                    corev1.ReadWriteOnce,
                },
			},
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

	return nil, config.NotImplementedErr
}

func (m *manager) UploadImage(context.Context, *nfv.Identifier, string /*location*/) error {

	return config.NotImplementedErr
}

func tryCalculeteContentLength(url *url.URL) (uint64, error) {
	resp, err := http.Head(url.String())
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
	size, err := strconv.ParseUint(contentLength, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse Content-Length header: %w", err)
	}
	return size, nil
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
