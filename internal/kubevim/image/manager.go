package image

import (
	"context"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	K8sImageIdLabel = "image.kubevim.kubenfv.io/id"
	// K8sImageAnnotation holds the image as the consumer created it (protojson, without its id and input-only fields).
	K8sImageAnnotation = "image.kubevim.kubenfv.io/image"

	// PvcMetadataKey is the metadata key kube-vim adds to an image: the PVC compute clones boot disks from.
	PvcMetadataKey = "kubevim.kubenfv.io/pvc"
)

//go:generate go run go.uber.org/mock/mockgen -source=manager.go -destination=mock/mock_manager.go -package=mock

type Manager interface {
	CreateImage(context.Context, *imagev1.Image) (*imagev1.Image, error)
	GetImage(ctx context.Context, imageId string) (*imagev1.Image, error)
	ListImages(context.Context) ([]*imagev1.Image, error)
}

// Source is one kind of image source (an ImageSource oneof member), imported into the cluster through CDI.
type Source interface {
	// ImportSource returns the CDI import source for src; ErrUnsupported for fields it does not handle.
	ImportSource(ctx context.Context, src *imagev1.ImageSource) (*cdiv1beta1.ImportSourceType, error)
	// Stored returns src as kept and returned to clients, without input-only fields.
	Stored(src *imagev1.ImageSource) *imagev1.ImageSource
}

// SourceKind returns the name of the ImageSource oneof member set in src (e.g. "http"), or "" when none is.
func SourceKind(src *imagev1.ImageSource) string {
	if src == nil {
		return ""
	}
	m := src.ProtoReflect()
	f := m.WhichOneof(m.Descriptor().Oneofs().ByName("source"))
	if f == nil {
		return ""
	}
	return string(f.Name())
}
