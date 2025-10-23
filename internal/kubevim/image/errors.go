package image

import (
	"errors"
	"fmt"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// Untyped errors for simple cases
	ErrApplyOption = errors.New("apply option")
)

func init() {
	// Register the image module error converter
	apperrors.RegisterErrorConverter(&ImageErrorConverter{})
}

// ErrDataVolumeAlreadyExists indicates that a Data Volume already exists
type ErrDataVolumeAlreadyExists struct {
	Name string
}

func (e *ErrDataVolumeAlreadyExists) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("Data Volume '%s' already exists", e.Name)
	}
	return "Data Volume already exists"
}

// ErrDataVolumeNotFound indicates that a Data Volume was not found
type ErrDataVolumeNotFound struct {
	Name string
	UID  string
}

func (e *ErrDataVolumeNotFound) Error() string {
	if e.Name != "" && e.UID != "" {
		return fmt.Sprintf("Data Volume '%s' (uid: %s) not found", e.Name, e.UID)
	} else if e.Name != "" {
		return fmt.Sprintf("Data Volume '%s' not found", e.Name)
	} else if e.UID != "" {
		return fmt.Sprintf("Data Volume with uid '%s' not found", e.UID)
	}
	return "Data Volume not found"
}

// ImageErrorConverter implements the ErrorConverter interface for image module errors
type ImageErrorConverter struct{}

// ConvertToGrpcError converts image module specific errors to gRPC status errors
func (c *ImageErrorConverter) ConvertToGrpcError(err error) error {
	if err == nil {
		return nil
	}

	// Check for image module specific structured errors
	var dvAlreadyExists *ErrDataVolumeAlreadyExists
	if errors.As(err, &dvAlreadyExists) {
		return status.Error(codes.AlreadyExists, err.Error())
	}

	var dvNotFound *ErrDataVolumeNotFound
	if errors.As(err, &dvNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}

	// Check for image module specific untyped errors
	if errors.Is(err, ErrApplyOption) {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	// Return nil if this is not an image module error (let main handler deal with it)
	return nil
}
