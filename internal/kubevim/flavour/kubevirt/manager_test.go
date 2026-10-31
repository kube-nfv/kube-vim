package kubevirt

import (
	"context"
	"testing"

	flavourv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/flavour/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"kubevirt.io/api/instancetype/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = k8stest.TestNamespace

// newManager wires a flavour manager on top of a fake client seeded with objs.
func newManager(t *testing.T, objs ...client.Object) (*manager, client.Client) {
	t.Helper()
	cl := k8stest.NewIndexedClient(t, IdIndexes, objs...)
	ns := testNamespace
	m, err := NewFlavourManager(cl, cl, &config.K8sConfig{Namespace: &ns})
	require.NoError(t, err)
	return m, cl
}

func newFlavour() *flavourv1.Flavour {
	return &flavourv1.Flavour{
		FlavourName:   "small",
		VirtualCpu:    &flavourv1.VirtualCpuData{NumVirtualCpu: 2},
		VirtualMemory: &flavourv1.VirtualMemoryData{VirtualMemSize: "2048Mi"},
		Metadata:      map[string]string{"owner": "osm"},
	}
}

// create stores a flavour through m and returns its id.
func create(t *testing.T, m *manager) string {
	t.Helper()
	f, err := m.CreateFlavour(context.Background(), newFlavour())
	require.NoError(t, err)
	return f.FlavourId
}

func TestCreateFlavour(t *testing.T) {
	t.Parallel()
	t.Run("persists instancetype and preference", func(t *testing.T) {
		m, cl := newManager(t)
		f, err := m.CreateFlavour(context.Background(), newFlavour())
		require.NoError(t, err)
		require.NotEmpty(t, f.FlavourId)
		assert.Equal(t, "small", f.FlavourName)
		assert.Equal(t, "2048Mi", f.VirtualMemory.VirtualMemSize)
		assert.Equal(t, map[string]string{
			"owner":                         "osm",
			flavour.InstancetypeMetadataKey: flavourNameFromId(f.FlavourId),
			flavour.PreferenceMetadataKey:   flavourPreferenceNameFromId(f.FlavourId),
		}, f.Metadata)

		it := &v1beta1.VirtualMachineInstancetype{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: flavourNameFromId(f.FlavourId)}, it))
		assert.Equal(t, common.KubeNfvName, it.Labels[common.K8sManagedByLabel])
		assert.Equal(t, f.FlavourId, it.Labels[flavour.K8sFlavourIdLabel])
		assert.Equal(t, uint32(2), it.Spec.CPU.Guest)
		assert.Equal(t, "2Gi", it.Spec.Memory.Guest.String())

		pref := &v1beta1.VirtualMachinePreference{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: flavourPreferenceNameFromId(f.FlavourId)}, pref))
	})

	t.Run("unsupported field is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		f := newFlavour()
		f.VirtualMemory.HugepageSize = "2Mi"
		_, err := m.CreateFlavour(context.Background(), f)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})
}

func TestGetFlavour(t *testing.T) {
	t.Parallel()
	t.Run("returns the created flavour unchanged", func(t *testing.T) {
		m, _ := newManager(t)
		id := create(t, m)
		got, err := m.GetFlavour(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, id, got.FlavourId)
		assert.Equal(t, "small", got.FlavourName)
		assert.Equal(t, "2048Mi", got.VirtualMemory.VirtualMemSize)
	})

	t.Run("missing flavour is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetFlavour(context.Background(), "nope")
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})

	t.Run("flavour owned by someone else is rejected", func(t *testing.T) {
		foreign := foreignInstancetype(t, "f1")
		m, _ := newManager(t, foreign)
		_, err := m.GetFlavour(context.Background(), "f1")
		var target *apperrors.ErrK8sObjectNotManagedByKubeNfv
		assert.ErrorAs(t, err, &target)
	})
}

func TestListFlavours(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t, foreignInstancetype(t, "f3"))
	ids := map[string]bool{create(t, m): true, create(t, m): true}

	got, err := m.ListFlavours(context.Background())
	require.NoError(t, err)
	listed := map[string]bool{}
	for _, f := range got {
		listed[f.FlavourId] = true
	}
	assert.Equal(t, ids, listed)
}

func TestDeleteFlavour(t *testing.T) {
	t.Parallel()
	t.Run("removes instancetype and preference", func(t *testing.T) {
		m, cl := newManager(t)
		id := create(t, m)
		require.NoError(t, m.DeleteFlavour(context.Background(), id))

		err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: flavourNameFromId(id)}, &v1beta1.VirtualMachineInstancetype{})
		assert.True(t, apierrors.IsNotFound(err), "instancetype should be gone")
		err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: flavourPreferenceNameFromId(id)}, &v1beta1.VirtualMachinePreference{})
		assert.True(t, apierrors.IsNotFound(err), "preference should be gone")
	})

	t.Run("missing flavour is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, m.DeleteFlavour(context.Background(), "nope"), &target)
	})

	t.Run("does not delete a flavour owned by someone else", func(t *testing.T) {
		foreign := foreignInstancetype(t, "f1")
		m, cl := newManager(t, foreign)
		assert.Error(t, m.DeleteFlavour(context.Background(), "f1"))
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: foreign.Name}, &v1beta1.VirtualMachineInstancetype{}))
	})
}

// foreignInstancetype is a flavour instancetype for id without the managed-by label.
func foreignInstancetype(t *testing.T, id string) *v1beta1.VirtualMachineInstancetype {
	t.Helper()
	instType, _, err := kubeVirtObjects(id, newFlavour())
	require.NoError(t, err)
	instType.Namespace = testNamespace
	delete(instType.Labels, common.K8sManagedByLabel)
	return instType
}
