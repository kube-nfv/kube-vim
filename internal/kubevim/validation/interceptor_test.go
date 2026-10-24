package validation

import (
	"context"
	"testing"

	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func TestUnaryServerInterceptor(t *testing.T) {
	tests := []struct {
		name        string
		req         any
		wantInvalid bool
	}{
		{name: "valid request", req: &flavourv1.GetFlavourRequest{FlavourId: "0b4e7f9a-3c1d-4e8b-9f2a-6d5c4b3a2918"}},
		{name: "invalid request", req: &flavourv1.GetFlavourRequest{FlavourId: "not-a-uuid"}, wantInvalid: true},
		{name: "non-proto request passes", req: "raw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := func(ctx context.Context, req any) (any, error) {
				called = true
				return nil, nil
			}
			_, err := UnaryServerInterceptor()(context.Background(), tt.req, &grpc.UnaryServerInfo{}, handler)
			if tt.wantInvalid {
				var invalid *apperrors.ErrInvalidArgument
				assert.ErrorAs(t, err, &invalid)
				assert.False(t, called)
				return
			}
			assert.NoError(t, err)
			assert.True(t, called)
		})
	}
}
