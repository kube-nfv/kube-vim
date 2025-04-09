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
	dv, err := initImageDataVolume(imgInfo, req.GetComputeName())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize kubevirt data volume: %w", err)
	}

	networks, interfaces, err := initNetworks(ctx, m.networkManager, req.InterfaceData, req.InterfaceIPAM)
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

	vmSpec := &kubevirtv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name: vmName,
			Labels: map[string]string{
				kubevirtv1.VirtualMachineLabel: vmName,
				common.K8sManagedByLabel:       common.KubeNfvName,
				flavour.K8sFlavourIdLabel:      req.ComputeFlavourId.GetValue(),
				image.K8sImageIdLabel:          req.VcImageId.GetValue(),
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
						common.K8sManagedByLabel:       common.KubeNfvName,
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
							Interfaces: interfaces,
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
	vmInst, err := m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).Create(ctx, vmSpec, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubevirt VirtualMachine: %w", err)
	}
	flavId, err := getFlavourFromInstanceSpec(vmInst)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavor from the instantiated kubevirt vm: %w", err)
	}
	imgId, err := getImageIdFromInstnceSpec(vmInst)
	if err != nil {
		return nil, fmt.Errorf("failed to get image id from the instantiated kubevirt vm: %w", err)
	}
	return &nfv.VirtualCompute{
		ComputeId:   misc.UIDToIdentifier(vmInst.UID),
		ComputeName: &vmInst.Name,
		FlavourId:   flavId,
		VcImageId:   imgId,
		Metadata:    &nfv.Metadata{},
	}, err
}

func (m *manager) QueryComputeResource(context.Context) ([]*nfv.VirtualCompute, error) {

	return nil, nil
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

func initImageDataVolume(imageInfo *nfv.SoftwareImageInformation, vmName string) (*kubevirtv1.DataVolumeTemplateSpec, error) {
	if imageInfo == nil {
		return nil, fmt.Errorf("nfv software image info can't be empty")
	}
	if imageInfo.Name == "" {
		return nil, fmt.Errorf("nfv software image info name can't be empty")
	}
	// Note(dmalovan): vmName/imageName pair should be unique
	dvName := fmt.Sprintf("%s-%s-dv", vmName, imageInfo.Name)
	if imageInfo.Size == nil || imageInfo.GetSize().Equal(*resource.NewQuantity(0, resource.BinarySI)) {
		return nil, fmt.Errorf("nfv software image size can't be 0")
	}

	return &kubevirtv1.DataVolumeTemplateSpec{
		ObjectMeta: v1.ObjectMeta{
			Name: dvName,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
			},
			Annotations: map[string]string{
				// Explicitly set the label to use the pvc population by the DV.
				"cdi.kubevirt.io/storage.usePopulator": "true",
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

// Note(dmalovan): Need to think if the VM need pod network by default, or it should be configured.
// For now pod network configured with a "masquarade" interface.
func initNetworks(ctx context.Context, netManager network.Manager, networksData []*nfv.VirtualNetworkInterfaceData, networkIpam []*nfv.VirtualNetworkInterfaceIPAM) ([]kubevirtv1.Network, []kubevirtv1.Interface, error) {
	networks := make([]kubevirtv1.Network, 0, len(networksData)+1 /*+ podNetwork*/)
	interfaces := make([]kubevirtv1.Interface, 0, len(networksData)+1 /*+ podNetwork*/)
	// Add pod network
	networks = append(networks, kubevirtv1.Network{
		Name: KubevirtVmMgmtNetworkName,
		NetworkSource: kubevirtv1.NetworkSource{
			Pod: &kubevirtv1.PodNetwork{},
		},
	})
	interfaces = append(interfaces, kubevirtv1.Interface{
		Name: KubevirtVmMgmtNetworkName,
		InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
			Masquerade: &kubevirtv1.InterfaceMasquerade{},
		},
	})
	// There are might be few different network types that should be handeled.
	// 1. Overlay network
	//    a. Must have an subnetId (which is used to identify the IPAM). IPAM might be empty -> dynamic IPAM allocation (eg.DHCP)
	// 2. Underlay network
	//    a. Might have an subnetId. In that case IPAM identified using subnetId.
	//    b. Only networkId. Try to find IPAM with networkId.
	//       - If found and IPAM has an static IP, try to find the subnet where port should belong to which include that static IP
	//       - If not found return the IPAM with a first subnet with dynamic IP (most cases for the underlay network).
	for netIdx, netData := range networksData {
		hasNetworkId := netData.NetworkId != nil && netData.NetworkId.Value != ""
		hasSubnetId := netData.SubnetId != nil && netData.SubnetId.Value != ""
		if !hasNetworkId && !hasSubnetId {
			return nil, nil, fmt.Errorf(
				"Failed to create vm interface with index \"%d\"."+
					"either networkId or subnetId should be defined to identify the VirtualNetworkInterfaceData related network",
				netIdx,
			)
		}
		var net *kubevirtv1.Network
		var iface *kubevirtv1.Interface
		if hasSubnetId {
			subnetIdVal := netData.SubnetId.GetValue()
			subInst, err := netManager.GetSubnet(ctx, network.GetSubnetByUid(netData.SubnetId))
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get subnet with id \"%s\" referenced in VirtualNetworkInterfaceData: %w", subnetIdVal, err)
			}
			// Check if the subnet belongs to the same network if both are specified.
			if hasNetworkId {
				if subInst.NetworkId == nil {
					return nil, nil, fmt.Errorf("subnet with id \"%s\" has no networkId but it is specified in the request as a \"%s\"", subnetIdVal, netData.NetworkId.Value)
				}
				if subInst.NetworkId.Value != netData.NetworkId.Value {
					return nil, nil, fmt.Errorf("subnet with id \"%s\" reference to the network with id \"%s\" but another network with id \"%s\" is specified in the request", subnetIdVal, subInst.NetworkId.Value, netData.NetworkId.Value)
				}
			}
			ipam, err := getSubnetIpam(ctx, netData.SubnetId, netManager, networkIpam)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get ipam for the subnet with is \"%s\": %w", subnetIdVal, err)
			}
			net, iface, err = initNetwork(ctx, netManager, ipam)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to init kubevirt network and interface from the ipam referenced by the subnet \"%s\": %w", subnetIdVal, err)
			}
			// If VirtualNetworkInterfaceData has an subnetId, networkId will just ignored since subnetId contains enough info.
		} else if hasNetworkId {
			netInst, err := netManager.GetNetwork(ctx, network.GetNetworkByUid(netData.NetworkId))
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get network with id \"%s\" referenced in VirtualNetworkInterfaceData: %w", netData.NetworkId.Value, err)
			}
			if netInst.NetworkType == nfv.NetworkType_UNDERLAY {
				ipam, err := getNetworkIpam(ctx, netData.NetworkId, netManager, networkIpam)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"failed to get IPAM for the network specified by the networkId \"%s\": %w",
						netData.NetworkId.Value,
						err,
					)
				}
				net, iface, err = initNetwork(ctx, netManager, ipam)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to init kubevirt network and interface from the ipam referenced by the network \"%s\": %w", netData.NetworkId.Value, err)
				}
			} else if netInst.NetworkType == nfv.NetworkType_OVERLAY && !hasSubnetId {
				// SubnetID Must be specified for the OVERLAY network type
				return nil, nil, fmt.Errorf(
					"failed to create vm interface from network with id \"%s\""+
						"The network referenced in the VirtualNetworkInterfaceData is \"overlay\""+
						"but request lack of the subnetId, which is required for the \"overlay\" networks",
					netData.NetworkId.Value,
				)
			}
		}
		networks = append(networks, *net)
		interfaces = append(interfaces, *iface)
	}
	return networks, interfaces, nil
}

func getSubnetIpam(ctx context.Context, subnetId *nfv.Identifier, netManager network.Manager, netIPAMs []*nfv.VirtualNetworkInterfaceIPAM) (*nfv.VirtualNetworkInterfaceIPAM, error) {
	var netIpam *nfv.VirtualNetworkInterfaceIPAM = nil
	for _, ipam := range netIPAMs {
		if ipam.SubnetId != nil && ipam.SubnetId.Value == subnetId.Value {
			netIpam = ipam
			break
		}
	}
	// If no IPAM set for subnetId return just the default IPAM with dynamic IP, MAC that is reference that subnetId
	if netIpam == nil {
		return &nfv.VirtualNetworkInterfaceIPAM{
			NetworkId:  nil, // Might be empty if since subnetId is going to used by the caller.
			SubnetId:   subnetId,
			IpAddress:  nil, // Dynamic Ip
			MacAddress: nil, // Dynamic MAC
		}, nil
	}
	if netIpam.IpAddress == nil {
		return netIpam, nil
	}
	// Check if the static IP Address belongs to the subnet referenced by the subnetId.
	_, err := netManager.GetSubnet(ctx, network.GetSubnetByUid(subnetId))
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet specified by the id \"%s\": %w", subnetId.Value, err)
	}
	// TODO:
	return nil, fmt.Errorf("Static IP not yet implemented for VirtualNetworkInterfaceIPAM: %w", common.NotImplementedErr)
}

// Find the correct IPAM for the network indentified by the networkId (without subnetId).
//   - If IPAM exists for the network.
//   - if subnetId exists in IPAM
//   - if IP address is static, check if it is related to the subnet. If not returs the error
//   - if IP address is dynamic just return the IPAM
//   - if subnetId does not exists in IPAM.
//   - if IP address is static, try to find the subnet which IP address belongs to. If not found returns the error.
//   - if IP address is dynamic, return the dynamic IPAM for the first subnet in the network referenced by the networkId.
//   - If IPAM does not exists for the network.
//   - return the dynamic IPAM (no IP, no MAC) for the first subnet in the network referenced by the networkId.
func getNetworkIpam(ctx context.Context, networkId *nfv.Identifier, netManager network.Manager, netIPAMs []*nfv.VirtualNetworkInterfaceIPAM) (*nfv.VirtualNetworkInterfaceIPAM, error) {
	var netIpam *nfv.VirtualNetworkInterfaceIPAM = nil
	for _, ipam := range netIPAMs {
		if ipam.NetworkId != nil && ipam.NetworkId.Value == networkId.Value {
			netIpam = ipam
			break
		}
	}
	if netIpam != nil && netIpam.SubnetId != nil {
		return getSubnetIpam(ctx, netIpam.SubnetId, netManager, netIPAMs)
	}
	// TODO: Support the case when subnetId not specified. It should reference to the first subnet wihin the network.
	return nil, fmt.Errorf("IPAM with only network reference not supported yet: %w", common.NotImplementedErr)
}

func initNetwork(ctx context.Context, netManager network.Manager, networkIpam *nfv.VirtualNetworkInterfaceIPAM) (*kubevirtv1.Network, *kubevirtv1.Interface, error) {
	if networkIpam.SubnetId == nil || networkIpam.SubnetId.Value == "" {
		return nil, nil, fmt.Errorf("network ipam should have an subnetId reference")
	}
	getSubnetOpts := make([]network.GetSubnetOpt, 0)
	if misc.IsUUID(networkIpam.SubnetId.Value) {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByUid(networkIpam.GetSubnetId()))
	} else {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByName(networkIpam.GetSubnetId().Value))
	}
	subnet, err := netManager.GetSubnet(ctx, getSubnetOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get subnet with id \"%s\": %w", networkIpam.GetSubnetId().Value, err)
	}
	netAttachName, ok := subnet.Metadata.Fields[network.K8sSubnetNetAttachNameLabel]
	if !ok {
		return nil, nil, fmt.Errorf("network subnet with id \"%s\" missing label \"%s\" to identify the subnet name: %w", networkIpam.GetSubnetId().Value, network.K8sSubnetNameLabel, common.UnsupportedErr)
	}
	subnetName, ok := subnet.Metadata.Fields[network.K8sSubnetNameLabel]
	if !ok {
		return nil, nil, fmt.Errorf("network subnet with id \"%s\" missing label \"%s\" to identify the subnet name: %w", networkIpam.GetSubnetId().Value, network.K8sSubnetNameLabel, common.UnsupportedErr)
	}

	return &kubevirtv1.Network{
			Name: subnetName,
			NetworkSource: kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					//Note(dmalovan): Ignore the namespace for the networkAttachmentDefinition name since it will use VMI namesapce
					NetworkName: netAttachName,
				},
			},
		}, &kubevirtv1.Interface{
			Name: subnetName,
			InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
				Bridge: &kubevirtv1.InterfaceBridge{},
			},
		}, nil
}

func getFlavourFromInstanceSpec(vmSpec *kubevirtv1.VirtualMachine) (*nfv.Identifier, error) {
	flavId, ok := vmSpec.Labels[flavour.K8sFlavourIdLabel]
	if !ok {
		return nil, fmt.Errorf("kubevirt virtualMachine spec missing kube-nfv flavour id label")
	}
	return &nfv.Identifier{
		Value: flavId,
	}, nil
}

func getImageIdFromInstnceSpec(vmSpec *kubevirtv1.VirtualMachine) (*nfv.Identifier, error) {
	imgId, ok := vmSpec.Labels[image.K8sImageIdLabel]
	if !ok {
		return nil, fmt.Errorf("kubevirt virtualMachine spec missing kube-nfv image id label")
	}
	return &nfv.Identifier{
		Value: imgId,
	}, nil

}
