package k8s

import (
	common "github.com/kube-nfv/kube-vim/internal/config"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

// NewCluster builds the controller-runtime cluster.Cluster backing all kube-vim
// k8s access: cache-backed reads via GetClient, direct writes, and an uncached
// APIReader (GetAPIReader) for read-after-write paths.
//
// The cache is scoped to `namespace` and restricted to kube-nfv-owned objects
// (managed-by label), with two exceptions:
//   - pods: virt-launcher pods carry kubevirt labels, not managed-by, so they are
//     cached namespace-wide with no label filter (hot path: launcher info + scrapes).
//   - storageclasses: cluster-scoped and unlabelled; never cached — read via APIReader.
func NewCluster(cfg *rest.Config, namespace string, scheme *runtime.Scheme) (cluster.Cluster, error) {
	managedSel := labels.SelectorFromSet(labels.Set{common.K8sManagedByLabel: common.KubeNfvName})
	return cluster.New(cfg, func(o *cluster.Options) {
		o.Scheme = scheme
		o.Cache = cache.Options{
			Scheme:               scheme,
			DefaultNamespaces:    map[string]cache.Config{namespace: {}},
			DefaultLabelSelector: managedSel,
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Pod{}: {Label: labels.Everything()},
			},
		}
		o.Client.Cache = &client.CacheOptions{
			DisableFor: []client.Object{&storagev1.StorageClass{}},
		}
	})
}
