package kubevirt

import (
	"context"
	"fmt"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/kubevirt"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	kubevirtv1 "kubevirt.io/api/core/v1"
	kubevirt "kubevirt.io/client-go/generated/kubevirt/clientset/versioned"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	// TODO(dmalovan): find a way to get the Kind from not initialized object.
	// See: k8s.io/apimachinery/pkg/runtime/scheme.go:AddKnowTypes
	KubevirtVolumeImportSourceKind         = "VolumeImportSource"
	KubevirtVirtualMachineInstanceTypeKind = "VirtualMachineInstanceType"
	KubevirtVirtualMachinePreferenceKind   = "VirtualMachinePreference"

	KubevirtVmMgmtNetworkName    = "default"
	KubevirtVmMgmtRootVolumeName = "root-volume"
)

// kubevirt manager for allocation and management of the compute resources.
type manager struct {
	kubevirtClient *kubevirt.Clientset
	flavourManager flavour.Manager
	imageManager   image.Manager
	networkManager network.Manager

	// Note: Access should be readonly otherwise it might introduce races
	cfg *config.K8sConfig
}

func NewComputeManager(
	k8sConfig *rest.Config,
	cfg *config.K8sConfig,
	flavourManager flavour.Manager,
	imageManager image.Manager,
	networkManager network.Manager) (*manager, error) {
	c, err := kubevirt.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-virt k8s client: %w", err)
	}
	return &manager{
		kubevirtClient: c,
		flavourManager: flavourManager,
		imageManager:   imageManager,
		networkManager: networkManager,
		cfg:            cfg,
	}, nil
}

func (m *manager) AllocateComputeResource(ctx context.Context, req *nfv.AllocateComputeRequest) (*nfv.VirtualCompute, error) {
	if req == nil {
		return nil, fmt.Errorf("request can't be empty")
	}

	// Get request related compute flavour
	if req.ComputeFlavourId == nil || req.ComputeFlavourId.GetValue() == "" {
		return nil, fmt.Errorf("computeFlavourId can't be empty")
	}
	flav, err := m.flavourManager.GetFlavour(ctx, req.ComputeFlavourId)
	if err != nil {
		return nil, fmt.Errorf("failed to retrive flavour with id \"%s\": %w", req.ComputeFlavourId.GetValue(), err)
	}
	if flav.Metadata == nil {
		return nil, fmt.Errorf("flavour metadata can't be nil: %w", common.UnsupportedErr)
	}

	// TODO(dmalovan): Add the ability to works with different flavours providers/managers (eg. get flavours directly from the openstack nova)
	if flavourSource, ok := flav.Metadata.Fields[flavour.K8sFlavourSourceLabel]; !ok || flavourSource != kubevirt_flavour.KubevirtFlavourSource {
		return nil, fmt.Errorf("kubevirt compute manager can only works with kubevirt flavour manager: %w", common.UnsupportedErr)
	}
	vmInstanceTypeName, ok := flav.Metadata.Fields[kubevirtv1.InstancetypeAnnotation]
	if !ok {
		return nil, fmt.Errorf("kubevirt flavour metadata missed \"%s\" annotation: %w", kubevirtv1.InstancetypeAnnotation, common.InvalidArgumentErr)
	}
	instanceTypeMatcher, err := initVmInstanceTypeMatcher(vmInstanceTypeName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize kubevirt instance type matcher \"%s\": %w", vmInstanceTypeName, err)
	}
	vmPreferenceName, ok := flav.Metadata.Fields[kubevirtv1.PreferenceAnnotation]
	if !ok {
		return nil, fmt.Errorf("kubevirt flavour metadata missed \"%s\" annotation: %w", kubevirtv1.PreferenceAnnotation, common.InvalidArgumentErr)
	}
	// Note(dmalovan): preference matcher can be nil if some errors are returned. (eg. missed preference name in meta)
	preferenceMatcher, _ := initVmPreferenceMatcher(vmPreferenceName)

	// Get the Request related image and place it
	if req.VcImageId == nil || req.VcImageId.GetValue() == "" {
		return nil, fmt.Errorf("vcImageId can't be empty")
	}
	imgInfo, err := m.imageManager.GetImage(ctx, req.GetVcImageId())
	if err != nil {
		return nil, fmt.Errorf("failed to get image with id \"%s\": %w", req.GetVcImageId(), err)
	}
	dv, err := initImageDataVolume(imgInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize kubevirt data volume: %w", err)
	}

	networks, err := initNetworks(ctx, m.networkManager, req.InterfaceData)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize kubevirt networks: %w", err)
	}

	var vmName string
	if req.ComputeName == nil || *req.ComputeName == "" {
		// Note(dmalovan): If multiple vm created from the same image this name will conflict. Need to implement the way how to
		// make this name unique if it is not specified by the producer.
		vmName = imgInfo.Name + "-vm"
	} else {
		vmName = *req.ComputeName
	}

	runStrategy := kubevirtv1.RunStrategyAlways

	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name: vmName,
			Labels: map[string]string{
				kubevirtv1.VirtualMachineLabel: vmName,
				common.K8sManagedByLabel:       common.KubeNfvName,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{*dv},
			Instancetype:        instanceTypeMatcher,
			Preference:          preferenceMatcher,
			RunStrategy:         &runStrategy,
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						kubevirtv1.VirtualMachineLabel: vmName,
					},
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Devices: kubevirtv1.Devices{
							Disks: []kubevirtv1.Disk{
								{
									Name: KubevirtVmMgmtRootVolumeName,
									DiskDevice: kubevirtv1.DiskDevice{
										Disk: &kubevirtv1.DiskTarget{
											Bus: "virtio",
										},
									},
								},
							},
							Interfaces: []kubevirtv1.Interface{
								{
									Name: KubevirtVmMgmtNetworkName,
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
							},
						},
					},
					Networks: networks,
					Volumes: []kubevirtv1.Volume{
						{
							Name: KubevirtVmMgmtRootVolumeName,
							VolumeSource: kubevirtv1.VolumeSource{
								DataVolume: &kubevirtv1.DataVolumeSource{
									Name: dv.Name,
								},
							},
						},
					},
				},
			},
		},
	}
	_, err = m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).Create(ctx, vm, v1.CreateOptions{})

	return &nfv.VirtualCompute{}, err
}

func initVmInstanceTypeMatcher(instanceTypeName string) (*kubevirtv1.InstancetypeMatcher, error) {
	if instanceTypeName == "" {
		return nil, fmt.Errorf("instanceType name can't be empty")
	}
	return &kubevirtv1.InstancetypeMatcher{
		Kind: KubevirtVirtualMachineInstanceTypeKind,
		Name: instanceTypeName,
	}, nil
}

func initVmPreferenceMatcher(preferenceName string) (*kubevirtv1.PreferenceMatcher, error) {
	if preferenceName == "" {
		return nil, fmt.Errorf("preference name can't be empty")
	}
	return &kubevirtv1.PreferenceMatcher{
		Kind: KubevirtVirtualMachinePreferenceKind,
		Name: preferenceName,
	}, nil
}

func initImageDataVolume(imageInfo *nfv.SoftwareImageInformation) (*kubevirtv1.DataVolumeTemplateSpec, error) {
	if imageInfo == nil {
		return nil, fmt.Errorf("nfv software image info can't be empty")
	}
	if imageInfo.Name == "" {
		return nil, fmt.Errorf("nfv software image info name can't be empty")
	}
	// TODO(dmalovan): If more than one VM going to be created from the same Volume Import Source it might leads
	// to the dv naming conflicts. Make name depends on image name (probably hash or uid)
	dvName := imageInfo.Name + "-dv"
	if imageInfo.Size == nil || imageInfo.GetSize().Equal(*resource.NewQuantity(0, resource.BinarySI)) {
		return nil, fmt.Errorf("nfv software image size can't be 0")
	}

	return &kubevirtv1.DataVolumeTemplateSpec{
		ObjectMeta: v1.ObjectMeta{
			Name: dvName,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			PVC: &corev1.PersistentVolumeClaimSpec{
				DataSourceRef: &corev1.TypedObjectReference{
					APIGroup: &v1beta1.CDIGroupVersionKind.Group,
					Kind:     KubevirtVolumeImportSourceKind,
					Name:     imageInfo.Name,
				},
				AccessModes: []corev1.PersistentVolumeAccessMode{
					// TODO: Temporary solution to make only readWriteOnce data volumes.
					// Rewrite this with identifying accessmode from the storage class
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *imageInfo.GetSize(),
					},
				},
			},
		},
	}, nil
}

func initNetworks(ctx context.Context, netManager network.Manager, networksData []*nfv.VirtualInterfaceData) ([]kubevirtv1.Network, error) {
	networks := make([]kubevirtv1.Network, 0, len(networksData)+1 /*+ mgmtNetwork*/)
	// Add mgmt network
	networks = append(networks, kubevirtv1.Network{
		Name: KubevirtVmMgmtNetworkName,
		NetworkSource: kubevirtv1.NetworkSource{
			Pod: &kubevirtv1.PodNetwork{},
		},
	})
	for _, netData := range networksData {
		net, err := initNetwork(ctx, netManager, netData)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize kubevirt network from reference \"%s\": %w", netData.NetworkId.GetValue(), err)
		}
		networks = append(networks, *net)
	}
	return networks, nil
}

func initNetwork(ctx context.Context, netManager network.Manager, networkData *nfv.VirtualInterfaceData) (*kubevirtv1.Network, error) {
	if networkData.NetworkId == nil || networkData.NetworkId.Value == "" {
		return nil, fmt.Errorf("networkId can't be empty for VirtualInterfaceData: %w", common.InvalidArgumentErr)
	}
	getSubnetOpts := make([]network.GetSubnetOpt, 0)
	if misc.IsUUID(networkData.NetworkId.Value) {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByUid(networkData.GetNetworkId()))
	} else {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByName(networkData.GetNetworkId().Value))
	}
	nfvSubnet, err := netManager.GetSubnet(ctx, getSubnetOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet with id \"%s\": %w", networkData.GetNetworkId().Value, err)
	}
	if nfvSubnet.Metadata == nil {
		return nil, fmt.Errorf("subnet \"%s\" metadata can't be empty: %w", nfvSubnet.ResourceId.String(), common.InvalidArgumentErr)
	}
	netAttachName, ok := nfvSubnet.Metadata.Fields[network.K8sSubnetNetAttachNameLabel]
	if !ok {
		return nil, fmt.Errorf("network subnet missing label \"%s\" to identify network attachment definition: %w", network.K8sSubnetNetAttachNameLabel, common.InvalidArgumentErr)
	}
	subnetName, ok := nfvSubnet.Metadata.Fields[network.K8sSubnetNameLabel]
	if !ok {
		return nil, fmt.Errorf("network subnet missing label \"%s\" to identify the subnet name: %w", network.K8sSubnetNameLabel, common.UnsupportedErr)
	}
	return &kubevirtv1.Network{
		Name: subnetName,
		NetworkSource: kubevirtv1.NetworkSource{
			Multus: &kubevirtv1.MultusNetwork{
				//Note(dmalovan): Ignore the namespace for the networkAttachmentDefinition name since it will use VMI namesapce
				NetworkName: netAttachName,
			},
		},
	}, nil
}

func initVirtualMachineInstance(name string) (*kubevirtv1.VirtualMachineInstanceTemplateSpec, error) {

	return nil, nil
}
