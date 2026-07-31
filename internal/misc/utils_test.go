package misc

import (
	"testing"
	"time"

	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestIsUUID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid uuid", "550e8400-e29b-41d4-a716-446655440000", true},
		{"valid uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", true},
		{"not a uuid", "not-a-uuid", false},
		{"empty", "", false},
		{"truncated", "550e8400-e29b-41d4-a716", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsUUID(tt.input))
		})
	}
}

func TestMergeLabels(t *testing.T) {
	t.Run("adds missing key", func(t *testing.T) {
		changed, merged := MergeLabels(map[string]string{"a": "1"}, map[string]string{"b": "2"})
		assert.True(t, changed)
		assert.Equal(t, map[string]string{"a": "1", "b": "2"}, merged)
	})
	t.Run("overwrites different value", func(t *testing.T) {
		changed, merged := MergeLabels(map[string]string{"a": "1"}, map[string]string{"a": "2"})
		assert.True(t, changed)
		assert.Equal(t, "2", merged["a"])
	})
	t.Run("no change when required already present", func(t *testing.T) {
		changed, merged := MergeLabels(map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1"})
		assert.False(t, changed)
		assert.Equal(t, map[string]string{"a": "1", "b": "2"}, merged)
	})
	t.Run("does not mutate current", func(t *testing.T) {
		current := map[string]string{"a": "1"}
		_, merged := MergeLabels(current, map[string]string{"b": "2"})
		assert.Equal(t, map[string]string{"a": "1"}, current)
		assert.NotSame(t, &current, &merged)
	})
}

func TestUIDIdentifierRoundTrip(t *testing.T) {
	uid := types.UID("abc-123")
	id := UIDToIdentifier(uid)
	require.NotNil(t, id)
	assert.Equal(t, "abc-123", id.Value)
	assert.Equal(t, uid, IdentifierToUID(id))
}

func TestIsObjectInstantiated(t *testing.T) {
	instantiated := metav1.ObjectMeta{
		UID:               "uid-1",
		ResourceVersion:   "42",
		CreationTimestamp: metav1.NewTime(time.Now()),
	}
	tests := []struct {
		name string
		obj  metav1.ObjectMeta
		want bool
	}{
		{"fully instantiated", instantiated, true},
		{"missing uid", metav1.ObjectMeta{ResourceVersion: "42", CreationTimestamp: metav1.NewTime(time.Now())}, false},
		{"missing resource version", metav1.ObjectMeta{UID: "uid-1", CreationTimestamp: metav1.NewTime(time.Now())}, false},
		{"zero creation timestamp", metav1.ObjectMeta{UID: "uid-1", ResourceVersion: "42"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := tt.obj
			assert.Equal(t, tt.want, IsObjectInstantiated(&obj))
		})
	}
}

func TestIsObjectManagedByKubeNfv(t *testing.T) {
	t.Run("managed", func(t *testing.T) {
		obj := metav1.ObjectMeta{Labels: map[string]string{common.K8sManagedByLabel: common.KubeNfvName}}
		assert.True(t, IsObjectManagedByKubeNfv(&obj))
	})
	t.Run("different manager", func(t *testing.T) {
		obj := metav1.ObjectMeta{Labels: map[string]string{common.K8sManagedByLabel: "someone-else"}}
		assert.False(t, IsObjectManagedByKubeNfv(&obj))
	})
	t.Run("no labels", func(t *testing.T) {
		obj := metav1.ObjectMeta{}
		assert.False(t, IsObjectManagedByKubeNfv(&obj))
	})
}
