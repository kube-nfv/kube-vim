package errors

import (
	"strings"
	"sync"

	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorDomain is the ErrorInfo domain of every kube-vim error.
const ErrorDomain = "kubevim.kubenfv.io"

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
	return reasonStatus(codes.Internal, commonv1.ErrorReason_ERROR_REASON_INTERNAL, err.Error())
}

// reasonStatus builds a gRPC status error with an ErrorInfo detail carrying the v1 error reason.
func reasonStatus(code codes.Code, reason commonv1.ErrorReason, msg string) error {
	st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{
		Reason: strings.TrimPrefix(reason.String(), "ERROR_REASON_"),
		Domain: ErrorDomain,
	})
	if err != nil {
		return status.Error(code, msg)
	}
	return st.Err()
}
