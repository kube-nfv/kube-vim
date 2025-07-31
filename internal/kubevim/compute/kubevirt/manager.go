package kubevirt

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	kubevirt_flavour "github.com/kube-nfv/kube-vim/internal/kubevim/flavour/kubevirt"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
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

	// Kubevirt related metadata labels that is used in nfv.VirtualCompute.Metadata fields
	// In general labels should not be used in k8s object (only in nfv.VirtualCompute.Metadata fields)
	KubevirtVmStatusCreated    = "status.vm.kubevirt.io/created"
	KubevirtVmStatusReady      = "status.vm.kubevirt.io/ready"
	KubevirtVmStatusConditions = "status.vm.kubevirt.io/conditions"
	KubevirtVmPrintableStatus  = "status.vm.kubevirt.io/printable-status"
	KubevirtVmRunStategy       = "status.vm.kubevirt.io/run-strategy"

	KubevirtVmiStatusPhase  = "status.vmi.kubevirt.io/phase"
	KubevirtVmiStatusReason = "status.vmi.kubevirt.io/reason"

	KubevirtVmNetworkManagement = "network.vm.kubevirt.io/management"

	KubevirtInterfaceReady     = "interface.vm.kubevirt.io/ready"
)

const (
	vmiCreationTimeout     = time.Second * 2
	vmStatusCreatedTimeout = time.Second * 3
)

var (
	ipamConfigurationMissingErr = fmt.Errorf("IPAM configuration should have either subnetId or staticIp configured")
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
	volumes := []kubevirtv1.Volume{
		{
			Name: KubevirtVmMgmtRootVolumeName,
			VolumeSource: kubevirtv1.VolumeSource{
				DataVolume: &kubevirtv1.DataVolumeSource{
					Name: dv.Name,
				},
			},
		},
	}
	disks := []kubevirtv1.Disk{
		{
			Name: KubevirtVmMgmtRootVolumeName,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: "virtio",
				},
			},
		},
	}


	if req.UserData != nil {
		volume, disk, err := initUserDataVolume(req.GetUserData())
		if err != nil {
			return nil, fmt.Errorf("failed to initialize vm userdata volume: %w", err)
		}
		volumes = append(volumes, *volume)
		disks   = append(disks, *disk)
	}

	networks, interfaces, netAnnotations, err := initNetworks(ctx, m.networkManager, req.InterfaceData, req.InterfaceIPAM)
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

	vmAnnotations := make(map[string]string)
	for k, v := range netAnnotations {
		vmAnnotations[k] = v
	}

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
					Annotations: vmAnnotations,
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Devices: kubevirtv1.Devices{
							Disks: disks,
							Interfaces: interfaces,
						},
					},
					Networks: networks,
					Volumes: volumes,
				},
			},
		},
	}
	vm, err := m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).Create(ctx, vmSpec, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubevirt VirtualMachine: %w", err)
	}
	if err = waitVmiCreatedField(ctx, m.kubevirtClient, vm.Name, *m.cfg.Namespace); err != nil {
		return nil, fmt.Errorf("failed to create vmi for vm: %w", err)
	}
	vmi, err := m.kubevirtClient.KubevirtV1().VirtualMachineInstances(*m.cfg.Namespace).Get(ctx, vmName, v1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get vm instance \"%s\": %w", vmName, err)
	}
	virtualCompute, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, vm, vmi)
	if err != nil {
		return nil, fmt.Errorf("failed to convert kubevirt vm to the nfv virtualCompute: %w", err)
	}
	return virtualCompute, nil
}

func (m *manager) ListComputeResources(ctx context.Context) ([]*nfv.VirtualCompute, error) {
	vmList, err := m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).List(ctx, v1.ListOptions{
		LabelSelector: common.ManagedByKubeNfvSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list kubevirt vms: %w", err)
	}
	res := make([]*nfv.VirtualCompute, 0, len(vmList.Items))
	for _, vm := range vmList.Items {
		vmi, err := m.kubevirtClient.KubevirtV1().VirtualMachineInstances(*m.cfg.Namespace).Get(ctx, vm.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get kubevirt vmi with name \"%s\": %w", vm.Name, err)
		}
		vComp, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, &vm, vmi)
		if err != nil {
			return nil, fmt.Errorf("failed to convert kubevirt vmi and vm with name \"%s\" to nfv VirtualCompute: %w", vm.Name, err)
		}
		res = append(res, vComp)
	}
	return res, nil
}

func (m *manager) GetComputeResource(ctx context.Context, opts ...compute.GetComputeOpt) (*nfv.VirtualCompute, error) {
	cfg := compute.ApplyGetComputeOpts(opts...)
	if cfg.Name != "" {
		vm, err := m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get kubevirt vm with name \"%s\": %w", cfg.Name, err)
		}
		vmi, err := m.kubevirtClient.KubevirtV1().VirtualMachineInstances(*m.cfg.Namespace).Get(ctx, cfg.Name, v1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get kubevirt vmi with name \"%s\": %w", cfg.Name, err)
		}
		vComp, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, vm, vmi)
		if err != nil {
			return nil, fmt.Errorf("failed to convert kubevirt vmi and vm with name \"%s\" to nfv VirtualCompute: %w", cfg.Name, err)
		}
		return vComp, nil
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vmList, err := m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).List(ctx, v1.ListOptions{
			LabelSelector: common.ManagedByKubeNfvSelector,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list kubevirt vms: %w", err)
		}
		for _, vm := range vmList.Items {
			if vm.UID != misc.IdentifierToUID(cfg.Uid) {
				continue
			}
			vmi, err := m.kubevirtClient.KubevirtV1().VirtualMachineInstances(*m.cfg.Namespace).Get(ctx, vm.Name, v1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to get kubevirt vmi with name \"%s\": %w", vm.Name, err)
			}
			vComp, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, &vm, vmi)
			if err != nil {
				return nil, fmt.Errorf("failed to convert kubevirt vmi and vm with name \"%s\" to nfv VirtualCompute: %w", vm.Name, err)
			}
			return vComp, nil
		}
		return nil, fmt.Errorf("vm with id \"%s\" not found: %w", cfg.Uid.Value, common.NotFoundErr)
	}
	return nil, fmt.Errorf("either compute name or uid should be specified to get kubevirt vm: %w", common.InvalidArgumentErr)
}

func (m *manager) DeleteComputeResource(ctx context.Context, opts ...compute.GetComputeOpt) error {
	vm, err := m.GetComputeResource(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to get vm: %w", err)
	}
	if err = m.kubevirtClient.KubevirtV1().VirtualMachines(*m.cfg.Namespace).Delete(ctx, vm.GetComputeName(), v1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete compute resource with name \"%s\" and uid \"%s\": %w", vm.GetComputeName(), vm.ComputeId.Value, err)
	}
	return nil
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

func initUserDataVolume(userData *nfv.UserData) (*kubevirtv1.Volume, *kubevirtv1.Disk, error) {
	if userData.Content == "" {
		return nil, nil, fmt.Errorf("userData content can't be empty")
	}
	if userData.Method == nil {
		return nil, nil, fmt.Errorf("userData method can't be empty")
	}
	volumeName := "cloudinitdisk"
	var volumeSource kubevirtv1.VolumeSource

	switch *userData.Method {
	case nfv.UserData_CONFIG_DRIVE_PLAINTEXT,
		nfv.UserData_CONFIG_DRIVE_MIME_MULTIPART:
		// Use cloudInitConfigDrive for both plaintext and multipart
		// TODO: Build MIME multipart config if needed with CertificateData.
		volumeSource = kubevirtv1.VolumeSource{
			CloudInitConfigDrive: &kubevirtv1.CloudInitConfigDriveSource{
				UserData: userData.Content,
			},
		}
	case nfv.UserData_NO_CLOUD:
		volumeSource = kubevirtv1.VolumeSource{
			CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
				UserData: userData.Content,
			},
		}
	case nfv.UserData_METADATA_SERVICE:
		return nil, nil, fmt.Errorf("metadata service method not supported in KubeVirt natively")
	default:
		return nil, nil, fmt.Errorf("unsupported userData method: %v", userData.Method)
	}

	volume := &kubevirtv1.Volume{
		Name:         volumeName,
		VolumeSource: volumeSource,
	}

	disk := &kubevirtv1.Disk{
		Name: volumeName,
		DiskDevice: kubevirtv1.DiskDevice{
			Disk: &kubevirtv1.DiskTarget{
				Bus: "virtio",
			},
		},
	}
	return volume, disk, nil
}

// Note(dmalovan): Need to think if the VM need pod network by default, or it should be configured.
// For now pod network configured with a "masquarade" interface.
func initNetworks(ctx context.Context, netManager network.Manager, networksData []*nfv.VirtualNetworkInterfaceData, networkIpam []*nfv.VirtualNetworkInterfaceIPAM) ([]kubevirtv1.Network, []kubevirtv1.Interface, map[string]string, error) {
	networks := make([]kubevirtv1.Network, 0, len(networksData)+1 /*+ podNetwork*/)
	interfaces := make([]kubevirtv1.Interface, 0, len(networksData)+1 /*+ podNetwork*/)
	annotations := make(map[string]string)
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
	//    a. Have an subnetId (which is used to identify the IPAM). IPAM might be empty -> dynamic IPAM allocation (eg.DHCP)
	//    b. Only networkId. Try to find IPAM with networkId.
	//       - If found and IPAM has an static IP, try to find the subnet where port should belong to which include that static IP
	//       - If not found and NetworkInterfaceData has an compute.kubevim.kubenfv.io/network.subnet.assignment=random
	//           in metadata return the IPAM with a first subnet with dynamic IP.
	// 2. Underlay network
	//    a. Might have an subnetId. In that case IPAM identified using subnetId.
	//    b. Only networkId. Try to find IPAM with networkId.
	//       - If found and IPAM has an static IP, try to find the subnet where port should belong to which include that static IP
	//       - If not found return the IPAM with a first subnet with dynamic IP (most cases for the underlay network).
	for netIdx, netData := range networksData {
		hasNetworkId := netData.NetworkId != nil && netData.NetworkId.Value != ""
		hasSubnetId := netData.SubnetId != nil && netData.SubnetId.Value != ""
		if !hasNetworkId && !hasSubnetId {
			return nil, nil, nil, fmt.Errorf(
				"Failed to create vm interface with index \"%d\"."+
					"either networkId or subnetId should be defined to identify the VirtualNetworkInterfaceData related network",
				netIdx,
			)
		}
		var net *kubevirtv1.Network
		var iface *kubevirtv1.Interface
		ann := make(map[string]string)
		if hasSubnetId {
			subnetIdVal := netData.SubnetId.GetValue()
			subInst, err := netManager.GetSubnet(ctx, network.GetSubnetByUid(netData.SubnetId))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to get subnet with id \"%s\" referenced in VirtualNetworkInterfaceData: %w", subnetIdVal, err)
			}
			// Check if the subnet belongs to the same network if both are specified.
			if hasNetworkId {
				if subInst.NetworkId == nil {
					return nil, nil, nil, fmt.Errorf("subnet with id \"%s\" has no networkId but it is specified in the request as a \"%s\"", subnetIdVal, netData.NetworkId.Value)
				}
				if subInst.NetworkId.Value != netData.NetworkId.Value {
					return nil, nil, nil, fmt.Errorf("subnet with id \"%s\" reference to the network with id \"%s\" but another network with id \"%s\" is specified in the request", subnetIdVal, subInst.NetworkId.Value, netData.NetworkId.Value)
				}
			}
			ipam, err := getSubnetIpam(ctx, netData.SubnetId, netManager, networkIpam)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to get ipam for the subnet with is \"%s\": %w", subnetIdVal, err)
			}
			net, iface, ann, err = initNetwork(ctx, netManager, ipam)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to init kubevirt network and interface from the ipam referenced by the subnet \"%s\": %w", subnetIdVal, err)
			}
			// If VirtualNetworkInterfaceData has an subnetId, networkId will just ignored since subnetId contains enough info.
		} else if hasNetworkId /* no subnetId */ {
			netInst, err := netManager.GetNetwork(ctx, network.GetNetworkByUid(netData.NetworkId))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to get network with id \"%s\" referenced in VirtualNetworkInterfaceData: %w", netData.NetworkId.Value, err)
			}

			var ipam *nfv.VirtualNetworkInterfaceIPAM
			if netInst.NetworkType == nfv.NetworkType_UNDERLAY {
				ipam, err = getNetworkIpam(ctx, netData.NetworkId, netManager, networkIpam, false)
			} else if netInst.NetworkType == nfv.NetworkType_OVERLAY {
				returnOnMiss := true
				if netData.Metadata != nil {
					ann, ok := netData.Metadata.Fields[compute.KubenfvVmNetworkSubnetAssignmentAnnotation]
					allocateRandom := ok && ann == "random"
					returnOnMiss = !allocateRandom
				}
				ipam, err = getNetworkIpam(ctx, netData.NetworkId, netManager, networkIpam, returnOnMiss)
			}
			if err != nil {
				return nil, nil, nil, fmt.Errorf(
					"failed to get IPAM for the network specified by the networkId \"%s\": %w",
					netData.NetworkId.Value,
					err,
				)
			}
			net, iface, ann, err = initNetwork(ctx, netManager, ipam)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to init kubevirt network and interface from the ipam referenced by the network \"%s\": %w", netData.NetworkId.Value, err)
			}
		}
		networks = append(networks, *net)
		interfaces = append(interfaces, *iface)
		for k, v := range ann {
			annotations[k] = v
		}
	}
	return networks, interfaces, annotations, nil
}

// Returns the IP address/Mac address that should be allocated for given subnet.
// If IPAM not configured: returns random allocatable IP/MAC
// If IPAM is configured and IP is not randomly allocatable, checks if the address belongs to the subnet.
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
	sub, err := netManager.GetSubnet(ctx, network.GetSubnetByUid(subnetId))
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet specified by the id \"%s\": %w", subnetId.Value, err)
	}
	if sub.Cidr == nil {
		return nil, fmt.Errorf("subnet \"%s\" cidr can't be nil", sub.ResourceId.Value)
	}
	if !network.IpBelongsToCidr(netIpam.IpAddress, sub.Cidr) {
		return nil, fmt.Errorf("ip address \"%s\" not in the subnet cidr \"%s\"", netIpam.IpAddress.Ip, sub.Cidr.Cidr)
	}
	return netIpam, nil
}

// Find the correct IPAM for the network indentified by the networkId (without subnetId).
//  1. If IPAM exists for the network.
//     a. if subnetId exists in IPAM: Find the IPAM from the subnetID.
//     b. IPAM config has an static IP: Try to find the subnetID from VPC which is holds the IP.
//     Returns if "returnIfNoIpam" flag is set
//  2. If IPAM does't exists for the network.
//     Return the first subnet in VPC with dynamic IP/MAC IPAM.
func getNetworkIpam(ctx context.Context, networkId *nfv.Identifier, netManager network.Manager, netIPAMs []*nfv.VirtualNetworkInterfaceIPAM, returnIfNoIpam bool) (*nfv.VirtualNetworkInterfaceIPAM, error) {
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
	if netIpam != nil && netIpam.IpAddress != nil {
		sub, err := netManager.GetSubnet(ctx, network.GetSubnetByNetworkIP(networkId, netIpam.IpAddress))
		if err != nil {
			return nil, fmt.Errorf("failed to get subnet with networkId \"%s\" and Ip \"%s\": %w", networkId.Value, netIpam.IpAddress.Ip, err)
		}
		netIpam.SubnetId.Value = sub.ResourceId.Value
		return getSubnetIpam(ctx, sub.ResourceId, netManager, netIPAMs)
	}
	if returnIfNoIpam {
		return nil, ipamConfigurationMissingErr
	}
	net, err := netManager.GetNetwork(ctx, network.GetNetworkByUid(networkId))
	if err != nil {
		return nil, fmt.Errorf("failed to get network with id \"%s\": %w", networkId.Value, err)
	}
	if len(net.SubnetId) == 0 {
		return nil, fmt.Errorf("network with id \"%s\" doesn't have subnets", networkId.Value)
	}
	fstSubId := net.SubnetId[0]

	if netIpam != nil {
		netIpam.SubnetId = fstSubId
	} else {
		netIPAMs = append(netIPAMs, &nfv.VirtualNetworkInterfaceIPAM{
			NetworkId:  networkId,
			SubnetId:   fstSubId,
			IpAddress:  nil, // dynamic ip
			MacAddress: nil, // dynamic mac
		})
	}
	return getSubnetIpam(ctx, fstSubId, netManager, netIPAMs)
}

// Returns the kubevirt network and interface from the IPAM. Ipam should have an SubnetID
func initNetwork(ctx context.Context, netManager network.Manager, networkIpam *nfv.VirtualNetworkInterfaceIPAM) (*kubevirtv1.Network, *kubevirtv1.Interface, map[string]string, error) {
	if networkIpam.SubnetId == nil || networkIpam.SubnetId.Value == "" {
		return nil, nil, nil, fmt.Errorf("network ipam should have an subnetId reference")
	}
	getSubnetOpts := make([]network.GetSubnetOpt, 0)
	if misc.IsUUID(networkIpam.SubnetId.Value) {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByUid(networkIpam.GetSubnetId()))
	} else {
		getSubnetOpts = append(getSubnetOpts, network.GetSubnetByName(networkIpam.GetSubnetId().Value))
	}
	subnet, err := netManager.GetSubnet(ctx, getSubnetOpts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get subnet with id \"%s\": %w", networkIpam.GetSubnetId().Value, err)
	}
	netAttachName, ok := subnet.Metadata.Fields[network.K8sSubnetNetAttachNameLabel]
	if !ok {
		return nil, nil, nil, fmt.Errorf("network subnet with id \"%s\" missing label \"%s\" to identify the subnet name: %w", networkIpam.GetSubnetId().Value, network.K8sSubnetNameLabel, common.UnsupportedErr)
	}
	subnetName, ok := subnet.Metadata.Fields[network.K8sSubnetNameLabel]
	if !ok {
		return nil, nil, nil, fmt.Errorf("network subnet with id \"%s\" missing label \"%s\" to identify the subnet name: %w", networkIpam.GetSubnetId().Value, network.K8sSubnetNameLabel, common.UnsupportedErr)
	}
	// If multiple interfaces use the same subnet it will cause a problem if interface named the same as a subnet name.
	// Generate the unique UID for each network interface and combine it with a subnet-name
	ifaceUid, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate UUID for interface in subnet with id \"%s\": %w", networkIpam.GetSubnetId().Value, err)
	}
	ann := make(map[string]string)
	if networkIpam.IpAddress != nil && networkIpam.IpAddress.Ip != "" {
		ann[fmt.Sprintf("%s.%s.ovn.kubernetes.io/ip_address", netAttachName, common.KubeNfvDefaultNamespace)] = networkIpam.IpAddress.Ip
	}
	if networkIpam.MacAddress != nil && networkIpam.MacAddress.Mac != "" {
		ann[fmt.Sprintf("%s.%s.ovn.kubernetes.io/mac_address", netAttachName, common.KubeNfvDefaultNamespace)] = networkIpam.MacAddress.Mac
	}

	ifaceName := fmt.Sprintf("%s-%s", subnetName, ifaceUid)
	return &kubevirtv1.Network{
			Name: ifaceName,
			NetworkSource: kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					//Note(dmalovan): Ignore the namespace for the networkAttachmentDefinition name since it will use VMI namesapce
					NetworkName: netAttachName,
				},
			},
		}, &kubevirtv1.Interface{
			Name: ifaceName,
			InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
				Bridge: &kubevirtv1.InterfaceBridge{},
			},
		}, ann, nil
}

// Waits for the k8s vm object status.created field equal to True.
// If VM object already exists and has a status.created: true, the function will
// returns immediately.
func waitVmiCreatedField(ctx context.Context, client *kubevirt.Clientset, vmName string, namespace string) error {
	vm, err := client.KubevirtV1().VirtualMachines(namespace).Get(ctx, vmName, v1.GetOptions{})
	if err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("failed to get k8s kubevirt vm \"%s\": %w", vmName, err)
	}
	// vm exists and vmi already created.
	if err == nil && vm.Status.Created {
		return nil
	}

	vmSelector := fields.OneTermEqualSelector("metadata.name", vmName).String()
	watcher, err := client.KubevirtV1().VirtualMachines(namespace).Watch(ctx, v1.ListOptions{
		FieldSelector: vmSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to watch virtualmachine with name \"%s\": %w", vmName, err)
	}
	defer watcher.Stop()
	watchCtx, cancel := context.WithTimeout(ctx, vmStatusCreatedTimeout)
	defer cancel()
	for {
		select {
		case event := <-watcher.ResultChan():
			vm, ok := event.Object.(*kubevirtv1.VirtualMachine)
			if !ok || vm.Name != vmName {
				continue
			}
			if vm.Status.Created {
				return nil
			}
		case <-watchCtx.Done():
			return fmt.Errorf("vm \"%s\" status.created is not true after \"%s\"", vmName, vmStatusCreatedTimeout)
		}
	}
}

func waitVmiObjectCreated(ctx context.Context, client *kubevirt.Clientset, vmName string, namesapce string) (*kubevirtv1.VirtualMachineInstance, error) {
	fieldSelector := fields.OneTermEqualSelector("metadata.name", vmName).String()
	watcher, err := client.KubevirtV1().VirtualMachineInstances(namesapce).Watch(ctx, v1.ListOptions{
		FieldSelector: fieldSelector,
		Watch:         true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch virtualmachineinstance with name \"%s\": %w", vmName, err)
	}
	defer watcher.Stop()
	watchCtx, cancel := context.WithTimeout(ctx, vmiCreationTimeout)
	defer cancel()
	for {
		select {
		case event := <-watcher.ResultChan():
			vmi, ok := event.Object.(*kubevirtv1.VirtualMachineInstance)
			if !ok {
				continue
			}
			if vmi.Name == vmName {
				return vmi, nil
			}
		case <-watchCtx.Done():
			return nil, fmt.Errorf("no vmi creation after \"%v\"", vmiCreationTimeout)
		}
	}
}
