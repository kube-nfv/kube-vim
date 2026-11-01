package services

import (
	"fmt"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	filter "github.com/kube-nfv/query-filter"
)

// listRequest is what every v1 List request carries.
type listRequest interface {
	GetFilter() string
	GetNextpageOpaqueMarker() string
}

// filterList applies the request's SOL 013 filter expression to in.
func filterList[T any](in []T, req listRequest) ([]T, error) {
	if req.GetNextpageOpaqueMarker() != "" {
		return nil, fmt.Errorf("nextpage_opaque_marker: %w", apperrors.ErrUnsupported)
	}
	if req.GetFilter() == "" {
		return in, nil
	}
	out, err := filter.FilterList(in, "filter="+req.GetFilter())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apperrors.ErrInvalidFilter, err)
	}
	return out, nil
}
