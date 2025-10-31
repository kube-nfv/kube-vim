package gateway

import (
	"io"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
)

// loggingMarshaler wraps runtime.JSONPb to log requests/responses
type loggingMarshaler struct {
	*runtime.JSONPb
	logger *zap.Logger
}

func newLoggingMarshaler(logger *zap.Logger) *loggingMarshaler {
	return &loggingMarshaler{
		JSONPb: &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:   true,
				EmitUnpopulated: false,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		},
		logger: logger,
	}
}

func (m *loggingMarshaler) Unmarshal(data []byte, v interface{}) error {
	m.logger.Debug("Unmarshaling JSON request",
		zap.String("json", string(data)),
		zap.String("type", getTypeName(v)),
	)

	err := m.JSONPb.Unmarshal(data, v)

	if err != nil {
		m.logger.Error("Unmarshal failed",
			zap.Error(err),
			zap.String("json", string(data)),
		)
	} else {
		m.logger.Debug("Unmarshaled to Go struct",
			zap.Any("struct", v),
		)
	}

	return err
}

func (m *loggingMarshaler) Marshal(v interface{}) ([]byte, error) {
	m.logger.Debug("Marshaling Go struct to JSON",
		zap.String("type", getTypeName(v)),
		zap.Any("struct", v),
	)

	data, err := m.JSONPb.Marshal(v)

	if err != nil {
		m.logger.Error("Marshal failed",
			zap.Error(err),
		)
	} else {
		m.logger.Debug("Marshaled JSON response",
			zap.String("json", string(data)),
		)
	}

	return data, err
}

func (m *loggingMarshaler) NewDecoder(r io.Reader) runtime.Decoder {
	return &loggingDecoder{
		reader:    r,
		marshaler: m,
	}
}

func (m *loggingMarshaler) NewEncoder(w io.Writer) runtime.Encoder {
	return &loggingEncoder{
		writer:    w,
		marshaler: m,
	}
}

type loggingDecoder struct {
	reader    io.Reader
	marshaler *loggingMarshaler
}

func (d *loggingDecoder) Decode(v interface{}) error {
	d.marshaler.logger.Debug("Decoding stream to Go struct",
		zap.String("type", getTypeName(v)),
	)

	// Read entire stream into memory
	data, err := io.ReadAll(d.reader)
	if err != nil {
		d.marshaler.logger.Error("Failed to read stream", zap.Error(err))
		return err
	}

	d.marshaler.logger.Debug("Read stream data",
		zap.Int("bytes", len(data)),
	)

	// Delegate to Unmarshal which has our custom logic
	return d.marshaler.Unmarshal(data, v)
}

type loggingEncoder struct {
	writer    io.Writer
	marshaler *loggingMarshaler
}

func (e *loggingEncoder) Encode(v interface{}) error {
	e.marshaler.logger.Debug("Encoding Go struct to stream",
		zap.String("type", getTypeName(v)),
	)

	// Delegate to Marshal which has our custom logic
	data, err := e.marshaler.Marshal(v)
	if err != nil {
		return err
	}

	e.marshaler.logger.Debug("Writing marshaled data to stream",
		zap.Int("bytes", len(data)),
	)

	// Write to stream
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
	case interface{ ProtoReflect() interface{ Descriptor() interface{ FullName() interface{ String() string } } } }:
		return t.ProtoReflect().Descriptor().FullName().String()
	default:
		return "unknown"
	}
}

// Ensure loggingMarshaler implements runtime.Marshaler
var _ runtime.Marshaler = (*loggingMarshaler)(nil)
