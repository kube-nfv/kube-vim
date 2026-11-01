package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	networkv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/network/v1"
	storagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/storage/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const problemTypePrefix = "https://kubevim.kubenfv.io/problems/"

// reasonHTTPStatus is the HTTP status of each error reason (ErrorReason in kube-vim-api).
var reasonHTTPStatus = map[commonv1.ErrorReason]int{
	commonv1.ErrorReason_ERROR_REASON_VALIDATION_FAILED:   http.StatusUnprocessableEntity,
	commonv1.ErrorReason_ERROR_REASON_UNSUPPORTED_FIELD:   http.StatusUnprocessableEntity,
	commonv1.ErrorReason_ERROR_REASON_INVALID_FILTER:      http.StatusBadRequest,
	commonv1.ErrorReason_ERROR_REASON_NOT_FOUND:           http.StatusNotFound,
	commonv1.ErrorReason_ERROR_REASON_RESOURCE_IN_USE:     http.StatusConflict,
	commonv1.ErrorReason_ERROR_REASON_INVALID_STATE:       http.StatusConflict,
	commonv1.ErrorReason_ERROR_REASON_NOT_IMPLEMENTED:     http.StatusNotImplemented,
	commonv1.ErrorReason_ERROR_REASON_BACKEND_UNAVAILABLE: http.StatusServiceUnavailable,
	commonv1.ErrorReason_ERROR_REASON_INTERNAL:            http.StatusInternalServerError,
}

// newServeMux returns the REST mux for the v1 APIs, with SOL 013 status codes and errors.
func newServeMux() *runtime.ServeMux {
	return runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &jsonMarshaler{JSONPb: &runtime.JSONPb{
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
		}}),
		runtime.WithForwardResponseOption(setStatus),
		runtime.WithErrorHandler(errorHandler),
		runtime.WithRoutingErrorHandler(routingErrorHandler),
	)
}

// registerHandlers registers the REST handlers of every v1 API on mux, forwarding to conn.
func registerHandlers(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
	for _, register := range []func(context.Context, *runtime.ServeMux, *grpc.ClientConn) error{
		computev1.RegisterComputeServiceHandler,
		flavourv1.RegisterFlavourServiceHandler,
		imagev1.RegisterImageServiceHandler,
		networkv1.RegisterNetworkServiceHandler,
		storagev1.RegisterStorageServiceHandler,
	} {
		if err := register(ctx, mux, conn); err != nil {
			return err
		}
	}
	return nil
}

// jsonMarshaler writes an empty list as [] rather than null.
type jsonMarshaler struct {
	*runtime.JSONPb
}

func (m *jsonMarshaler) Marshal(v any) ([]byte, error) {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		return []byte("[]"), nil
	}
	return m.JSONPb.Marshal(v)
}

type requestPathKey struct{}

// withRequestPath keeps the request path in the context, for the Location header.
func withRequestPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPathKey{}, r.URL.EscapedPath())))
	})
}

// setStatus answers Create with 201 and Location, Delete with 204.
func setStatus(ctx context.Context, w http.ResponseWriter, resp proto.Message) error {
	method, _ := runtime.RPCMethod(ctx)
	switch name := method[strings.LastIndex(method, "/")+1:]; {
	case strings.HasPrefix(name, "Create"):
		path, _ := ctx.Value(requestPathKey{}).(string)
		// Field 1 of every resource is its id.
		m := resp.ProtoReflect()
		id := m.Get(m.Descriptor().Fields().ByNumber(1)).String()
		w.Header().Set("Location", strings.TrimSuffix(path, "/")+"/"+id)
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(name, "Delete"):
		w.Header().Del("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}
	return nil
}

// problemDetails is the SOL 013 6.3 error body (RFC 9457).
type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// errorHandler writes a gRPC error as ProblemDetails, typed by its ErrorInfo reason.
func errorHandler(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler, w http.ResponseWriter, _ *http.Request, err error) {
	httpStatus := 0
	var custom *runtime.HTTPStatusError
	if errors.As(err, &custom) {
		err, httpStatus = custom.Err, custom.HTTPStatus
	}
	s := status.Convert(err)
	if s.Code() == codes.Unauthenticated {
		w.Header().Set("WWW-Authenticate", s.Message())
	}
	p := problemFromStatus(s)
	if s.Code() == codes.Unauthenticated {
		p.Detail = challengeReason(s.Message())
	}
	if httpStatus != 0 {
		p = problemDetails{Type: "about:blank", Status: httpStatus, Detail: s.Message()}
	}
	writeProblem(w, p)
}

// challengeReason returns the error_description of an RFC 6750 challenge, which kube-vim sends as the status message.
func challengeReason(challenge string) string {
	if _, desc, ok := strings.Cut(challenge, `error_description="`); ok {
		if reason, _, ok := strings.Cut(desc, `"`); ok && reason != "" {
			return reason
		}
	}
	return "Authentication required"
}

func routingErrorHandler(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler, w http.ResponseWriter, _ *http.Request, httpStatus int) {
	writeProblem(w, problemDetails{Type: "about:blank", Status: httpStatus, Detail: http.StatusText(httpStatus)})
}

func problemFromStatus(s *status.Status) problemDetails {
	p := problemDetails{Type: "about:blank", Status: runtime.HTTPStatusFromCode(s.Code()), Detail: s.Message()}
	reason := commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
	for _, d := range s.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == apperrors.ErrorDomain {
			reason = commonv1.ErrorReason(commonv1.ErrorReason_value["ERROR_REASON_"+info.GetReason()])
		}
	}
	if httpStatus, ok := reasonHTTPStatus[reason]; ok {
		name := strings.TrimPrefix(reason.String(), "ERROR_REASON_")
		p.Type = problemTypePrefix + strings.ReplaceAll(strings.ToLower(name), "_", "-")
		p.Status = httpStatus
	}
	return p
}

func writeProblem(w http.ResponseWriter, p problemDetails) {
	p.Title = http.StatusText(p.Status)
	w.Header().Del("Trailer")
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
