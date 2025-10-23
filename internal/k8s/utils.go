package k8s

import (
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
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
