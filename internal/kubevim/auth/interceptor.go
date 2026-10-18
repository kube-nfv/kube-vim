package auth

import (
	"context"
	"strings"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// UnaryServerInterceptor rejects requests without a valid bearer token and puts the Principal in the context.
func UnaryServerInterceptor(a Authenticator, log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := authenticate(ctx, a)
		if err != nil {
			log.Warn("Request rejected", zap.String("Request", info.FullMethod), zap.Error(err))
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is the streaming counterpart of UnaryServerInterceptor.
func StreamServerInterceptor(a Authenticator, log *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := authenticate(ss.Context(), a)
		if err != nil {
			log.Warn("Request rejected", zap.String("Request", info.FullMethod), zap.Error(err))
			return err
		}
		return handler(srv, &serverStream{ServerStream: ss, ctx: ctx})
	}
}

func authenticate(ctx context.Context, a Authenticator) (context.Context, error) {
	token, err := bearerToken(ctx)
	if err != nil {
		return nil, err
	}
	p, err := a.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	return NewContext(ctx, p), nil
}

// bearerToken reads the RFC 6750 bearer token from the "authorization" metadata.
func bearerToken(ctx context.Context) (string, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", &apperrors.ErrUnauthenticated{}
	}
	scheme, token, _ := strings.Cut(values[0], " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", &apperrors.ErrUnauthenticated{}
	}
	if token = strings.TrimSpace(token); token == "" {
		return "", &apperrors.ErrUnauthenticated{Reason: "empty bearer token"}
	}
	return token, nil
}

type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context {
	return s.ctx
}
