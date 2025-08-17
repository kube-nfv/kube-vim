package errors

import (
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorConverter is an interface for module-specific error converters
type ErrorConverter interface {
	ConvertToGrpcError(err error) error
}

var (
	converters []ErrorConverter
	mu         sync.RWMutex
)

// RegisterErrorConverter registers a module-specific error converter
func RegisterErrorConverter(converter ErrorConverter) {
	mu.Lock()
	defer mu.Unlock()
	converters = append(converters, converter)
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

	// Check all registered error converters
	mu.RLock()
	for _, converter := range converters {
		if grpcErr := converter.ConvertToGrpcError(err); grpcErr != nil {
			mu.RUnlock()
			return grpcErr
		}
	}
	mu.RUnlock()

	// All other errors become Internal
	return status.Errorf(codes.Internal, "%v", err)
}
