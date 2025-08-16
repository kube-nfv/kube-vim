package errors

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// Untyped errors for simple cases that don't need additional fields
var (
	ErrNotImplemented = errors.New("not implemented")
	ErrUnsupported    = errors.New("unsupported")
)

// Typed errors for cases that benefit from carrying additional fields

// ErrNotFound for cases where entity and identifier matter
type ErrNotFound struct {
	Entity     string
	Identifier string
}

func (e *ErrNotFound) Error() string {
	if e.Identifier != "" {
		return fmt.Sprintf("%s '%s' not found", e.Entity, e.Identifier)
	}
	return fmt.Sprintf("%s not found", e.Entity)
}

// ErrInvalidArgument for cases where field and reason matter
type ErrInvalidArgument struct {
	Field  string
	Reason string
}

func (e *ErrInvalidArgument) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
	}
	return fmt.Sprintf("invalid %s", e.Field)
}

// ErrAlreadyExists for cases where entity and identifier matter
type ErrAlreadyExists struct {
	Entity     string
	Identifier string
}

func (e *ErrAlreadyExists) Error() string {
	if e.Identifier != "" {
		return fmt.Sprintf("%s '%s' already exists", e.Entity, e.Identifier)
	}
	return fmt.Sprintf("%s already exists", e.Entity)
}

// ErrPermissionDenied for cases where resource and reason matter
type ErrPermissionDenied struct {
	Resource string
	Reason   string
}

func (e *ErrPermissionDenied) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("access denied to %s: %s", e.Resource, e.Reason)
	}
	return fmt.Sprintf("access denied to %s", e.Resource)
}

// ErrK8sObjectNotInstantiated indicates that the object is not from Kubernetes
type ErrK8sObjectNotInstantiated struct {
	ObjectType string
}

func (e *ErrK8sObjectNotInstantiated) Error() string {
	return fmt.Sprintf("%s is not from Kubernetes (likely created manually)", e.ObjectType)
}

// ErrK8sObjectNotManagedByKubeNfv indicates that the object was not created by kube-nfv
type ErrK8sObjectNotManagedByKubeNfv struct {
	ObjectType string
	ObjectName string
	ObjectId   string
}

func (e *ErrK8sObjectNotManagedByKubeNfv) Error() string {
	return fmt.Sprintf("%s '%s' (uid: %s) not managed by kube-nfv", e.ObjectType, e.ObjectName, e.ObjectId)
}

// ToGRPCError converts any error to a gRPC status error, preserving error chains
func ToGRPCError(err error) error {
	if err == nil {
		return nil
	}

	// If already a gRPC status error, return as-is
	if _, ok := status.FromError(err); ok {
		return err
	}

	// Check for typed errors
	var notFoundErr *ErrNotFound
	if errors.As(err, &notFoundErr) {
		return status.Error(codes.NotFound, err.Error())
	}

	var invalidArgErr *ErrInvalidArgument
	if errors.As(err, &invalidArgErr) {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	var alreadyExistsErr *ErrAlreadyExists
	if errors.As(err, &alreadyExistsErr) {
		return status.Error(codes.AlreadyExists, err.Error())
	}

	var permissionDeniedErr *ErrPermissionDenied
	if errors.As(err, &permissionDeniedErr) {
		return status.Error(codes.PermissionDenied, err.Error())
	}

	var k8sNotInstantiated *ErrK8sObjectNotInstantiated
	if errors.As(err, &k8sNotInstantiated) {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	var k8sNotManaged *ErrK8sObjectNotManagedByKubeNfv
	if errors.As(err, &k8sNotManaged) {
		return status.Error(codes.PermissionDenied, err.Error())
	}

	// Check for untyped errors anywhere in the chain
	if errors.Is(err, ErrNotImplemented) {
		return status.Error(codes.Unimplemented, err.Error())
	}
	if errors.Is(err, ErrUnsupported) {
		return status.Error(codes.Unimplemented, err.Error())
	}

	// Check for K8s API errors anywhere in the chain
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

	// All other errors become Internal
	return status.Errorf(codes.Internal, "%v", err)
}