package image

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	cdi "kubevirt.io/client-go/generated/containerized-data-importer/clientset/versioned"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

var (
	ApplyOptionErr     = fmt.Errorf("failed to apply option")
	DVAlreadyExistsErr = fmt.Errorf("Data Volume already exists")
	DVNotFoundErr      = fmt.Errorf("Data Volume not found")
)

// kubevirt CDI (Contrinerized Data Imported) controller manage the lifecycle of the DVs(Data Volume)
// Current implementation is stateless (no objects located in struct related to DV). But it is not
// efficient since the calls to the kube-api need to be made on each call.
type CdiController struct {
	cdiClient *cdi.Clientset
	namespace string
}

// Creates new controller for the CDI resources like DV within default KubeNfv namespace.
func NewCdiController(k8sConfig *rest.Config) (*CdiController, error) {
	c, err := cdi.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubevirt cdi k8s client: %w", err)
	}
	return &CdiController{
		cdiClient: c,
		namespace: common.KubeNfvDefaultNamespace,
	}, nil
}

// Creates new controller for the CDI resources like DV within the specified namespace.
func NewNamespacedCdiController(k8sConfig *rest.Config, namespace string) (*CdiController, error) {
	c, err := cdi.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubevirt cdi k8s client: %w", err)
	}
	return &CdiController{
		cdiClient: c,
		namespace: namespace,
	}, nil
}

type GetDvOrVisOpt func(*getDvOrVisOpts)
type getDvOrVisOpts struct {
	Name      string
	UID       string
	SourceUrl string
}

// Option to specify Name for k8s resource. The best option to make Data Volume queries since it won't do bulk Get.
func FindByName(name string) GetDvOrVisOpt {
	return func(gdo *getDvOrVisOpts) {
		gdo.Name = name
	}
}

// Option to specify UID. If WithName specified togather it will be ignored.
func FindByUID(uid string) GetDvOrVisOpt {
	return func(gdo *getDvOrVisOpts) {
		gdo.UID = uid
	}
}

// Option to specify Source. If either WithName or WithUID specified it will be ignored
func FindBySourceUrl(sourceUrl string) GetDvOrVisOpt {
	return func(gdo *getDvOrVisOpts) {
		gdo.SourceUrl = sourceUrl
	}
}

// Returns the DV(Data Volume) if it exists. Data Volume should be identified by name, UID or created source URL.
func (c CdiController) GetDv(ctx context.Context, opts ...GetDvOrVisOpt) (*v1beta1.DataVolume, error) {
	// Apply each option
	cfg := getDvOrVisOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Name != "" {
		return c.cdiClient.CdiV1beta1().DataVolumes(c.namespace).Get(ctx, cfg.Name, v1.GetOptions{})
	} else if cfg.UID != "" {
		dvList, err := c.cdiClient.CdiV1beta1().DataVolumes(c.namespace).List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for idx, _ := range dvList.Items {
			dvRef := &dvList.Items[idx]
			if string(dvRef.GetUID()) == cfg.UID {
				return dvRef, nil
			}
		}
	} else if cfg.SourceUrl != "" {
		dvList, err := c.cdiClient.CdiV1beta1().DataVolumes(c.namespace).List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for idx, _ := range dvList.Items {
			dvRef := &dvList.Items[idx]
			sourceTypeStr, ok := dvRef.Labels[K8sSourceLabel]
			if !ok {
				continue
			}
			sourceType, err := SourceTypeFromString(sourceTypeStr)
			if err != nil {
				continue
			}
			switch sourceType {
			case HTTP:
				fallthrough
			case HTTPS:
				if dvRef.Spec.Source.HTTP != nil && dvRef.Spec.Source.HTTP.URL == cfg.SourceUrl {
					return dvRef, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("Either Name, UID or Source should be specified to find Data Volume: %w", common.NotFoundErr)
}

func (c CdiController) GetVolumeImportSource(ctx context.Context, opts ...GetDvOrVisOpt) (*v1beta1.VolumeImportSource, error) {
	// Apply each option
	cfg := getDvOrVisOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Name != "" {
		return c.cdiClient.CdiV1beta1().VolumeImportSources(c.namespace).Get(ctx, cfg.Name, v1.GetOptions{})
	} else if cfg.UID != "" {
		visList, err := c.cdiClient.CdiV1beta1().VolumeImportSources(c.namespace).List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for idx, _ := range visList.Items {
			visRef := &visList.Items[idx]
			if string(visRef.GetUID()) == cfg.UID {
				return visRef, nil
			}
		}
	} else if cfg.SourceUrl != "" {
		visList, err := c.cdiClient.CdiV1beta1().VolumeImportSources(c.namespace).List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for idx, _ := range visList.Items {
			visRef := &visList.Items[idx]
			sourceTypeStr, ok := visRef.Labels[K8sSourceLabel]
			if !ok {
				continue
			}
			sourceType, err := SourceTypeFromString(sourceTypeStr)
			if err != nil {
				continue
			}
			switch sourceType {
			case HTTP:
				fallthrough
			case HTTPS:
				if visRef.Spec.Source.HTTP != nil && visRef.Spec.Source.HTTP.URL == cfg.SourceUrl {
					return visRef, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("Either Name, UID or Source should be specified to find Volume Import Source: %w", common.NotFoundErr)
}

type CreateDvOpt func(*createDvOpts)
type createDvOpts struct {
	Name                string
	Size                *resource.Quantity
	StorageClassName    string
	PreferredAccessMode corev1.PersistentVolumeAccessMode
}

func defaultCreateDvOpts() createDvOpts {
	return createDvOpts{
		Name:                "",
		Size:                resource.NewQuantity(50*1024*1024 /*50Mi*/, resource.BinarySI),
		StorageClassName:    "default",
		PreferredAccessMode: corev1.ReadOnlyMany,
	}
}

// specify the name for a created Data Volume. If not specified name will be automatically
// calculated from the source
func CreateWithName(name string) CreateDvOpt {
	return func(cdo *createDvOpts) {
		cdo.Name = name
	}
}

// spcify size of the Data Volume to be created. If not specifed DV will be created
// with default size 50Mi
func CreateWithSize(size *resource.Quantity) CreateDvOpt {
	return func(cdo *createDvOpts) {
		cdo.Size = size
	}
}

// specify the k8s storage class name to be used while creating Data Volume. If not specified
// the DV will be create with the "default" storage class.
func CreateWithStorageClass(name string) CreateDvOpt {
	return func(cdo *createDvOpts) {
		cdo.StorageClassName = name
	}
}

// specify the Data Volume PVC accessMode. If not specified RX is used. If specified
// storage class not support RX then supported access mode will be used.
func CreateWithPreferredPVAccessMode(accessMode corev1.PersistentVolumeAccessMode) CreateDvOpt {
	return func(cdo *createDvOpts) {
		cdo.PreferredAccessMode = accessMode
	}
}

// Creates the DV(Data Volume) with provided source spec.
func (c CdiController) CreateDv(ctx context.Context, source *v1beta1.DataVolumeSource, opts ...CreateDvOpt) (*v1beta1.DataVolume, error) {
	cfg := defaultCreateDvOpts()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Name == "" {
		var err error
		cfg.Name, err = formatDVNameFromSource(source)
		if err != nil {
			return nil, fmt.Errorf("failed to format Data Voule name from source: %w", err)
		}
	}
	var storageClassName *string = nil
	if cfg.StorageClassName != "" && cfg.StorageClassName != "default" {
		storageClassName = &cfg.StorageClassName
	}
	sourceType, err := formatSourceNameFromDvSource(source)
	if err != nil {
		return nil, fmt.Errorf("failed to identify source type from Data Volume Source: %w", err)
	}

	// TODO: add storage class verification
	return c.cdiClient.CdiV1beta1().DataVolumes(c.namespace).Create(ctx, &v1beta1.DataVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: cfg.Name,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
				K8sSourceLabel:           string(sourceType),
			},
			Annotations: map[string]string{
				// Temporary solution to imidiately bind PVC to the storage class with WaitForFirstConsumer option
				"cdi.kubevirt.io/storage.bind.immediate.requested": "true",
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			Source:      source,
			ContentType: "kubevirt",
			Storage: &v1beta1.StorageSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *cfg.Size,
					},
				},
				AccessModes: []corev1.PersistentVolumeAccessMode{
					cfg.PreferredAccessMode,
				},
				StorageClassName: storageClassName,
			},
		},
	}, v1.CreateOptions{})
}

// Create Virtual Import Source from spec.
func (c CdiController) CreateVolumeImportSource(ctx context.Context, source *v1beta1.ImportSourceType) (*v1beta1.VolumeImportSource, error) {
	name, err := formatVisNameFromSource(source)
	if err != nil {
		return nil, fmt.Errorf("failed to format Virtual Import Source name from source: %w", err)
	}
	sourceType, err := formatSourceNameFromVisSource(source)
	if err != nil {
		return nil, fmt.Errorf("failed to identify source type from Virtual Import Source: %w", err)
	}
	return c.cdiClient.CdiV1beta1().VolumeImportSources(c.namespace).Create(ctx, &v1beta1.VolumeImportSource{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
				K8sSourceLabel:           string(sourceType),
			},
		},
		Spec: v1beta1.VolumeImportSourceSpec{
			Source: source,
		},
	}, v1.CreateOptions{})
}

// DeleteDV(Data Volume) specified by id or name.
func DeleteDv() {

}

func GetDvs() {

}

func formatSourceNameFromDvSource(source *v1beta1.DataVolumeSource) (sourceType, error) {
	if source.HTTP != nil {
		if source.HTTP.CertConfigMap != "" || source.HTTP.SecretRef != "" {
			return HTTPS, nil
		}
		return HTTP, nil
	}
	return "", fmt.Errorf("unsupported source: %w", common.NotImplementedErr)
}

func formatSourceNameFromVisSource(source *v1beta1.ImportSourceType) (sourceType, error) {
	if source.HTTP != nil {
		if source.HTTP.CertConfigMap != "" || source.HTTP.SecretRef != "" {
			return HTTPS, nil
		}
		return HTTP, nil
	}
	return "", fmt.Errorf("unsupported source: %w", common.NotImplementedErr)
}

func formatDVNameFromSource(source *v1beta1.DataVolumeSource) (string, error) {
	switch {
	case source.HTTP != nil:
		return formatDvNameFromHttpSource(source.HTTP)
	default:
		return "", fmt.Errorf("can't format name from the specified source: %w", common.UnsupportedErr)
	}
}

func formatVisNameFromSource(source *v1beta1.ImportSourceType) (string, error) {
	switch {
	case source.HTTP != nil:
		return formatDvNameFromHttpSource(source.HTTP)
	default:
		return "", fmt.Errorf("can't format name from the specified source: %w", common.UnsupportedErr)
	}
}

func formatDvNameFromHttpSource(httpSource *v1beta1.DataVolumeSourceHTTP) (string, error) {
	url, err := url.Parse(httpSource.URL)
	if err != nil {
		return "", fmt.Errorf("failed to parse url \"%s\": %w", httpSource.URL, err)
	}
	fileName := path.Base(url.Path)
	leafName := strings.TrimSuffix(fileName, path.Ext(fileName))
	leafName = strings.ToLower(leafName)
	reg := regexp.MustCompile(`[^a-z0-9-]+`)
	leafName = reg.ReplaceAllString(leafName, "-")
	leafName = strings.Trim(leafName, "-")
	if len(leafName) > 253 {
		leafName = leafName[:253]
	}
	return leafName, nil
}
