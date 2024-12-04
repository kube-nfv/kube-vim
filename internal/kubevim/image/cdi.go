package image

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
	cdi "kubevirt.io/client-go/generated/containerized-data-importer/clientset/versioned"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
    DVAlreadyExistsErr = fmt.Errorf("Data Volume already exists")
    DVNotFoundErr      = fmt.Errorf("Data Volume not found")
)

// kubevirt CDI (Contrinerized Data Imported) controller manage the lifecycle of the DVs(Data Volume)
// Current implementation is stateless (no objects located in struct related to DV). But it is not
// efficient since the calls to the kube-api need to be made on each call.
type CdiController struct {
	cdiClient *cdi.Clientset
}


type getDvOpt func (*getDvOpts)
type getDvOpts struct {
    // Query params
    Name string
    UID string
    SourceUrl string

    Namespace string
    ctx context.Context
}
func getDefaultDvOpts() getDvOpts {
    return getDvOpts{
        Namespace: config.KubeNfvDefaultNamespace,
        ctx: context.Background(),
    }
}

// Option to specify Name. The best option to make Data Volume queries since it won't do bulk Get.
func WithName(name string) getDvOpt {
    return func(gdo *getDvOpts) {
        gdo.Name = name
    }
}
// Option to specify UID. If WithName specified togather it will be ignored.
func WithUID(uid string) getDvOpt {
    return func(gdo *getDvOpts) {
        gdo.UID = uid
    }
}
// Option to specify Source. If either WithName or WithUID specified it will be ignored
func WithSourceUrl(sourceUrl string) getDvOpt {
    return func(gdo *getDvOpts) {
        gdo.SourceUrl = sourceUrl
    }
}
// Specify namespace for kubectl Data Volumes resources. If not specified dafault will be used.
func WithNamespace(namespace string) getDvOpt {
    return func(gdo *getDvOpts) {
        gdo.Namespace = namespace
    }
}
func WithContext(ctx context.Context) getDvOpt {
    return func(gdo *getDvOpts) {
        gdo.ctx = ctx
    }
}

// Returns the DV(Data Volume) if it exists
func (c *CdiController) GetDv(opts ...getDvOpt) (*v1beta1.DataVolume, error) {
    // Apply each option
    cfg := getDefaultDvOpts()
    for _, opt := range opts {
        opt(&cfg)
    }
    if cfg.Name != "" {
        return c.cdiClient.CdiV1beta1().DataVolumes(cfg.Namespace).Get(cfg.ctx, cfg.Name, v1.GetOptions{})
    } else if cfg.UID != "" {
        dvList, err := c.cdiClient.CdiV1beta1().DataVolumes(cfg.Namespace).List(cfg.ctx, v1.ListOptions{})
        if err != nil {
            return nil, err
        }
        for idx, _ := range dvList.Items {
            dvRef := &dvList.Items[idx]
            if string(dvRef.GetUID()) == cfg.UID {
                return dvRef, nil
            }
        }
        return nil, DVNotFoundErr
    } else if cfg.SourceUrl != "" {
        dvList, err := c.cdiClient.CdiV1beta1().DataVolumes(cfg.Namespace).List(cfg.ctx, v1.ListOptions{
            LabelSelector: fmt.Sprintf("%s=%s", K8sSourceUrlLabel, cfg.SourceUrl),
        })
        if err != nil {
            return nil, err
        }
        if len(dvList.Items) > 1 {
            return nil, fmt.Errorf("more that one Data Volume specified by source URL \"%s\"", cfg.SourceUrl)
        }
        return &dvList.Items[0], nil
    }
    return nil, fmt.Errorf("Either UID or Source should be specified to find Data Volume")
}

// Creates the DV(Data Volume) with provided source spec.
func CreateDv() {

}

// DeleteDV(Data Volume) specified by id or name.
func DeleteDv() {

}

func GetDvs() {

}
