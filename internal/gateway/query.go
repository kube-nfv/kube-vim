package gateway

import (
	"net/url"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/grpc-ecosystem/grpc-gateway/v2/utilities"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// gRPC-gateway custom query parameter parser to support complex query mappings.
// ex. map filter to nfv.Filter.value, paggination, etc.
type queryParameterParser struct{}

func (p *queryParameterParser) Parse(target proto.Message, values url.Values, filter *utilities.DoubleArray) error {
	queryFilter := values.Get("filter")
	if queryFilter != "" {
		populateQueryFilter(target, queryFilter)
	}
	return (&runtime.DefaultQueryParser{}).Parse(target, values, filter)
}

func populateQueryFilter(target proto.Message, filter string) {
	md := target.ProtoReflect()

	for i := 0; i < md.Descriptor().Fields().Len(); i++ {
		field := md.Descriptor().Fields().Get(i)

		if field.Kind() == protoreflect.MessageKind {
			// If the proto Message field is same as a nfv.Filter
			if field.Message() == (&nfv.Filter{}).ProtoReflect().Descriptor() {
				setFilter := &nfv.Filter{
					Value: filter,
				}

				md.Set(field, protoreflect.ValueOfMessage(setFilter.ProtoReflect()))
			}
		}
	}
}
