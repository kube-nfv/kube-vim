package kubevirt

import (
	"context"
	"testing"
	"time"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"kubevirt.io/api/instancetype/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = k8stest.TestNamespace

// newManager wires a flavour manager on top of a fake client seeded with objs.
func newManager(t *testing.T, objs ...client.Object) (*manager, client.Client) {
	t.Helper()
	cl := k8stest.NewClient(t, objs...)
	ns := testNamespace
	m, err := NewFlavourManager(cl, cl, &config.K8sConfig{Namespace: &ns})
	require.NoError(t, err)
	return m, cl
}

// seedFlavour builds a valid, instantiated, kube-nfv-owned instancetype +
// preference pair for the given flavour id, ready to seed into the fake client.
func seedFlavour(t *testing.T, id string, cpu uint32, mem string) (*v1beta1.VirtualMachineInstancetype, *v1beta1.VirtualMachinePreference) {
	t.Helper()
	memQ := resource.MustParse(mem)
	nfv := &vivnfm.VirtualComputeFlavour{
		VirtualCpu:    &vivnfm.VirtualCpuData{NumVirtualCpu: cpu},
		VirtualMemory: &vivnfm.VirtualMemoryData{VirtualMemSize: &memQ},
	}
	instType, instPref, err := kubeVirtInstanceTypePreferencesFromNfvFlavour(id, nfv)
	require.NoError(t, err)
	// The converter builds a template object; instantiate it as the apiserver
	// would (namespace + UID + CreationTimestamp) so IsObjectInstantiated passes.
	instantiate(&instType.ObjectMeta)
	instantiate(&instPref.ObjectMeta)
	return instType, instPref
}

func instantiate(meta *metav1.ObjectMeta) {
	meta.Namespace = testNamespace
	meta.UID = types.UID("uid-" + meta.Name)
	meta.CreationTimestamp = metav1.NewTime(time.Now())
}

func TestCreateFlavour(t *testing.T) {
	t.Parallel()
	memQ := resource.MustParse("2Gi")
	validFlavour := func() *vivnfm.VirtualComputeFlavour {
		return &vivnfm.VirtualComputeFlavour{
			VirtualCpu:    &vivnfm.VirtualCpuData{NumVirtualCpu: 2},
			VirtualMemory: &vivnfm.VirtualMemoryData{VirtualMemSize: &memQ},
		}
	}

	t.Run("generates id and persists instancetype+preference", func(t *testing.T) {
		m, cl := newManager(t)
		id, err := m.CreateFlavour(context.Background(), validFlavour())
		require.NoError(t, err)
		require.NotEmpty(t, id.GetValue())

		it := &v1beta1.VirtualMachineInstancetype{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: flavourNameFromId(id.GetValue())}, it))
		assert.Equal(t, common.KubeNfvName, it.Labels[common.K8sManagedByLabel])
		assert.Equal(t, id.GetValue(), it.Labels[flavour.K8sFlavourIdLabel])

		pref := &v1beta1.VirtualMachinePreference{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: flavourPreferenceNameFromId(id.GetValue())}, pref))
	})

	t.Run("honours caller-provided flavour id", func(t *testing.T) {
		m, _ := newManager(t)
		f := validFlavour()
		f.FlavourId = &nfvcommon.Identifier{Value: "my-id"}
		id, err := m.CreateFlavour(context.Background(), f)
		require.NoError(t, err)
		assert.Equal(t, "my-id", id.GetValue())
	})

	t.Run("nil flavour is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.CreateFlavour(context.Background(), nil)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})
}

func TestGetFlavour(t *testing.T) {
	t.Parallel()
	t.Run("returns seeded flavour", func(t *testing.T) {
		it, pref := seedFlavour(t, "f1", 4, "4Gi")
		m, _ := newManager(t, it, pref)
		got, err := m.GetFlavour(context.Background(), &nfvcommon.Identifier{Value: "f1"})
		require.NoError(t, err)
		assert.Equal(t, "f1", got.FlavourId.GetValue())
		assert.Equal(t, uint32(4), got.VirtualCpu.NumVirtualCpu)
	})

	t.Run("nil id is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetFlavour(context.Background(), nil)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("missing flavour is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetFlavour(context.Background(), &nfvcommon.Identifier{Value: "nope"})
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})

	t.Run("flavour owned by someone else is rejected", func(t *testing.T) {
		it, _ := seedFlavour(t, "f1", 2, "2Gi")
		delete(it.Labels, common.K8sManagedByLabel)
		m, _ := newManager(t, it)
		_, err := m.GetFlavour(context.Background(), &nfvcommon.Identifier{Value: "f1"})
		var target *apperrors.ErrK8sObjectNotManagedByKubeNfv
		assert.ErrorAs(t, err, &target)
	})
}

func TestGetFlavours(t *testing.T) {
	t.Parallel()
	t.Run("lists only kube-nfv-owned flavours", func(t *testing.T) {
		it1, pref1 := seedFlavour(t, "f1", 2, "2Gi")
		it2, pref2 := seedFlavour(t, "f2", 4, "4Gi")
		foreign, _ := seedFlavour(t, "f3", 8, "8Gi")
		delete(foreign.Labels, common.K8sManagedByLabel)

		m, _ := newManager(t, it1, pref1, it2, pref2, foreign)
		got, err := m.GetFlavours(context.Background())
		require.NoError(t, err)
		require.Len(t, got, 2)

		ids := map[string]bool{}
		for _, f := range got {
			ids[f.FlavourId.GetValue()] = true
		}
		assert.Equal(t, map[string]bool{"f1": true, "f2": true}, ids)
	})

	t.Run("empty cluster returns no flavours", func(t *testing.T) {
		m, _ := newManager(t)
		got, err := m.GetFlavours(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestDeleteFlavour(t *testing.T) {
	t.Parallel()
	t.Run("removes instancetype and preference", func(t *testing.T) {
		it, pref := seedFlavour(t, "f1", 2, "2Gi")
		m, cl := newManager(t, it, pref)
		require.NoError(t, m.DeleteFlavour(context.Background(), &nfvcommon.Identifier{Value: "f1"}))

		err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: it.Name}, &v1beta1.VirtualMachineInstancetype{})
		assert.True(t, apierrors.IsNotFound(err), "instancetype should be gone")
		err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: pref.Name}, &v1beta1.VirtualMachinePreference{})
		assert.True(t, apierrors.IsNotFound(err), "preference should be gone")
	})

	t.Run("deleting a missing flavour is a no-op", func(t *testing.T) {
		m, _ := newManager(t)
		assert.NoError(t, m.DeleteFlavour(context.Background(), &nfvcommon.Identifier{Value: "nope"}))
	})

	t.Run("does not delete a flavour owned by someone else", func(t *testing.T) {
		it, _ := seedFlavour(t, "f1", 2, "2Gi")
		delete(it.Labels, common.K8sManagedByLabel)
		m, cl := newManager(t, it)
		require.NoError(t, m.DeleteFlavour(context.Background(), &nfvcommon.Identifier{Value: "f1"}))
		// Foreign object must survive the label-scoped delete.
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: it.Name}, &v1beta1.VirtualMachineInstancetype{}))
	})
}
