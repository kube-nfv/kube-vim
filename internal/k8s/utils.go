package k8s

import (
	"github.com/DiMalovanyy/kube-vim-api/pb/nfv"
	"k8s.io/apimachinery/pkg/types"
)

func UIDToIdentifier(uid types.UID) *nfv.Identifier {
    return &nfv.Identifier{
        Value: string(uid),
    }
}

func IdentifierToUID(identifier *nfv.Identifier) types.UID {
    return types.UID(identifier.Value)
}
