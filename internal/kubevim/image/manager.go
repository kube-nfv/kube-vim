package image

import (
	"context"
	"fmt"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
)

const (
	K8sImageIdLabel  = "image.kubevim.kubenfv.io/id"
	K8sSourceLabel   = "image.kubevim.kubenfv.io/source"
	K8sIsUploadLabel = "image.kubevim.kubenfv.io/uploaded"

	K8sSourceUrlAnnotation  = "image.kubevim.kubenfv.io/source-url"
	K8sIsImageBoundToPvc    = "image.kubevim.kubenfv.io/is-pvc-bound"
	K8sImagePvcStorageClass = "image.kubevim.kubenfv.io/storage-class"
)

// NfvImageManager is the ETSI Vi-Vnfm image query surface. It is split out of
// Manager so it can be mocked: Manager also embeds the gRPC admin.AdminServer,
// whose forced mustEmbedUnimplementedAdminServer() cannot be satisfied by a
// generated mock. Tests double Manager with a type embedding
// admin.UnimplementedAdminServer plus a mock of this interface.
//
//go:generate go run go.uber.org/mock/mockgen -destination=mock/mock_manager.go -package=mock github.com/kube-nfv/kube-vim/internal/kubevim/image NfvImageManager
type NfvImageManager interface {
	// TODO: Change to be able to getImage by Name and source
	GetImage(context.Context, *nfvcommon.Identifier) (*vivnfm.SoftwareImageInformation, error)
	ListImages(context.Context) ([]*vivnfm.SoftwareImageInformation, error)
}

type Manager interface {
	// Admin Api
	admin.AdminServer

	// NFV Api
	NfvImageManager
}

type SourceType string

const (
	HTTP    SourceType = "http"
	HTTPS              = "https"
	Unknown            = ""
)

func SourceTypeFromString(sourceTypeStr string) (SourceType, error) {
	switch sourceTypeStr {
	case string(HTTPS):
		return HTTPS, nil
	case string(HTTP):
		return HTTP, nil
	default:
		return Unknown, fmt.Errorf("unknown source type '%s': %w", sourceTypeStr, apperrors.ErrUnsupported)
	}
}
