package auth

import (
	"testing"

	_ "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
)

func TestAuthorize(t *testing.T) {
	const getFlavour = "/kubenfv.kubevim.flavour.v1.FlavourService/GetFlavour"
	tests := []struct {
		name       string
		scopes     []string
		method     string
		wantDenied bool
	}{
		{name: "scope granted", scopes: []string{"image:v1:query", "flavour:v1:query"}, method: getFlavour},
		{name: "scope missing", scopes: []string{"flavour:v1:create"}, method: getFlavour, wantDenied: true},
		{name: "no scopes", method: getFlavour, wantDenied: true},
		{name: "RPC without scopes", method: "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"},
		{name: "unknown RPC", method: "/unknown.Service/Method"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := authorize(&Principal{Scopes: tt.scopes}, tt.method)
			if tt.wantDenied {
				var denied *apperrors.ErrPermissionDenied
				assert.ErrorAs(t, err, &denied)
				return
			}
			assert.NoError(t, err)
		})
	}
}
