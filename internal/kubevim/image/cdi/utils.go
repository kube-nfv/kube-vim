package cdi

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	storagev1 "k8s.io/api/storage/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// checkSupported rejects image fields outside the source that kube-vim does not apply yet.
func checkSupported(img *imagev1.Image) error {
	unsupported := ""
	switch {
	case img.GetImportPolicy() == imagev1.ImportPolicy_IMPORT_POLICY_ON_FIRST_USE:
		unsupported = "importPolicy"
	case img.GetFirmware() != imagev1.Firmware_FIRMWARE_UNSPECIFIED:
		unsupported = "firmware"
	case img.GetDiskBus() != imagev1.DiskBus_DISK_BUS_UNSPECIFIED:
		unsupported = "diskBus"
	case img.GetMinDisk() != "":
		unsupported = "minDisk"
	case img.GetMinRam() != "":
		unsupported = "minRam"
	}
	if unsupported != "" {
		return fmt.Errorf("%s: %w", unsupported, apperrors.ErrUnsupported)
	}
	return nil
}

// storedImage returns the consumer's fields of img as kept in the annotation: no id, outputs or input-only fields.
func storedImage(img *imagev1.Image, src image.Source) *imagev1.Image {
	return &imagev1.Image{
		Name:         img.GetName(),
		Provider:     img.GetProvider(),
		Version:      img.GetVersion(),
		Metadata:     img.GetMetadata(),
		Source:       src.Stored(img.GetSource()),
		ImportPolicy: imagev1.ImportPolicy_IMPORT_POLICY_EAGER,
	}
}

func marshalImage(img *imagev1.Image) (string, error) {
	raw, err := protojson.Marshal(img)
	if err != nil {
		return "", fmt.Errorf("marshal image: %w", err)
	}
	return string(raw), nil
}

// imageFromCdi returns the image vis carries, with its id, outputs from dv and kube-vim metadata keys.
func imageFromCdi(vis *v1beta1.VolumeImportSource, dv *v1beta1.DataVolume) (*imagev1.Image, error) {
	raw, ok := vis.Annotations[image.K8sImageAnnotation]
	if !ok {
		return nil, fmt.Errorf("VolumeImportSource '%s' has no '%s' annotation: %w", vis.Name, image.K8sImageAnnotation, apperrors.ErrInternal)
	}
	img := &imagev1.Image{}
	if err := protojson.Unmarshal([]byte(raw), img); err != nil {
		return nil, fmt.Errorf("unmarshal image of VolumeImportSource '%s': %w", vis.Name, err)
	}
	img.ImageId = vis.Labels[image.K8sImageIdLabel]
	img.CreatedAt = timestamppb.New(vis.CreationTimestamp.Time)
	img.UpdatedAt = img.CreatedAt
	if t := misc.GetLastUpdateTime(dv); t != nil {
		img.UpdatedAt = timestamppb.New(*t)
	}
	img.Status = imageStatus(dv.Status.Phase)
	switch img.Status {
	case imagev1.ImageStatus_IMAGE_STATUS_IMPORTING:
		img.Progress = progressPercent(dv.Status.Progress)
	case imagev1.ImageStatus_IMAGE_STATUS_AVAILABLE:
		if dv.Spec.Storage != nil {
			img.Size = dv.Spec.Storage.Resources.Requests.Storage().String()
		}
	case imagev1.ImageStatus_IMAGE_STATUS_FAILED:
		img.StatusDetail = runningMessage(dv)
	}
	if img.Metadata == nil {
		img.Metadata = map[string]string{}
	}
	img.Metadata[image.PvcMetadataKey] = dv.Name
	return img, nil
}

// imageStatus maps a DataVolume phase to an image status.
func imageStatus(phase v1beta1.DataVolumePhase) imagev1.ImageStatus {
	switch phase {
	case v1beta1.Succeeded:
		return imagev1.ImageStatus_IMAGE_STATUS_AVAILABLE
	case v1beta1.Failed:
		return imagev1.ImageStatus_IMAGE_STATUS_FAILED
	case v1beta1.ImportScheduled, v1beta1.ImportInProgress:
		return imagev1.ImageStatus_IMAGE_STATUS_IMPORTING
	case v1beta1.UploadScheduled, v1beta1.UploadReady:
		return imagev1.ImageStatus_IMAGE_STATUS_UPLOAD_PENDING
	default:
		return imagev1.ImageStatus_IMAGE_STATUS_PENDING
	}
}

// progressPercent parses CDI progress ("45.67%"); 0 when it is not a percentage.
func progressPercent(p v1beta1.DataVolumeProgress) int32 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(string(p), "%"), 64)
	if err != nil {
		return 0
	}
	return int32(v)
}

// runningMessage returns the message of dv's Running condition, which carries the import failure.
func runningMessage(dv *v1beta1.DataVolume) string {
	for _, c := range dv.Status.Conditions {
		if c.Type == v1beta1.DataVolumeRunning {
			return c.Message
		}
	}
	return ""
}

// getStorageClass retrieves a StorageClass by name.
// If the name is "default", it returns the cluster's default StorageClass.
// StorageClasses are cluster-scoped and not kube-nfv-owned, so this reads through
// the uncached reader (apiReader) rather than the shared cache.
func getStorageClass(ctx context.Context, name string, reader client.Reader) (*storagev1.StorageClass, error) {
	if name == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "name", Reason: "can't be empty"}
	}

	if name != "default" {
		// Get StorageClass by exact name
		sc := &storagev1.StorageClass{}
		if err := reader.Get(ctx, client.ObjectKey{Name: name}, sc); err != nil {
			return nil, fmt.Errorf("failed to get storage class %q: %w", name, err)
		}
		return sc, nil
	}
	// If "default", find the StorageClass annotated as default
	scList := &storagev1.StorageClassList{}
	if err := reader.List(ctx, scList); err != nil {
		return nil, fmt.Errorf("failed to list storage classes: %w", err)
	}

	for idx := range scList.Items {
		annotations := scList.Items[idx].Annotations
		if annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
			return &scList.Items[idx], nil
		}
	}

	return nil, &apperrors.ErrNotFound{Entity: "storageClass", Identifier: "default"}
}
