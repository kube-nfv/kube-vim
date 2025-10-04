package errors

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestToGRPCError_StructuredErrors(t *testing.T) {
	tests := []struct {
		name         string
		inputError   error
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "NotFoundError with entity and identifier",
			inputError:   &ErrNotFound{Entity: "flavour", Identifier: "abc123"},
			expectedCode: codes.NotFound,
			expectedMsg:  "flavour 'abc123' not found",
		},
		{
			name:         "NotFoundError with entity only",
			inputError:   &ErrNotFound{Entity: "flavour"},
			expectedCode: codes.NotFound,
			expectedMsg:  "flavour not found",
		},
		{
			name:         "InvalidArgumentError with field and reason",
			inputError:   &ErrInvalidArgument{Field: "flavour id", Reason: "required"},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "invalid flavour id: required",
		},
		{
			name:         "InvalidArgumentError with field only",
			inputError:   &ErrInvalidArgument{Field: "flavour"},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "invalid flavour",
		},
		{
			name:         "AlreadyExistsError with entity and identifier",
			inputError:   &ErrAlreadyExists{Entity: "flavour", Identifier: "xyz789"},
			expectedCode: codes.AlreadyExists,
			expectedMsg:  "flavour 'xyz789' already exists",
		},
		{
			name:         "PermissionDeniedError with resource and reason",
			inputError:   &ErrPermissionDenied{Resource: "flavour", Reason: "not managed by kube-nfv"},
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "access denied to flavour: not managed by kube-nfv",
		},
		{
			name:         "K8sObjectNotInstantiated",
			inputError:   &ErrK8sObjectNotInstantiated{ObjectType: "VirtualMachineInstancetype"},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "VirtualMachineInstancetype is not from Kubernetes (likely created manually)",
		},
		{
			name:         "K8sObjectNotManagedByKubeNfv",
			inputError:   &ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachineInstancetype", ObjectName: "test-vm", ObjectId: "uid-123"},
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "VirtualMachineInstancetype 'test-vm' (uid: uid-123) not managed by kube-nfv",
		},
		{
			name:         "Untyped error - not implemented",
			inputError:   ErrNotImplemented,
			expectedCode: codes.Unimplemented,
			expectedMsg:  "not implemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToGRPCError(tt.inputError)

			// Check that result is a gRPC status error
			st, ok := status.FromError(result)
			if !ok {
				t.Errorf("ToGRPCError() did not return a gRPC status error")
				return
			}

			// Check expected code
			if st.Code() != tt.expectedCode {
				t.Errorf("ToGRPCError() code = %v, want %v", st.Code(), tt.expectedCode)
			}

			// Check expected message
			if st.Message() != tt.expectedMsg {
				t.Errorf("ToGRPCError() message = %q, want %q", st.Message(), tt.expectedMsg)
			}
		})
	}
}

func TestToGRPCError_WrappedStructuredErrors(t *testing.T) {
	// Test that structured errors work when wrapped with fmt.Errorf
	baseErr := &ErrNotFound{Entity: "flavour", Identifier: "test123"}
	wrappedErr := fmt.Errorf("failed to retrieve: %w", baseErr)

	result := ToGRPCError(wrappedErr)

	st, ok := status.FromError(result)
	if !ok {
		t.Fatal("ToGRPCError() did not return a gRPC status error")
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected codes.NotFound, got %v", st.Code())
	}

	expectedMsg := "failed to retrieve: flavour 'test123' not found"
	if st.Message() != expectedMsg {
		t.Errorf("Expected message %q, got %q", expectedMsg, st.Message())
	}

	// Verify original structured error can still be detected
	var originalErr *ErrNotFound
	if !errors.As(wrappedErr, &originalErr) {
		t.Errorf("Original structured error not detectable in wrapped error")
	}

	if originalErr.Entity != "flavour" || originalErr.Identifier != "test123" {
		t.Errorf("Original error fields not preserved: Entity=%s, Identifier=%s", originalErr.Entity, originalErr.Identifier)
	}
}

func TestToGRPCError_PreservesK8sErrors(t *testing.T) {
	k8sNotFoundErr := k8serrors.NewNotFound(schema.GroupResource{Resource: "flavours"}, "test-flavour")
	wrappedErr := fmt.Errorf("failed to retrieve flavour: %w", k8sNotFoundErr)

	result := ToGRPCError(wrappedErr)

	// Should convert to gRPC NotFound
	st, ok := status.FromError(result)
	if !ok {
		t.Fatal("ToGRPCError() did not return a gRPC status error")
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected codes.NotFound, got %v", st.Code())
	}

	// Original K8s error should still be detectable in the chain
	if !k8serrors.IsNotFound(wrappedErr) {
		t.Errorf("Original K8s NotFound error not detectable in chain")
	}
}
