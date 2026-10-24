package errors

import (
	"errors"
	"fmt"
	"strings"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CommonErrorConverter handles the common structured errors and untyped errors
type CommonErrorConverter struct{}

// ConvertToGrpcError converts common errors to gRPC status errors
func (c *CommonErrorConverter) ConvertToGrpcError(err error) error {
	if err == nil {
		return nil
	}

	// Check for common structured errors
	var notFoundErr *ErrNotFound
	if errors.As(err, &notFoundErr) {
		return reasonStatus(codes.NotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND, err.Error())
	}

	var invalidArgErr *ErrInvalidArgument
	if errors.As(err, &invalidArgErr) {
		return reasonStatus(codes.InvalidArgument, commonv1.ErrorReason_ERROR_REASON_VALIDATION_FAILED, err.Error())
	}

	// Kube-vim generates every Kubernetes name, so a clash is an internal error.
	var alreadyExistsErr *ErrAlreadyExists
	if errors.As(err, &alreadyExistsErr) {
		return reasonStatus(codes.Internal, commonv1.ErrorReason_ERROR_REASON_INTERNAL, err.Error())
	}

	var unauthenticatedErr *ErrUnauthenticated
	if errors.As(err, &unauthenticatedErr) {
		return status.Error(codes.Unauthenticated, bearerChallenge(unauthenticatedErr.Reason))
	}

	var permissionDeniedErr *ErrPermissionDenied
	if errors.As(err, &permissionDeniedErr) {
		return status.Error(codes.PermissionDenied, err.Error())
	}

	// Check for common untyped errors anywhere in the chain
	if errors.Is(err, ErrNotImplemented) {
		return reasonStatus(codes.Unimplemented, commonv1.ErrorReason_ERROR_REASON_NOT_IMPLEMENTED, err.Error())
	}
	if errors.Is(err, ErrUnsupported) {
		return reasonStatus(codes.InvalidArgument, commonv1.ErrorReason_ERROR_REASON_UNSUPPORTED_FIELD, err.Error())
	}
	if errors.Is(err, ErrInvalidFilter) {
		return reasonStatus(codes.InvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_FILTER, err.Error())
	}
	if errors.Is(err, ErrInternal) {
		return reasonStatus(codes.Internal, commonv1.ErrorReason_ERROR_REASON_INTERNAL, err.Error())
	}

	// Return nil if this is not a common error (let other converters handle it)
	return nil
}

// bearerChallenge formats an RFC 6750 WWW-Authenticate challenge; the gateway copies it into that header.
func bearerChallenge(reason string) string {
	if reason == "" {
		return `Bearer realm="kube-vim"`
	}
	desc := strings.NewReplacer(`"`, "'", `\`, "/").Replace(reason)
	return fmt.Sprintf(`Bearer realm="kube-vim", error="invalid_token", error_description="%s"`, desc)
}

func init() {
	// Register the common error converter
	RegisterErrorConverter(&CommonErrorConverter{})
}
