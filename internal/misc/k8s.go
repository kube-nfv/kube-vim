package misc

import (
	"io"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
)

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
