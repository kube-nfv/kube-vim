// Package k8stest provides shared helpers for testing kube-vim managers against
// the controller-runtime fake client. Managers are constructed with an injected
// client.Client (and client.Reader), so a fake client seeded with objects stands
// in for a live cluster without envtest or a real apiserver.
package k8stest

import (
	"context"
	"testing"
	"time"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestNamespace is the namespace managers are configured with across tests.
const TestNamespace = "kube-nfv"

// ID builds an ETSI Identifier, the most common test fixture argument.
func ID(v string) *nfvcommon.Identifier { return &nfvcommon.Identifier{Value: v} }

// Ptr returns a pointer to v, for the many optional (pointer) proto fields.
func Ptr[T any](v T) *T { return &v }

// AssignMetaOnCreate mirrors the apiserver stamping a UID and creation timestamp
// on create, which the fake client does not do on its own. Read-after-create
// paths that run IsObjectInstantiated need both.
var AssignMetaOnCreate = interceptor.Funcs{
	Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetUID() == "" {
			obj.SetUID(types.UID("uid-" + obj.GetName()))
		}
		if obj.GetCreationTimestamp().Time.IsZero() {
			obj.SetCreationTimestamp(metav1.Now())
		}
		return c.Create(ctx, obj, opts...)
	},
}

// NewClient returns a fake client backed by the kube-vim scheme, seeded with
// objs, that stamps create metadata like the apiserver. The returned value
// satisfies both client.Client and client.Reader, so it can be injected as the
// cache-backed client and the uncached apiReader.
func NewClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	scheme, err := k8s.BuildScheme()
	require.NoError(t, err)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithInterceptorFuncs(AssignMetaOnCreate).Build()
}

// ManagedMeta returns an ObjectMeta that passes both IsObjectInstantiated (UID +
// ResourceVersion + non-zero CreationTimestamp) and IsObjectManagedByKubeNfv
// (managed-by label). Namespace is left empty; set it for namespaced kinds.
func ManagedMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:              name,
		UID:               types.UID("uid-" + name),
		ResourceVersion:   "1",
		CreationTimestamp: metav1.NewTime(time.Now()),
		Labels:            map[string]string{common.K8sManagedByLabel: common.KubeNfvName},
	}
}
