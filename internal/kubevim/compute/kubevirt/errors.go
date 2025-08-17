package kubevirt

import (
	"errors"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Compute-specific errors
var (
	ErrIPAMConfigurationMissing = errors.New("IPAM configuration should have either subnetId or staticIp configured")
)

// ComputeErrorConverter handles compute-specific errors
type ComputeErrorConverter struct{}

// ConvertToGrpcError converts compute-specific errors to gRPC status errors
func (c *ComputeErrorConverter) ConvertToGrpcError(err error) error {
	if err == nil {
		return nil
	}

	// Check for compute-specific errors
	if errors.Is(err, ErrIPAMConfigurationMissing) {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	// Return nil if this is not a compute-specific error (let other converters handle it)
	return nil
}

func init() {
	// Register the compute error converter
	apperrors.RegisterErrorConverter(&ComputeErrorConverter{})
}