package services

import (
	"fmt"

	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	filter "github.com/kube-nfv/query-filter"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Attributes in Kubernetes quantity format, compared by value in filters.
var (
	flavourQuantityAttrs = []string{"virtualMemory/virtualMemSize", "virtualMemory/hugepageSize", "storageAttributes/sizeOfStorage"}
	computeQuantityAttrs = []string{"virtualMemory/virtualMemSize", "virtualMemory/hugepageSize", "disks/storageData/sizeOfStorage"}
	imageQuantityAttrs   = []string{"minDisk", "minRam", "size"}
)

// listRequest is what every v1 List request carries.
type listRequest interface {
	GetFilter() string
	GetNextpageOpaqueMarker() string
}

func compareQuantities(actual, expected string) (int, error) {
	a, err := resource.ParseQuantity(actual)
	if err != nil {
		return 0, fmt.Errorf("attribute value %q: %w", actual, err)
	}
	e, err := resource.ParseQuantity(expected)
	if err != nil {
		return 0, fmt.Errorf("filter value %q: %w", expected, err)
	}
	return a.Cmp(e), nil
}

// filterList applies the request's SOL 013 filter expression to in.
func filterList[T any](in []T, req listRequest, quantityAttrs ...string) ([]T, error) {
	if req.GetNextpageOpaqueMarker() != "" {
		return nil, fmt.Errorf("nextpage_opaque_marker: %w", apperrors.ErrUnsupported)
	}
	if req.GetFilter() == "" {
		return in, nil
	}
	opts := make([]filter.Option, len(quantityAttrs))
	for i, attr := range quantityAttrs {
		opts[i] = filter.WithComparator(attr, compareQuantities)
	}
	out, err := filter.FilterList(in, "filter="+req.GetFilter(), opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apperrors.ErrInvalidFilter, err)
	}
	return out, nil
}
