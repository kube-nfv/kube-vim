package validation

import (
	"context"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// UnaryServerInterceptor rejects requests that break the API's protovalidate rules.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if msg, ok := req.(proto.Message); ok {
			if err := validate(msg); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}
}

func validate(msg proto.Message) error {
	err := protovalidate.Validate(msg)
	if err == nil {
		return nil
	}
	var verr *protovalidate.ValidationError
	if errors.As(err, &verr) {
		return &apperrors.ErrInvalidArgument{Field: "request", Reason: verr.Error()}
	}
	return fmt.Errorf("validate request: %w", err)
}
