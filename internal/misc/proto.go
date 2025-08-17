package misc

import (
	"fmt"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Marshal marshals src (interface{}) into dst (*anypb.Any).
func MarshalAny(dst *anypb.Any, src interface{}) error {
	if dst == nil {
		return &apperrors.ErrInvalidArgument{Field: "destination", Reason: "cannot be nil"}
	}

	var message protoreflect.ProtoMessage

	switch v := src.(type) {
	case string:
		message = wrapperspb.String(v)
	case int32:
		message = wrapperspb.Int32(v)
	case int64:
		message = wrapperspb.Int64(v)
	case float32:
		message = wrapperspb.Float(v)
	case float64:
		message = wrapperspb.Double(v)
	case bool:
		message = wrapperspb.Bool(v)
	case protoreflect.ProtoMessage:
		message = v
	default:
		return &apperrors.ErrInvalidArgument{Field: "source type", Reason: fmt.Sprintf("unsupported type '%T' for marshaling", src)}
	}
	var err error
	dst, err = anypb.New(message)
	if err != nil {
		return fmt.Errorf("create protobuf Any from type '%T': %w", src, err)
	}
	return nil
}

func UnmarshalAny(src *anypb.Any, dst interface{}) error {
	if src == nil {
		return &apperrors.ErrInvalidArgument{Field: "source", Reason: "cannot be nil"}
	}
	if dst == nil {
		return &apperrors.ErrInvalidArgument{Field: "destination", Reason: "cannot be nil"}
	}

	switch d := dst.(type) {
	case *string:
		var wrapper wrapperspb.StringValue
		if err := src.UnmarshalTo(&wrapper); err != nil {
			return fmt.Errorf("unmarshal Any to string: %w", err)
		}
		*d = wrapper.Value
	case *int32:
		var wrapper wrapperspb.Int32Value
		if err := src.UnmarshalTo(&wrapper); err != nil {
			return fmt.Errorf("unmarshal Any to int32: %w", err)
		}
		*d = wrapper.Value
	case *int64:
		var wrapper wrapperspb.Int64Value
		if err := src.UnmarshalTo(&wrapper); err != nil {
			return fmt.Errorf("unmarshal Any to int64: %w", err)
		}
		*d = wrapper.Value
	case *float32:
		var wrapper wrapperspb.FloatValue
		if err := src.UnmarshalTo(&wrapper); err != nil {
			return fmt.Errorf("unmarshal Any to float32: %w", err)
		}
		*d = wrapper.Value
	case *float64:
		var wrapper wrapperspb.DoubleValue
		if err := src.UnmarshalTo(&wrapper); err != nil {
			return fmt.Errorf("unmarshal Any to float64: %w", err)
		}
		*d = wrapper.Value
	case *bool:
		var wrapper wrapperspb.BoolValue
		if err := src.UnmarshalTo(&wrapper); err != nil {
			return fmt.Errorf("unmarshal Any to bool: %w", err)
		}
		*d = wrapper.Value
	case protoreflect.ProtoMessage:
		if err := src.UnmarshalTo(d); err != nil {
			return fmt.Errorf("unmarshal Any to proto.Message: %w", err)
		}
	default:
		return &apperrors.ErrInvalidArgument{Field: "destination type", Reason: fmt.Sprintf("unsupported type '%T' for unmarshaling", dst)}
	}
	return nil
}
