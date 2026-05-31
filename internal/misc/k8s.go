package misc

import (
	"io"
	"time"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	common "github.com/kube-nfv/kube-vim/internal/config"
	kubevimconfig "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
)

// MergeLabels merges required into current. Returns (changed, merged) where
// changed is true if any required key was missing or had a different value in
// current. The merged map is a fresh copy; current is never mutated. Useful
// for idempotent label reconciliation of existing Kubernetes objects.
func MergeLabels(current, required map[string]string) (bool, map[string]string) {
	merged := map[string]string{}
	for k, v := range current {
		merged[k] = v
	}
	changed := false
	for k, v := range required {
		if existing, ok := merged[k]; !ok || existing != v {
			merged[k] = v
			changed = true
		}
	}
	return changed, merged
}

func UIDToIdentifier(uid types.UID) *nfvcommon.Identifier {
	return &nfvcommon.Identifier{
		Value: string(uid),
	}
}

func IdentifierToUID(identifier *nfvcommon.Identifier) types.UID {
	return types.UID(identifier.Value)
}

func IsObjectInstantiated(obj metav1.Object) bool {
	return obj.GetResourceVersion() != "" &&
		obj.GetUID() != "" &&
		!obj.GetCreationTimestamp().Time.IsZero()
}

func IsObjectManagedByKubeNfv(obj metav1.Object) bool {
	labels := obj.GetLabels()
	if managedBy, ok := labels[common.K8sManagedByLabel]; ok && managedBy == common.KubeNfvName {
		return true
	}
	return false
}

func DumpObjectAsJSON(obj runtime.Object, out io.Writer) error {
	encoder := json.NewSerializer(json.DefaultMetaFactory, nil, nil, false)
	return encoder.Encode(obj, out)
}

func ConvertK8sTimeToProtoTimestamp(t metav1.Time) *timestamppb.Timestamp {
	return timestamppb.New(t.Time)
}

func GetCreationTimestamp(obj metav1.Object) time.Time {
	return obj.GetCreationTimestamp().Time
}

func ToK8sTolerations(tolerations []kubevimconfig.Toleration) []corev1.Toleration {
	result := make([]corev1.Toleration, 0, len(tolerations))
	for _, t := range tolerations {
		kt := corev1.Toleration{}
		if t.Key != nil {
			kt.Key = *t.Key
		}
		if t.Value != nil {
			kt.Value = *t.Value
		}
		if t.Operator != nil {
			kt.Operator = corev1.TolerationOperator(*t.Operator)
		}
		if t.Effect != nil {
			kt.Effect = corev1.TaintEffect(*t.Effect)
		}
		if t.TolerationSeconds != nil {
			kt.TolerationSeconds = t.TolerationSeconds
		}
		result = append(result, kt)
	}
	return result
}

func GetLastUpdateTime(obj metav1.Object) *time.Time {
	var latest *time.Time
	for _, field := range obj.GetManagedFields() {
		if field.Time != nil {
			if latest == nil || field.Time.After(*latest) {
				t := field.Time.Time
				latest = &t
			}
		}
	}
	return latest
}
