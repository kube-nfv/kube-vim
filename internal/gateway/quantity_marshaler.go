// Package gateway provides a custom marshaler for grpc-gateway that handles
// Kubernetes resource.Quantity fields correctly.
//
// Problem:
// The grpc-gateway uses protojson for marshaling/unmarshaling, which calls protobuf's
// Unmarshal() method instead of the JSON methods. This causes k8s.io/apimachinery/pkg/api/resource.Quantity
// fields to not be properly initialized, leaving their internal fields (i, d, s, Format) empty.
//
// The Quantity type has custom MarshalJSON/UnmarshalJSON methods that handle conversion
// between the internal representation and JSON string format (e.g., "2048M"), but protojson
// bypasses these methods entirely.
//
// Solution:
// This file implements a custom marshaler that wraps runtime.JSONPb and adds post-processing:
//
// Unmarshal (Request path):
//  1. Let protojson unmarshal normally (Quantity fields will be empty)
//  2. Parse original JSON to extract Quantity string values
//  3. Use reflection to find all Quantity fields in the protobuf message
//  4. Call Quantity.UnmarshalJSON() on each field to properly initialize internal fields
//
// Marshal (Response path):
//  1. Let protojson marshal normally (Quantity fields will be empty {} objects)
//  2. Use reflection to find all Quantity fields in the protobuf message
//  3. Call Quantity.MarshalJSON() to get the string representation
//  4. Replace empty {} with {"string": "2048M"} format in the JSON output
//
// This ensures Quantity fields work correctly in both REST API requests and responses
// while maintaining proper protobuf compatibility for enums, timestamps, and other types.
package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"k8s.io/apimachinery/pkg/api/resource"
)

// quantityMarshaler wraps runtime.JSONPb to handle k8s resource.Quantity fields correctly.
// It fixes Quantity marshaling/unmarshaling since protojson doesn't call Quantity's MarshalJSON/UnmarshalJSON methods.
type quantityMarshaler struct {
	*runtime.JSONPb
	logger *zap.Logger
}

func newQuantityMarshaler(logger *zap.Logger) *quantityMarshaler {
	return &quantityMarshaler{
		JSONPb: &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				EmitUnpopulated: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		},
		logger: logger,
	}
}

func (m *quantityMarshaler) Unmarshal(data []byte, v interface{}) error {
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		m.logger.Warn("Failed to parse JSON for Quantity extraction", zap.Error(err))
	}

	err := m.JSONPb.Unmarshal(data, v)
	if err != nil {
		m.logger.Error("Unmarshal failed", zap.Error(err), zap.String("json", string(data)))
		return err
	}

	// Fix Quantity fields using reflection and UnmarshalJSON
	if protoMsg, ok := v.(proto.Message); ok {
		if err := m.fixQuantityFields(protoMsg, jsonMap); err != nil {
			m.logger.Error("Failed to fix Quantity fields", zap.Error(err))
		}
	}

	return nil
}

func (m *quantityMarshaler) Marshal(v interface{}) ([]byte, error) {
	data, err := m.JSONPb.Marshal(v)
	if err != nil {
		m.logger.Error("Marshal failed", zap.Error(err))
		return nil, err
	}

	// Fix Quantity fields using reflection and MarshalJSON
	if protoMsg, ok := v.(proto.Message); ok {
		data, err = m.fixQuantityFieldsInJSON(protoMsg, data)
		if err != nil {
			m.logger.Error("Failed to fix Quantity fields in JSON output", zap.Error(err))
			return data, nil
		}
	}

	return data, nil
}

func (m *quantityMarshaler) NewDecoder(r io.Reader) runtime.Decoder {
	return &quantityDecoder{
		reader:    r,
		marshaler: m,
	}
}

func (m *quantityMarshaler) NewEncoder(w io.Writer) runtime.Encoder {
	return &quantityEncoder{
		writer:    w,
		marshaler: m,
	}
}

type quantityDecoder struct {
	reader    io.Reader
	marshaler *quantityMarshaler
}

func (d *quantityDecoder) Decode(v interface{}) error {
	data, err := io.ReadAll(d.reader)
	if err != nil {
		d.marshaler.logger.Error("Failed to read stream", zap.Error(err))
		return err
	}
	return d.marshaler.Unmarshal(data, v)
}

type quantityEncoder struct {
	writer    io.Writer
	marshaler *quantityMarshaler
}

func (e *quantityEncoder) Encode(v interface{}) error {
	data, err := e.marshaler.Marshal(v)
	if err != nil {
		return err
	}
	_, err = e.writer.Write(data)
	if err != nil {
		e.marshaler.logger.Error("Failed to write to stream", zap.Error(err))
	}
	return err
}

func getTypeName(v interface{}) string {
	if v == nil {
		return "nil"
	}
	switch t := v.(type) {
	case interface {
		ProtoReflect() interface {
			Descriptor() interface {
				FullName() interface{ String() string }
			}
		}
	}:
		return t.ProtoReflect().Descriptor().FullName().String()
	default:
		return "unknown"
	}
}

func (m *quantityMarshaler) fixQuantityFields(msg proto.Message, jsonMap map[string]interface{}) error {
	return m.walkMessage(msg.ProtoReflect(), jsonMap)
}

func (m *quantityMarshaler) walkMessage(msg protoreflect.Message, jsonMap map[string]interface{}) error {
	fields := msg.Descriptor().Fields()

	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		fieldName := field.JSONName()

		if field.Kind() == protoreflect.MessageKind {
			fullName := field.Message().FullName()

			if fullName == "k8s_io.apimachinery.pkg.api.resource.Quantity" {
				quantityStr, err := m.extractQuantityString(jsonMap, fieldName)
				if err != nil {
					continue
				}

				if err := m.setQuantityField(msg, field, quantityStr); err != nil {
					m.logger.Error("Failed to set Quantity field", zap.String("field", fieldName), zap.Error(err))
				}

			} else if !field.IsMap() {
				if field.IsList() {
					fieldValue := msg.Get(field)
					list := fieldValue.List()
					if jsonArray, ok := jsonMap[fieldName].([]interface{}); ok {
						for j := 0; j < list.Len() && j < len(jsonArray); j++ {
							if jsonItem, ok := jsonArray[j].(map[string]interface{}); ok {
								m.walkMessage(list.Get(j).Message(), jsonItem)
							}
						}
					}
				} else {
					if nestedJSON, ok := jsonMap[fieldName].(map[string]interface{}); ok {
						fieldValue := msg.Get(field)
						m.walkMessage(fieldValue.Message(), nestedJSON)
					}
				}
			}
		}
	}

	return nil
}

func (m *quantityMarshaler) extractQuantityString(jsonMap map[string]interface{}, fieldName string) (string, error) {
	fieldValue, ok := jsonMap[fieldName]
	if !ok {
		return "", fmt.Errorf("field not found")
	}

	quantityObj, ok := fieldValue.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("not an object")
	}

	str, ok := quantityObj["string"].(string)
	if !ok {
		return "", fmt.Errorf("string field missing")
	}

	return str, nil
}

func (m *quantityMarshaler) setQuantityField(msg protoreflect.Message, field protoreflect.FieldDescriptor, quantityStr string) error {
	_ = msg.Mutable(field)

	msgReflect := msg.Interface()
	goValue := reflect.ValueOf(msgReflect)
	if goValue.Kind() == reflect.Ptr {
		goValue = goValue.Elem()
	}

	fieldGoName := field.Name()
	goField := goValue.FieldByNameFunc(func(name string) bool {
		return name == string(fieldGoName) ||
			name == field.JSONName() ||
			name == string(fieldGoName[0]-32)+string(fieldGoName[1:])
	})

	if !goField.IsValid() {
		return fmt.Errorf("could not find Go field for %s", fieldGoName)
	}
	if !goField.CanSet() {
		return fmt.Errorf("field %s is not settable", fieldGoName)
	}

	var quantity *resource.Quantity
	if goField.IsNil() {
		quantity = &resource.Quantity{}
	} else {
		quantity = goField.Interface().(*resource.Quantity)
	}

	jsonBytes := []byte(`"` + quantityStr + `"`)
	if err := quantity.UnmarshalJSON(jsonBytes); err != nil {
		return fmt.Errorf("unmarshal Quantity field %s: %w", fieldGoName, err)
	}

	if goField.IsNil() {
		quantityValue := reflect.ValueOf(quantity)
		if goField.Type() != quantityValue.Type() {
			return fmt.Errorf("type mismatch: field is %v, value is %v", goField.Type(), quantityValue.Type())
		}
		goField.Set(quantityValue)
	}

	return nil
}

func (m *quantityMarshaler) fixQuantityFieldsInJSON(msg proto.Message, jsonData []byte) ([]byte, error) {
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &jsonMap); err != nil {
		return nil, fmt.Errorf("parse JSON for Quantity fix: %w", err)
	}

	if err := m.walkMessageForMarshal(msg.ProtoReflect(), jsonMap); err != nil {
		return nil, err
	}

	fixedJSON, err := json.Marshal(jsonMap)
	if err != nil {
		return nil, fmt.Errorf("marshal fixed JSON: %w", err)
	}

	return fixedJSON, nil
}

func (m *quantityMarshaler) walkMessageForMarshal(msg protoreflect.Message, jsonMap map[string]interface{}) error {
	fields := msg.Descriptor().Fields()

	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		fieldName := field.JSONName()

		if field.Kind() == protoreflect.MessageKind {
			fullName := field.Message().FullName()

			if fullName == "k8s_io.apimachinery.pkg.api.resource.Quantity" {
				msgReflect := msg.Interface()
				goValue := reflect.ValueOf(msgReflect)
				if goValue.Kind() == reflect.Ptr {
					goValue = goValue.Elem()
				}

				goField := goValue.FieldByNameFunc(func(name string) bool {
					return name == string(field.Name()) ||
						name == field.JSONName() ||
						name == string(field.Name()[0]-32)+string(field.Name()[1:])
				})

				if !goField.IsValid() || goField.IsNil() {
					continue
				}

				quantity := goField.Interface().(*resource.Quantity)
				quantityJSON, err := quantity.MarshalJSON()
				if err != nil {
					m.logger.Error("Failed to marshal Quantity", zap.String("field", fieldName), zap.Error(err))
					continue
				}

				jsonMap[fieldName] = map[string]interface{}{
					"string": string(quantityJSON[1 : len(quantityJSON)-1]),
				}

			} else if !field.IsMap() {
				if field.IsList() {
					if jsonArray, ok := jsonMap[fieldName].([]interface{}); ok {
						fieldValue := msg.Get(field)
						list := fieldValue.List()
						for j := 0; j < list.Len() && j < len(jsonArray); j++ {
							if jsonItem, ok := jsonArray[j].(map[string]interface{}); ok {
								m.walkMessageForMarshal(list.Get(j).Message(), jsonItem)
							}
						}
					}
				} else {
					if nestedJSON, ok := jsonMap[fieldName].(map[string]interface{}); ok {
						fieldValue := msg.Get(field)
						m.walkMessageForMarshal(fieldValue.Message(), nestedJSON)
					}
				}
			}
		}
	}

	return nil
}

// Ensure quantityMarshaler implements runtime.Marshaler
var _ runtime.Marshaler = (*quantityMarshaler)(nil)
