package k8s

import (
	"fmt"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	kubeovnv1 "github.com/kube-nfv/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubevirtv1 "kubevirt.io/api/core/v1"
	instancetypev1beta1 "kubevirt.io/api/instancetype/v1beta1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// BuildScheme returns a runtime.Scheme with every API group kube-vim reads or
// writes registered, so a single controller-runtime cache/client can serve them.
func BuildScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	adders := []struct {
		name string
		fn   func(*runtime.Scheme) error
	}{
		{"client-go builtins", clientgoscheme.AddToScheme}, // core (pods, secrets), storage (storageclasses)
		{"kubevirt core/v1", kubevirtv1.AddToScheme},
		{"kubevirt instancetype/v1beta1", instancetypev1beta1.AddToScheme},
		{"cdi core/v1beta1", cdiv1beta1.AddToScheme},
		{"kube-ovn v1", kubeovnv1.AddToScheme},
		{"net-attach v1", netattv1.AddToScheme},
	}
	for _, a := range adders {
		if err := a.fn(scheme); err != nil {
			return nil, fmt.Errorf("register %s scheme: %w", a.name, err)
		}
	}
	return scheme, nil
}
