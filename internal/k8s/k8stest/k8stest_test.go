package k8stest

import (
	"context"
	"testing"

	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewIndexedClient(t *testing.T) {
	const idLabel = "test.kubevim.kubenfv.io/id"
	labelled := func(name, id string) *corev1.ConfigMap {
		cm := &corev1.ConfigMap{ObjectMeta: ManagedMeta(name)}
		cm.Namespace = TestNamespace
		if id != "" {
			cm.Labels[idLabel] = id
		}
		return cm
	}
	c := NewIndexedClient(t, []k8s.LabelIndex{{Obj: &corev1.ConfigMap{}, Label: idLabel}},
		labelled("a", "id-a"), labelled("b", "id-b"), labelled("c", ""))

	var list corev1.ConfigMapList
	require.NoError(t, c.List(context.Background(), &list, client.InNamespace(TestNamespace), client.MatchingFields{idLabel: "id-b"}))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "b", list.Items[0].Name)
}
