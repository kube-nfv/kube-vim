package image

import (
	"fmt"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
}

// Returns the DV(Data Volume) if it exists
func (c *CdiController) GetDv(opts ...option) (*v1beta1.DataVolume, error) {
    // Apply each option
    cfg := getDefaultDvOpts()
    for _, opt := range opts {
        opt.apply(&cfg)
    }
    if cfg.Name != "" {
        return c.cdiClient.CdiV1beta1().DataVolumes(cfg.Namespace).Get(cfg.Ctx, cfg.Name, v1.GetOptions{})
    } else if cfg.UID != "" {
        dvList, err := c.cdiClient.CdiV1beta1().DataVolumes(cfg.Namespace).List(cfg.Ctx, v1.ListOptions{})
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
        dvList, err := c.cdiClient.CdiV1beta1().DataVolumes(cfg.Namespace).List(cfg.Ctx, v1.ListOptions{
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
