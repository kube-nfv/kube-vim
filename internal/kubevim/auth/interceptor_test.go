package auth

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type verifyFunc func(ctx context.Context, rawToken string) (*Principal, error)

func (f verifyFunc) Verify(ctx context.Context, rawToken string) (*Principal, error) {
	return f(ctx, rawToken)
}

// stub accepts "good" and rejects every other token.
var stub = verifyFunc(func(_ context.Context, rawToken string) (*Principal, error) {
	if rawToken == "good" {
		return &Principal{ClientID: "osm"}, nil
	}
	return nil, &apperrors.ErrUnauthenticated{Reason: "invalid token"}
})

func withAuthorization(values ...string) context.Context {
	md := metadata.MD{}
	for _, v := range values {
		md.Append("authorization", v)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

var authTests = []struct {
	name       string
	ctx        context.Context
	wantClient string
	wantReason string
}{
	{name: "no metadata", ctx: context.Background()},
	{name: "no authorization header", ctx: withAuthorization()},
	{name: "other scheme", ctx: withAuthorization("Basic dXNlcjpwYXNz")},
	{name: "empty bearer token", ctx: withAuthorization("Bearer  "), wantReason: "empty bearer token"},
	{name: "rejected token", ctx: withAuthorization("Bearer bad"), wantReason: "invalid token"},
	{name: "valid token", ctx: withAuthorization("Bearer good"), wantClient: "osm"},
	{name: "scheme is case-insensitive", ctx: withAuthorization("bearer good"), wantClient: "osm"},
}

func TestUnaryServerInterceptor(t *testing.T) {
	interceptor := UnaryServerInterceptor(stub, zap.NewNop())
	info := &grpc.UnaryServerInfo{FullMethod: "/vivnfm.vi_vnfm/QueryImages"}
	for _, tt := range authTests {
		t.Run(tt.name, func(t *testing.T) {
			var got *Principal
			_, err := interceptor(tt.ctx, nil, info, func(ctx context.Context, _ any) (any, error) {
				got = PrincipalFromContext(ctx)
				return nil, nil
			})
			if tt.wantClient == "" {
				var unauth *apperrors.ErrUnauthenticated
				require.True(t, errors.As(err, &unauth), "got %v", err)
				assert.Equal(t, tt.wantReason, unauth.Reason)
				assert.Nil(t, got, "handler must not run")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantClient, got.ClientID)
		})
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *fakeStream) Context() context.Context { return s.ctx }

func TestStreamServerInterceptor(t *testing.T) {
	interceptor := StreamServerInterceptor(stub, zap.NewNop())
	info := &grpc.StreamServerInfo{FullMethod: "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"}
	for _, tt := range authTests {
		t.Run(tt.name, func(t *testing.T) {
			var got *Principal
			err := interceptor(nil, &fakeStream{ctx: tt.ctx}, info, func(_ any, ss grpc.ServerStream) error {
				got = PrincipalFromContext(ss.Context())
				return nil
			})
			if tt.wantClient == "" {
				var unauth *apperrors.ErrUnauthenticated
				require.True(t, errors.As(err, &unauth), "got %v", err)
				assert.Nil(t, got, "handler must not run")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantClient, got.ClientID)
		})
	}
}
