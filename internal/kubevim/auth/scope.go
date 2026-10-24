package auth

import (
	"fmt"
	"slices"
	"strings"

	options "github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv3/options"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// oauth2Scheme is the security scheme the API declares its scopes under.
const oauth2Scheme = "oauth2"

// authorize rejects p when it lacks a scope that the RPC fullMethod declares.
func authorize(p *Principal, fullMethod string) error {
	for _, scope := range requiredScopes(fullMethod) {
		if !slices.Contains(p.Scopes, scope) {
			return &apperrors.ErrPermissionDenied{Resource: fullMethod, Reason: fmt.Sprintf("token lacks scope '%s'", scope)}
		}
	}
	return nil
}

// requiredScopes returns the OAuth 2.0 scopes in the RPC's openapiv3_operation option; none for an RPC without it.
func requiredScopes(fullMethod string) []string {
	name := protoreflect.FullName(strings.ReplaceAll(strings.TrimPrefix(fullMethod, "/"), "/", "."))
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
	if err != nil {
		return nil
	}
	method, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return nil
	}
	op, _ := proto.GetExtension(method.Options(), options.E_Openapiv3Operation).(*options.Operation)
	var scopes []string
	for _, req := range op.GetSecurity() {
		scopes = append(scopes, req.GetSchemes()[oauth2Scheme].GetScopes()...)
	}
	return scopes
}
