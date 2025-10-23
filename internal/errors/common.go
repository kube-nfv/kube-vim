package errors

import (
	"errors"

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

	// Check for common untyped errors anywhere in the chain
	if errors.Is(err, ErrNotImplemented) {
		return status.Error(codes.Unimplemented, err.Error())
	}
	if errors.Is(err, ErrUnsupported) {
		return status.Error(codes.Unimplemented, err.Error())
	}
	if errors.Is(err, ErrInternal) {
		return status.Error(codes.Internal, err.Error())
	}

	// Return nil if this is not a common error (let other converters handle it)
	return nil
}

func init() {
	// Register the common error converter
	RegisterErrorConverter(&CommonErrorConverter{})
}
