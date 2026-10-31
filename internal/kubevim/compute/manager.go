package compute

import (
	"context"

	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
)

const (
	K8sComputeIdLabel = "compute.kubevim.kubenfv.io/id"
	// K8sComputeAnnotation holds the create request as the consumer sent it (protojson, without user data),
	// with the interface names the VIM chose.
	K8sComputeAnnotation = "compute.kubevim.kubenfv.io/compute"

	// PodNameMetadataKey holds the virt-launcher pod name backing a compute;
	// the join key to pod-scoped backend series (cAdvisor, kube-state-metrics).
	PodNameMetadataKey = "kubevim.kubenfv.io/pod-name"
)

//go:generate go run go.uber.org/mock/mockgen -source=manager.go -destination=mock/mock_manager.go -package=mock

type Manager interface {
	CreateCompute(context.Context, *computev1.CreateComputeRequest) (*computev1.Compute, error)
	GetCompute(ctx context.Context, computeId string) (*computev1.Compute, error)
	ListComputes(context.Context) ([]*computev1.Compute, error)
	DeleteCompute(ctx context.Context, computeId string) error
}
