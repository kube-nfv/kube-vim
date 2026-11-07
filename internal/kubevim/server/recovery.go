package server

import (
	"context"
	"runtime/debug"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/kubevim/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// recovery turns a panic in an RPC into an INTERNAL error, logged with its stack and counted.
type recovery struct {
	log    *zap.Logger
	panics metric.Int64Counter
}

func newRecovery(log *zap.Logger, meterProvider metric.MeterProvider) (*recovery, error) {
	panics, err := meterProvider.Meter(telemetry.MeterName).Int64Counter(
		"kubevim.grpc.panics",
		metric.WithDescription("gRPC requests that panicked"),
	)
	if err != nil {
		return nil, err
	}
	return &recovery{log: log, panics: panics}, nil
}

func (r *recovery) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer r.handle(ctx, info.FullMethod, &err)
	return handler(ctx, req)
}

func (r *recovery) stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer r.handle(ss.Context(), info.FullMethod, &err)
	return handler(srv, ss)
}

func (r *recovery) handle(ctx context.Context, method string, err *error) {
	p := recover()
	if p == nil {
		return
	}
	r.log.Error("Request panicked", zap.String("Request", method), zap.Any("Panic", p), zap.ByteString("Stack", debug.Stack()))
	r.panics.Add(ctx, 1, metric.WithAttributes(attribute.String("rpc.method", method)))
	*err = apperrors.ToGRPCError(apperrors.ErrInternal)
}
