package errors

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		return status.Error(codes.InvalidArgument, err.Error())
	}

	var k8sNotManaged *ErrK8sObjectNotManagedByKubeNfv
	if errors.As(err, &k8sNotManaged) {
		return status.Error(codes.PermissionDenied, err.Error())
	}

	// Check for standard K8s API errors anywhere in the chain
	if k8serrors.IsNotFound(err) {
		return status.Error(codes.NotFound, err.Error())
	}
	if k8serrors.IsAlreadyExists(err) {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	if k8serrors.IsInvalid(err) || k8serrors.IsBadRequest(err) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if k8serrors.IsForbidden(err) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if k8serrors.IsUnauthorized(err) {
		return status.Error(codes.Unauthenticated, err.Error())
	}

	// Return nil if this is not a k8s error (let other converters handle it)
	return nil
}

func init() {
	// Register the k8s error converter
	RegisterErrorConverter(&K8sErrorConverter{})
}
