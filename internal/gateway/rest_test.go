package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

const imageID = "6f0d3c1e-8a52-4a8e-9a43-2f1d6b1c7e10"

// imageService answers the image API: one image, Update left unimplemented.
type imageService struct {
	imagev1.UnimplementedImageServiceServer
}

func (imageService) CreateImage(_ context.Context, req *imagev1.CreateImageRequest) (*imagev1.Image, error) {
	img := req.GetImage()
	img.ImageId = imageID
	return img, nil
}

func (imageService) GetImage(_ context.Context, req *imagev1.GetImageRequest) (*imagev1.Image, error) {
	if req.GetImageId() != imageID {
		return nil, &apperrors.ErrNotFound{Entity: "image", Identifier: req.GetImageId()}
	}
	return &imagev1.Image{ImageId: imageID}, nil
}

func (imageService) ListImages(context.Context, *imagev1.ListImagesRequest) (*imagev1.ListImagesResponse, error) {
	return &imagev1.ListImagesResponse{}, nil
}

func (imageService) DeleteImage(context.Context, *imagev1.DeleteImageRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

// tokenAuthenticator accepts only the token "good".
type tokenAuthenticator struct{}

func (tokenAuthenticator) Verify(_ context.Context, rawToken string) (*auth.Principal, error) {
	if rawToken == "good" {
		return &auth.Principal{ClientID: "osm", Scopes: []string{"image:v1:query"}}, nil
	}
	return nil, &apperrors.ErrUnauthenticated{Reason: "invalid token"}
}

// newTestGateway serves the image API through the gateway, with kube-vim's error conversion
// and the given extra interceptors, and returns the gateway URL.
func newTestGateway(t *testing.T, interceptors ...grpc.UnaryServerInterceptor) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	convert := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		return resp, apperrors.ToGRPCError(err)
	}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(append([]grpc.UnaryServerInterceptor{convert}, interceptors...)...))
	imagev1.RegisterImageServiceServer(srv, imageService{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	mux := newServeMux()
	require.NoError(t, registerHandlers(context.Background(), mux, conn))
	gw := httptest.NewServer(withRequestPath(mux))
	t.Cleanup(gw.Close)
	return gw.URL
}

func do(t *testing.T, method, url, body, authorization string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(b)
}

func TestRest(t *testing.T) {
	gw := newTestGateway(t)
	tests := []struct {
		name         string
		method, path string
		body         string
		wantStatus   int
		wantBody     string // exact body, for success responses
		wantHeader   map[string]string
		wantProblem  string // ProblemDetails type
	}{
		{name: "create is 201 with Location", method: http.MethodPost, path: "/image/v1/images", body: `{"image":{"name":"cirros"}}`,
			wantStatus: http.StatusCreated, wantHeader: map[string]string{"Location": "/image/v1/images/" + imageID}},
		{name: "get", method: http.MethodGet, path: "/image/v1/images/" + imageID,
			wantStatus: http.StatusOK, wantBody: `{"imageId":"` + imageID + `"}`},
		{name: "empty list is an empty array", method: http.MethodGet, path: "/image/v1/images",
			wantStatus: http.StatusOK, wantBody: `[]`},
		{name: "delete is 204 without body", method: http.MethodDelete, path: "/image/v1/images/" + imageID,
			wantStatus: http.StatusNoContent, wantBody: ``},
		{name: "not found", method: http.MethodGet, path: "/image/v1/images/other",
			wantStatus: http.StatusNotFound, wantProblem: problemTypePrefix + "not-found"},
		{name: "unimplemented is 501", method: http.MethodPatch, path: "/image/v1/images/" + imageID, body: `{}`,
			wantStatus: http.StatusNotImplemented, wantProblem: problemTypePrefix + "not-implemented"},
		{name: "unknown body field is rejected", method: http.MethodPost, path: "/image/v1/images", body: `{"image":{"bogus":1}}`,
			wantStatus: http.StatusBadRequest, wantProblem: "about:blank"},
		{name: "unknown path", method: http.MethodGet, path: "/vivnfm/v5/images",
			wantStatus: http.StatusNotFound, wantProblem: "about:blank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := do(t, tt.method, gw+tt.path, tt.body, "")
			require.Equal(t, tt.wantStatus, resp.StatusCode, body)
			for k, v := range tt.wantHeader {
				assert.Equal(t, v, resp.Header.Get(k), k)
			}
			if tt.wantProblem == "" {
				if tt.wantBody != "" || tt.wantStatus == http.StatusNoContent {
					assert.Equal(t, tt.wantBody, body)
				}
				return
			}
			assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
			var p problemDetails
			require.NoError(t, json.Unmarshal([]byte(body), &p))
			assert.Equal(t, tt.wantProblem, p.Type)
			assert.Equal(t, tt.wantStatus, p.Status)
			assert.NotEmpty(t, p.Detail)
		})
	}
}

// TestGatewayForwardsBearerToken checks that the gateway passes Authorization to kube-vim
// and turns its Unauthenticated status into a 401 with an RFC 6750 challenge.
func TestGatewayForwardsBearerToken(t *testing.T) {
	gw := newTestGateway(t, auth.UnaryServerInterceptor(tokenAuthenticator{}, zap.NewNop()))
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
		wantChallenge string
		wantDetail    string
	}{
		{name: "no token", wantStatus: http.StatusUnauthorized, wantChallenge: `Bearer realm="kube-vim"`,
			wantDetail: "Authentication required"},
		{name: "invalid token", authorization: "Bearer bad", wantStatus: http.StatusUnauthorized,
			wantChallenge: `Bearer realm="kube-vim", error="invalid_token", error_description="invalid token"`,
			wantDetail:    "invalid token"},
		{name: "valid token", authorization: "Bearer good", wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := do(t, http.MethodGet, gw+"/image/v1/images", "", tt.authorization)
			assert.Equal(t, tt.wantStatus, resp.StatusCode, body)
			assert.Equal(t, tt.wantChallenge, resp.Header.Get("WWW-Authenticate"))
			if tt.wantDetail != "" {
				var p problemDetails
				require.NoError(t, json.Unmarshal([]byte(body), &p))
				assert.Equal(t, tt.wantDetail, p.Detail, "the reason, not the challenge")
			}
		})
	}
}
