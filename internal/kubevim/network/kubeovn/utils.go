package kubeovn

import (
	"fmt"

	kubeovnv1 "github.com/DiMalovanyy/kube-vim-api/kube-ovn-api/pkg/apis/kubeovn/v1"
	"github.com/DiMalovanyy/kube-vim-api/pb/nfv"
	"github.com/DiMalovanyy/kube-vim/internal/k8s"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func kubeovnVpcFromNfvNetworkData(name string, nfvNet *nfv.VirtualNetworkData) (*kubeovnv1.Vpc, error) {
	if len(name) == 0 {
		return nil, fmt.Errorf("name can't be empty")
	}
	res := &kubeovnv1.Vpc{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Spec: kubeovnv1.VpcSpec{},
	}
	return res, nil
}

func kubeovnVpcToNfvNetwork(vpc *kubeovnv1.Vpc) (*nfv.VirtualNetwork, error) {
	uid := vpc.GetUID()
	if len(uid) == 0 {
		return nil, fmt.Errorf("UID for kube-ovn vpc can't be empty")
	}
	name := vpc.GetName()
	if len(name) == 0 {
		return nil, fmt.Errorf("Name for kube-ovn vpc can't be empty")
	}
	return &nfv.VirtualNetwork{
		NetworkResourceId:   k8s.UIDToIdentifier(uid),
		NetworkResourceName: &name,
	}, nil
}
