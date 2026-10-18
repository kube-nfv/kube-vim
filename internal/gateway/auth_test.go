package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// tokenAuthenticator accepts only the token "good".
type tokenAuthenticator struct{}

func (tokenAuthenticator) Verify(_ context.Context, rawToken string) (*auth.Principal, error) {
	if rawToken == "good" {
		return &auth.Principal{ClientID: "osm"}, nil
	}
	return nil, &apperrors.ErrUnauthenticated{Reason: "invalid token"}
}

type imagesServer struct {
	vivnfm.UnimplementedViVnfmServer
}

func (imagesServer) QueryImages(context.Context, *vivnfm.QueryImagesRequest) (*vivnfm.QueryImagesResponse, error) {
	return &vivnfm.QueryImagesResponse{}, nil
}

// TestGatewayForwardsBearerToken checks that the gateway passes Authorization to kube-vim
// and turns its Unauthenticated status into a 401 with an RFC 6750 challenge.
func TestGatewayForwardsBearerToken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			resp, err := handler(ctx, req)
			return resp, apperrors.ToGRPCError(err)
		},
		auth.UnaryServerInterceptor(tokenAuthenticator{}, zap.NewNop()),
	))
	vivnfm.RegisterViVnfmServer(srv, imagesServer{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	mux := runtime.NewServeMux()
	require.NoError(t, vivnfm.RegisterViVnfmHandler(context.Background(), mux, conn))

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
		wantChallenge string
	}{
		{name: "no token", wantStatus: http.StatusUnauthorized, wantChallenge: `Bearer realm="kube-vim"`},
		{name: "invalid token", authorization: "Bearer bad", wantStatus: http.StatusUnauthorized,
			wantChallenge: `Bearer realm="kube-vim", error="invalid_token", error_description="invalid token"`},
		{name: "valid token", authorization: "Bearer good", wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/vivnfm/v5/images", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tt.wantChallenge, rec.Header().Get("WWW-Authenticate"))
		})
	}
}
