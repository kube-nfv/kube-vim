package server

import (
	"context"
	"net"
	"testing"

	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	flavourmock "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const flavourID = "6f1c2a4e-8d3b-4c5a-9e7f-0a1b2c3d4e5f"

func TestHandlerPanicReturnsInternal(t *testing.T) {
	flavours := flavourmock.NewMockManager(gomock.NewController(t))
	gomock.InOrder(
		flavours.EXPECT().GetFlavour(gomock.Any(), flavourID).Do(func(context.Context, string) { panic("boom") }),
		flavours.EXPECT().GetFlavour(gomock.Any(), flavourID).Return(&flavourv1.Flavour{FlavourId: flavourID}, nil),
	)
	reader := sdkmetric.NewManualReader()
	srv, err := NewNorthboundServer(&config.ServerConfig{}, zap.NewNop(), sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), nil, nil, flavours, nil, nil)
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.server.Serve(ln) }()
	t.Cleanup(srv.server.Stop)
	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	client := flavourv1.NewFlavourServiceClient(conn)

	_, err = client.GetFlavour(context.Background(), &flavourv1.GetFlavourRequest{FlavourId: flavourID})
	st := status.Convert(err)
	assert.Equal(t, codes.Internal, st.Code())
	assert.NotContains(t, st.Message(), "boom")
	require.Len(t, st.Details(), 1)
	assert.Equal(t, "INTERNAL", st.Details()[0].(*errdetails.ErrorInfo).GetReason())

	// The server keeps serving after a panic.
	got, err := client.GetFlavour(context.Background(), &flavourv1.GetFlavourRequest{FlavourId: flavourID})
	require.NoError(t, err)
	assert.Equal(t, flavourID, got.GetFlavourId())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	assert.Equal(t, int64(1), panicCount(rm))
}

func panicCount(rm metricdata.ResourceMetrics) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "kubevim.grpc.panics" {
				continue
			}
			var n int64
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				n += dp.Value
			}
			return n
		}
	}
	return 0
}
