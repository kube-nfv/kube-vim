package flavour

import "github.com/DiMalovanyy/kube-vim-api/pb/nfv"

// Manager should be synced with the cluster state.
// When new flavour is add it should be updated in k8s VirtualMachineInstancetype VirtualMachinePreference objects
// When kube-vim is bring up, all flavours should be read from k8s
// If any flavour is add to the cluster it should be also add to manager

type manager struct {
	flavours map[string] /*Identifier*/ nfv.VirtualComputeFlavour
}

func NewFlavourManager() (*manager, error) {
	return nil, nil
}

// Add new flavour to the cluster topology
func (m *manager) AddFlavour(flavour *nfv.VirtualComputeFlavour) error {
	return nil
}
