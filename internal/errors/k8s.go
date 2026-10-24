package errors

import (
	"errors"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	"google.golang.org/grpc/codes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// K8sErrorConverter handles Kubernetes API errors and kube-nfv specific k8s errors
type K8sErrorConverter struct{}

// ConvertToGrpcError converts k8s-specific errors to gRPC status errors
func (c *K8sErrorConverter) ConvertToGrpcError(err error) error {
	if err == nil {
		return nil
	}

	// Check for kube-nfv specific k8s errors
	var k8sNotInstantiated *ErrK8sObjectNotInstantiated
	if errors.As(err, &k8sNotInstantiated) {
		return reasonStatus(codes.Internal, commonv1.ErrorReason_ERROR_REASON_INTERNAL, err.Error())
	}

	// Objects kube-vim does not own are invisible to clients.
	var k8sNotManaged *ErrK8sObjectNotManagedByKubeNfv
	if errors.As(err, &k8sNotManaged) {
		return reasonStatus(codes.NotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND, err.Error())
	}

	// Check for standard K8s API errors anywhere in the chain
	if k8serrors.IsNotFound(err) {
		return reasonStatus(codes.NotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND, err.Error())
	}
	if k8serrors.IsInvalid(err) || k8serrors.IsBadRequest(err) {
		return reasonStatus(codes.InvalidArgument, commonv1.ErrorReason_ERROR_REASON_VALIDATION_FAILED, err.Error())
	}
	if k8serrors.IsServiceUnavailable(err) || k8serrors.IsTimeout(err) || k8serrors.IsServerTimeout(err) || k8serrors.IsTooManyRequests(err) {
		return reasonStatus(codes.Unavailable, commonv1.ErrorReason_ERROR_REASON_BACKEND_UNAVAILABLE, err.Error())
	}
	// AlreadyExists, Forbidden and Unauthorized come from kube-vim's own names and credentials.
	if k8serrors.IsAlreadyExists(err) || k8serrors.IsForbidden(err) || k8serrors.IsUnauthorized(err) {
		return reasonStatus(codes.Internal, commonv1.ErrorReason_ERROR_REASON_INTERNAL, err.Error())
	}

	// Return nil if this is not a k8s error (let other converters handle it)
	return nil
}

func init() {
	// Register the k8s error converter
	RegisterErrorConverter(&K8sErrorConverter{})
}
