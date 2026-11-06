package auth

import (
	"strings"
	"testing"

	_ "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	_ "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	_ "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	_ "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	_ "github.com/kube-nfv/kube-vim-api/pkg/apis/storage/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// An RPC that declares no scope is open to any valid token, so every v1 RPC must declare one.
func TestEveryRpcDeclaresAScope(t *testing.T) {
	services, methods := 0, 0
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(file.Package()), "kubenfv.kubevim.") {
			return true
		}
		for i := range file.Services().Len() {
			service := file.Services().Get(i)
			services++
			for j := range service.Methods().Len() {
				method := service.Methods().Get(j)
				methods++
				assert.NotEmpty(t, requiredScopes("/"+string(service.FullName())+"/"+string(method.Name())), "%s declares no scope", method.FullName())
			}
		}
		return true
	})
	assert.Equal(t, 5, services)
	t.Logf("checked %d RPCs", methods)
}

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
