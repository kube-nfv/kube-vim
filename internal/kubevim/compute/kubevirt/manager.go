package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
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

	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// TODO(dmalovan): find a way to get the Kind from not initialized object.
	// See: k8s.io/apimachinery/pkg/runtime/scheme.go:AddKnowTypes
	KubevirtVolumeImportSourceKind         = "VolumeImportSource"
	KubevirtVirtualMachineInstanceTypeKind = "VirtualMachineInstanceType"
	KubevirtVirtualMachinePreferenceKind   = "VirtualMachinePreference"

	KubevirtVmMgmtNetworkName       = "default"
	KubevirtVmMgmtRootVolumeName    = "root-volume"
	KubevirtVmCloudInitSecretSuffix = "-cloud-init"

	// Kubevirt related metadata labels that is used in vivnfm.VirtualCompute.Metadata fields
	// In general labels should not be used in k8s object (only in vivnfm.VirtualCompute.Metadata fields)
	KubevirtVmStatusCreated    = "status.vm.kubevirt.io/created"
	KubevirtVmStatusReady      = "status.vm.kubevirt.io/ready"
	KubevirtVmStatusConditions = "status.vm.kubevirt.io/conditions"
	KubevirtVmPrintableStatus  = "status.vm.kubevirt.io/printable-status"
	KubevirtVmRunStategy       = "status.vm.kubevirt.io/run-strategy"

	KubevirtVmiStatusPhase  = "status.vmi.kubevirt.io/phase"
	KubevirtVmiStatusReason = "status.vmi.kubevirt.io/reason"

	KubevirtVmNetworkManagement = "network.vm.kubevirt.io/management"

	KubevirtInterfaceReady = "interface.vm.kubevirt.io/ready"
)

const (
	// vmiCreationTimeout bounds how long AllocateComputeResource waits for KubeVirt
	// to create the VMI after the VM is created (scheduler + virt-controller).
	vmiCreationTimeout = time.Second * 30
	vmiPollInterval    = time.Millisecond * 500
)

// ipamConfigurationMissingErr moved to errors.go as ErrIPAMConfigurationMissing

// kubevirt manager for allocation and management of the compute resources.
type manager struct {
	// client serves cache-backed reads and direct writes.
	client client.Client
	// apiReader is uncached; used to poll for a just-created VMI (read-after-write).
	apiReader      client.Reader
	flavourManager flavour.Manager
	imageManager   image.Manager
	networkManager network.Manager

	// Note: Access should be readonly otherwise it might introduce races
	cfg        *config.K8sConfig
	computeCfg *config.ComputeConfig
}

func NewComputeManager(
	cl client.Client,
	apiReader client.Reader,
	cfg *config.K8sConfig,
	computeCfg *config.ComputeConfig,
	flavourManager flavour.Manager,
	imageManager image.Manager,
	networkManager network.Manager) (*manager, error) {
	return &manager{
		client:         cl,
		apiReader:      apiReader,
		flavourManager: flavourManager,
		imageManager:   imageManager,
		networkManager: networkManager,
		cfg:            cfg,
		computeCfg:     computeCfg,
	}, nil
}

func (m *manager) AllocateComputeResource(ctx context.Context, req *vivnfm.AllocateComputeRequest) (*vivnfm.VirtualCompute, error) {
	namespace := *m.cfg.Namespace
	if req == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "request", Reason: "cannot be empty"}
	}

	// Get request related compute flavour
	if req.ComputeFlavourId == nil || req.ComputeFlavourId.GetValue() == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "compute flavour id", Reason: "cannot be empty"}
	}
	flav, err := m.flavourManager.GetFlavour(ctx, req.ComputeFlavourId)
	if err != nil {
		return nil, fmt.Errorf("retrieve flavour '%s': %w", req.ComputeFlavourId.GetValue(), err)
	}
	if flav.Metadata == nil {
		return nil, fmt.Errorf("flavour metadata cannot be nil: %w", apperrors.ErrUnsupported)
	}

	// TODO(dmalovan): Add the ability to works with different flavours providers/managers (eg. get flavours directly from the openstack nova)
	if flavourSource, ok := flav.Metadata.Fields[flavour.K8sFlavourSourceLabel]; !ok || flavourSource != kubevirt_flavour.KubevirtFlavourSource {
		return nil, fmt.Errorf("kubevirt compute manager can only work with kubevirt flavour manager: %w", apperrors.ErrUnsupported)
	}
	vmInstanceTypeName, ok := flav.Metadata.Fields[kubevirtv1.InstancetypeAnnotation]
	if !ok {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour metadata", Reason: fmt.Sprintf("missing '%s' annotation", kubevirtv1.InstancetypeAnnotation)}
	}
	instanceTypeMatcher, err := initVmInstanceTypeMatcher(vmInstanceTypeName)
	if err != nil {
		return nil, fmt.Errorf("initialize kubevirt instance type matcher '%s': %w", vmInstanceTypeName, err)
	}
	vmPreferenceName, ok := flav.Metadata.Fields[kubevirtv1.PreferenceAnnotation]
	if !ok {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour metadata", Reason: fmt.Sprintf("missing '%s' annotation", kubevirtv1.PreferenceAnnotation)}
	}
	// Note(dmalovan): preference matcher can be nil if some errors are returned. (eg. missed preference name in meta)
	preferenceMatcher, _ := initVmPreferenceMatcher(vmPreferenceName)

	// Get the Request related image and place it
	if req.VcImageId == nil || req.VcImageId.GetValue() == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "vc image id", Reason: "cannot be empty"}
	}
	imgInfo, err := m.imageManager.GetImage(ctx, req.GetVcImageId())
	if err != nil {
		return nil, fmt.Errorf("get image '%s': %w", req.GetVcImageId(), err)
	}
	dvs, err := initImageDataVolumes(imgInfo, flav.StorageAttributes, req.GetComputeName(), namespace)
	if err != nil {
		return nil, fmt.Errorf("initialize kubevirt data volume: %w", err)
	}
	volumes, disks := initVolumesDisksFromDataVolumes(dvs)

	var vmName string
	if req.ComputeName == nil || *req.ComputeName == "" {
		// Note(dmalovan): If multiple vm created from the same image this name will conflict. Need to implement the way how to
		// make this name unique if it is not specified by the producer.
		vmName = imgInfo.Name + "-vm"
	} else {
		vmName = *req.ComputeName
	}

	if req.UserData != nil {
		volume, disk, err := m.createUserDataVolumeWithSecret(ctx, namespace, vmName, req.GetUserData())
		if err != nil {
			return nil, fmt.Errorf("initialize vm userdata volume: %w", err)
		}
		volumes = append(volumes, *volume)
		disks = append(disks, *disk)
	}

	networks, interfaces, netAnnotations, err := newIpamResolver(m.networkManager, namespace).resolveInterfaces(ctx, req.InterfaceData, req.InterfaceIPAM)
	if err != nil {
		return nil, fmt.Errorf("initialize kubevirt networks: %w", err)
	}

	runStrategy := kubevirtv1.RunStrategyAlways

	vmAnnotations := make(map[string]string)
	for k, v := range netAnnotations {
		vmAnnotations[k] = v
	}

	useSecureBoot := false

	vmSpec := &kubevirtv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name:      vmName,
			Namespace: namespace,
			Labels: map[string]string{
				kubevirtv1.VirtualMachineLabel: vmName,
				common.K8sManagedByLabel:       common.KubeNfvName,
				flavour.K8sFlavourIdLabel:      req.ComputeFlavourId.GetValue(),
				image.K8sImageIdLabel:          req.VcImageId.GetValue(),
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			DataVolumeTemplates: dvs,
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
							Disks:      disks,
							Interfaces: interfaces,
						},
						Firmware: &kubevirtv1.Firmware{
							Bootloader: &kubevirtv1.Bootloader{
								EFI: &kubevirtv1.EFI{
									SecureBoot: &useSecureBoot,
								},
							},
						},
					},
					Networks: networks,
					Volumes:  volumes,
				},
			},
		},
	}
	if m.computeCfg != nil {
		if m.computeCfg.NodeSelector != nil {
			vmSpec.Spec.Template.Spec.NodeSelector = *m.computeCfg.NodeSelector
		}
		if m.computeCfg.Tolerations != nil {
			vmSpec.Spec.Template.Spec.Tolerations = misc.ToK8sTolerations(*m.computeCfg.Tolerations)
		}
	}

	if err := m.client.Create(ctx, vmSpec); err != nil {
		return nil, fmt.Errorf("create kubevirt VirtualMachine '%s': %w", vmName, err)
	}
	vmi, err := m.waitForVmi(ctx, vmName, namespace)
	if err != nil {
		return nil, fmt.Errorf("await VMI for VM '%s' (uid: %s): %w", vmName, vmSpec.UID, err)
	}
	virtualCompute, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, vmSpec, vmi, m.getLauncherInfo(ctx, vmi))
	if err != nil {
		return nil, fmt.Errorf("convert kubevirt VM '%s' (uid: %s) to nfv VirtualCompute: %w", vmName, vmSpec.UID, err)
	}
	return virtualCompute, nil
}

// waitForVmi polls the apiserver (uncached, for strong read-after-write) until the
// VMI for the just-created VM exists, or vmiCreationTimeout elapses.
func (m *manager) waitForVmi(ctx context.Context, name, namespace string) (*kubevirtv1.VirtualMachineInstance, error) {
	ctx, cancel := context.WithTimeout(ctx, vmiCreationTimeout)
	defer cancel()
	key := client.ObjectKey{Namespace: namespace, Name: name}
	for {
		vmi := &kubevirtv1.VirtualMachineInstance{}
		err := m.apiReader.Get(ctx, key, vmi)
		if err == nil {
			return vmi, nil
		}
		if !k8s_errors.IsNotFound(err) {
			return nil, fmt.Errorf("get VirtualMachineInstance '%s': %w", name, err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("VMI '%s' not created after %s: %w", name, vmiCreationTimeout, ctx.Err())
		case <-time.After(vmiPollInterval):
		}
	}
}

func (m *manager) ListComputeResources(ctx context.Context) ([]*vivnfm.VirtualCompute, error) {
	namespace := *m.cfg.Namespace
	managed := client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}
	ns := client.InNamespace(namespace)

	vmList := &kubevirtv1.VirtualMachineList{}
	if err := m.client.List(ctx, vmList, ns, managed); err != nil {
		return nil, fmt.Errorf("list kubevirt VirtualMachines: %w", err)
	}
	// List VMIs and virt-launcher pods once, then join in memory, instead of a
	// per-VM VMI Get + per-VM pod List (this path also runs on every metrics scrape).
	vmiList := &kubevirtv1.VirtualMachineInstanceList{}
	if err := m.client.List(ctx, vmiList, ns, managed); err != nil {
		return nil, fmt.Errorf("list kubevirt VirtualMachineInstances: %w", err)
	}
	vmiByName := make(map[string]*kubevirtv1.VirtualMachineInstance, len(vmiList.Items))
	for i := range vmiList.Items {
		vmiByName[vmiList.Items[i].Name] = &vmiList.Items[i]
	}
	podList := &corev1.PodList{}
	if err := m.client.List(ctx, podList, ns); err != nil {
		return nil, fmt.Errorf("list virt-launcher pods: %w", err)
	}
	podsByVmiUID := make(map[string][]*corev1.Pod)
	for i := range podList.Items {
		if uid, ok := podList.Items[i].Labels[kubevirtv1.CreatedByLabel]; ok {
			podsByVmiUID[uid] = append(podsByVmiUID[uid], &podList.Items[i])
		}
	}

	res := make([]*vivnfm.VirtualCompute, 0, len(vmList.Items))
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		vmi, ok := vmiByName[vm.Name]
		if !ok {
			// VMI not present yet (VM just created, or brief cache lag). Skip it;
			// it will appear on a subsequent list rather than failing the whole call.
			continue
		}
		launcher := launcherInfoFromPod(selectLauncherPod(podsByVmiUID[string(vmi.UID)], vmi.Status.NodeName))
		vComp, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, vm, vmi, launcher)
		if err != nil {
			return nil, fmt.Errorf("convert kubevirt VM '%s' (uid: %s) to nfv VirtualCompute: %w", vm.Name, vm.UID, err)
		}
		res = append(res, vComp)
	}
	return res, nil
}

// kubevirtNetworkInfo mirrors the kubevirt.io/network-info annotation payload
// (upstream type lives in the non-consumable kubevirt.io/kubevirt module).
const kubevirtNetworkInfoAnnotation = "kubevirt.io/network-info"

type kubevirtNetworkInfo struct {
	Interfaces []struct {
		Network    string               `json:"network"`
		DeviceInfo *netattv1.DeviceInfo `json:"deviceInfo,omitempty"`
	} `json:"interfaces,omitempty"`
}

type launcherInfo struct {
	podName       string
	hostPciByVnic map[string]string // vNIC name -> host PCI address (SR-IOV / pass-through)
}

// getLauncherInfo reads the VMI's virt-launcher pod name and per-vNIC host PCI
// addresses. Best-effort: returns zero values on any error, never fails the caller.
func (m *manager) getLauncherInfo(ctx context.Context, vmi *kubevirtv1.VirtualMachineInstance) launcherInfo {
	if vmi == nil || vmi.UID == "" {
		return launcherInfo{hostPciByVnic: map[string]string{}}
	}
	podList := &corev1.PodList{}
	if err := m.client.List(ctx, podList,
		client.InNamespace(*m.cfg.Namespace),
		client.MatchingLabels{kubevirtv1.CreatedByLabel: string(vmi.UID)},
	); err != nil {
		return launcherInfo{hostPciByVnic: map[string]string{}}
	}
	pods := make([]*corev1.Pod, 0, len(podList.Items))
	for i := range podList.Items {
		pods = append(pods, &podList.Items[i])
	}
	return launcherInfoFromPod(selectLauncherPod(pods, vmi.Status.NodeName))
}

// selectLauncherPod prefers the pod on the given node (unambiguous during
// migration), else the first pod. Returns nil for an empty set.
func selectLauncherPod(pods []*corev1.Pod, nodeName string) *corev1.Pod {
	if len(pods) == 0 {
		return nil
	}
	for _, p := range pods {
		if p.Spec.NodeName == nodeName {
			return p
		}
	}
	return pods[0]
}

// launcherInfoFromPod parses the virt-launcher pod name and per-vNIC host PCI
// addresses from the kubevirt network-info annotation.
func launcherInfoFromPod(pod *corev1.Pod) launcherInfo {
	info := launcherInfo{hostPciByVnic: map[string]string{}}
	if pod == nil {
		return info
	}
	info.podName = pod.Name
	infoJSON, ok := pod.Annotations[kubevirtNetworkInfoAnnotation]
	if !ok {
		return info
	}
	var netInfo kubevirtNetworkInfo
	if err := json.Unmarshal([]byte(infoJSON), &netInfo); err != nil {
		return info
	}
	for _, iface := range netInfo.Interfaces {
		if iface.DeviceInfo == nil || iface.DeviceInfo.Pci == nil || iface.DeviceInfo.Pci.PciAddress == "" {
			continue
		}
		info.hostPciByVnic[iface.Network] = iface.DeviceInfo.Pci.PciAddress
	}
	return info
}

func (m *manager) GetComputeResource(ctx context.Context, opts ...compute.GetComputeOpt) (*vivnfm.VirtualCompute, error) {
	namespace := *m.cfg.Namespace
	cfg := compute.ApplyGetComputeOpts(opts...)
	if cfg.Name != "" {
		vm := &kubevirtv1.VirtualMachine{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cfg.Name}, vm); err != nil {
			return nil, fmt.Errorf("get kubevirt VirtualMachine '%s': %w", cfg.Name, err)
		}
		return m.computeFromVM(ctx, vm)
	} else if cfg.Uid != nil && cfg.Uid.Value != "" {
		vmList := &kubevirtv1.VirtualMachineList{}
		if err := m.client.List(ctx, vmList, client.InNamespace(namespace), client.MatchingLabels{common.K8sManagedByLabel: common.KubeNfvName}); err != nil {
			return nil, fmt.Errorf("list kubevirt VirtualMachines: %w", err)
		}
		for i := range vmList.Items {
			if vmList.Items[i].UID != misc.IdentifierToUID(cfg.Uid) {
				continue
			}
			return m.computeFromVM(ctx, &vmList.Items[i])
		}
		return nil, &apperrors.ErrNotFound{Entity: "virtual machine", Identifier: cfg.Uid.Value}
	}
	return nil, &apperrors.ErrInvalidArgument{Field: "compute lookup", Reason: "either name or uid must be specified"}
}

// computeFromVM resolves the VMI and launcher info for a VM (from the cache) and
// converts it to a vivnfm.VirtualCompute.
func (m *manager) computeFromVM(ctx context.Context, vm *kubevirtv1.VirtualMachine) (*vivnfm.VirtualCompute, error) {
	vmi := &kubevirtv1.VirtualMachineInstance{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vmi); err != nil {
		return nil, fmt.Errorf("get kubevirt VirtualMachineInstance '%s' (uid: %s): %w", vm.Name, vm.UID, err)
	}
	vComp, err := nfvVirtualComputeFromKubevirtVm(ctx, m.networkManager, vm, vmi, m.getLauncherInfo(ctx, vmi))
	if err != nil {
		return nil, fmt.Errorf("convert kubevirt VM '%s' (uid: %s) to nfv VirtualCompute: %w", vm.Name, vm.UID, err)
	}
	return vComp, nil
}

func (m *manager) DeleteComputeResource(ctx context.Context, opts ...compute.GetComputeOpt) error {
	namespace := *m.cfg.Namespace
	vm, err := m.GetComputeResource(ctx, opts...)
	if err != nil {
		return fmt.Errorf("get virtual machine for deletion: %w", err)
	}
	vmObj := &kubevirtv1.VirtualMachine{ObjectMeta: v1.ObjectMeta{Name: vm.GetComputeName(), Namespace: namespace}}
	if err = m.client.Delete(ctx, vmObj); err != nil {
		return fmt.Errorf("delete kubevirt VirtualMachine '%s' (id: %s): %w", vm.GetComputeName(), vm.ComputeId.Value, err)
	}
	secretName := vm.GetComputeName() + KubevirtVmCloudInitSecretSuffix
	secret := &corev1.Secret{ObjectMeta: v1.ObjectMeta{Name: secretName, Namespace: namespace}}
	if err := m.client.Delete(ctx, secret); err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("delete cloud-init secret '%s' for VM '%s': %w", secretName, vm.GetComputeName(), err)
	}
	return nil
}

func initVmInstanceTypeMatcher(instanceTypeName string) (*kubevirtv1.InstancetypeMatcher, error) {
	if instanceTypeName == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "instanceType name", Reason: "cannot be empty"}
	}
	return &kubevirtv1.InstancetypeMatcher{
		Kind: KubevirtVirtualMachineInstanceTypeKind,
		Name: instanceTypeName,
	}, nil
}

func initVmPreferenceMatcher(preferenceName string) (*kubevirtv1.PreferenceMatcher, error) {
	if preferenceName == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "preference name", Reason: "cannot be empty"}
	}
	return &kubevirtv1.PreferenceMatcher{
		Kind: KubevirtVirtualMachinePreferenceKind,
		Name: preferenceName,
	}, nil
}

func initVolumesDisksFromDataVolumes(dvs []kubevirtv1.DataVolumeTemplateSpec) ([]kubevirtv1.Volume, []kubevirtv1.Disk) {
	volumes := make([]kubevirtv1.Volume, 0, len(dvs))
	disks := make([]kubevirtv1.Disk, 0, len(dvs))
	for _, dv := range dvs {
		volumes = append(volumes, kubevirtv1.Volume{
			Name: dv.Name,
			VolumeSource: kubevirtv1.VolumeSource{
				DataVolume: &kubevirtv1.DataVolumeSource{
					Name: dv.Name,
				},
			},
		})
		disks = append(disks, kubevirtv1.Disk{
			Name: dv.Name,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: "virtio",
				},
			},
		})
	}
	return volumes, disks
}

func initImageDataVolumes(imageInfo *vivnfm.SoftwareImageInformation, storageAttributes []*vivnfm.VirtualStorageData, vmName string, namespace string) ([]kubevirtv1.DataVolumeTemplateSpec, error) {
	// Init bootable disk.
	var bootableStorageAttr *vivnfm.VirtualStorageData = nil
	for _, storageAttr := range storageAttributes {
		if storageAttr.IsBoot != nil && *storageAttr.IsBoot == true {
			if bootableStorageAttr != nil {
				return nil, fmt.Errorf("more than one bootable storageAttributes specified: %w", apperrors.ErrUnsupported)
			}
			bootableStorageAttr = storageAttr
		}
	}
	if bootableStorageAttr == nil {
		return nil, fmt.Errorf("storage attributes not found for bootable disk: %w", apperrors.ErrUnsupported)
	}
	// TODO: For now only bootable disk attached supported
	bootDv, err := initImageBootableDataVolume(imageInfo, bootableStorageAttr, vmName, namespace)
	if err != nil {
		return nil, fmt.Errorf("initialize bootable CDI DataVolume for vm %s: %w", vmName, err)
	}
	return []kubevirtv1.DataVolumeTemplateSpec{*bootDv}, nil
}

func initImageBootableDataVolume(imageInfo *vivnfm.SoftwareImageInformation, bootableAttributes *vivnfm.VirtualStorageData, vmName string, namespace string) (*kubevirtv1.DataVolumeTemplateSpec, error) {
	if imageInfo == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "software image info", Reason: "cannot be nil"}
	}
	if imageInfo.Name == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "software image name", Reason: "cannot be empty"}
	}
	if imageInfo.Status != "ready" {
		return nil, fmt.Errorf("image %s (id: %s) not ready (actual state: %s): %w", imageInfo.Name, imageInfo.SoftwareImageId.Value, imageInfo.Status, apperrors.ErrInternal)
	}
	if bootableAttributes == nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "storage attributes for bootable disk", Reason: "can't be nil"}
	}

	zeroQ := resource.NewQuantity(0, resource.BinarySI)
	if imageInfo.Size == nil || imageInfo.GetSize().Equal(*zeroQ) {
		return nil, &apperrors.ErrInvalidArgument{Field: "software image size", Reason: "cannot be zero"}
	}
	if bootableAttributes.SizeOfStorage == nil || bootableAttributes.SizeOfStorage.Equal(*zeroQ) {
		return nil, &apperrors.ErrInvalidArgument{Field: "size of bootable disk", Reason: "cannot be zero"}
	}
	if imageInfo.GetSize().Cmp(*bootableAttributes.SizeOfStorage) == 1 {
		return nil, &apperrors.ErrInvalidArgument{Field: "size of bootable disk", Reason: "can't be less that image size"}
	}

	dvName := fmt.Sprintf("%s-boot-dv", vmName)
	return &kubevirtv1.DataVolumeTemplateSpec{
		ObjectMeta: v1.ObjectMeta{
			Name: dvName,
			Labels: map[string]string{
				common.K8sManagedByLabel: common.KubeNfvName,
				image.K8sImageIdLabel:    imageInfo.SoftwareImageId.GetValue(),
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				PVC: &v1beta1.DataVolumeSourcePVC{
					Namespace: namespace,
					Name:      imageInfo.Name,
				},
			},
			Storage: &v1beta1.StorageSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *bootableAttributes.SizeOfStorage,
					},
				},
				AccessModes: []corev1.PersistentVolumeAccessMode{
					// TODO: Temporary solution to make it works with ReadWriteOnce sc.
					corev1.ReadWriteOnce,
				},
			},
		},
	}, nil
}

func (m *manager) createUserDataVolumeWithSecret(ctx context.Context, namespace, vmName string, userData *vivnfm.UserData) (*kubevirtv1.Volume, *kubevirtv1.Disk, error) {
	if userData.Content == "" {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "userData content", Reason: "cannot be empty"}
	}
	if userData.Method == nil {
		return nil, nil, &apperrors.ErrInvalidArgument{Field: "userData method", Reason: "cannot be nil"}
	}

	// KubeVirt enforces a 2048-byte limit on inline userData for both
	// cloudInitConfigDrive and cloudInitNoCloud. Persisting the content in
	// a Secret and referencing it via UserDataSecretRef removes that limit.
	secretName := vmName + KubevirtVmCloudInitSecretSuffix
	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				common.K8sManagedByLabel:       common.KubeNfvName,
				kubevirtv1.VirtualMachineLabel: vmName,
			},
		},
		Data: map[string][]byte{
			"userdata": []byte(userData.Content),
		},
	}
	if err := m.client.Create(ctx, secret); err != nil {
		if !k8s_errors.IsAlreadyExists(err) {
			return nil, nil, fmt.Errorf("create cloud-init secret '%s': %w", secretName, err)
		}
		if err := m.client.Update(ctx, secret); err != nil {
			return nil, nil, fmt.Errorf("update cloud-init secret '%s': %w", secretName, err)
		}
	}

	volumeName := "cloudinitdisk"
	secretRef := &corev1.LocalObjectReference{Name: secretName}
	var volumeSource kubevirtv1.VolumeSource

	switch *userData.Method {
	case vivnfm.UserData_CONFIG_DRIVE_PLAINTEXT,
		vivnfm.UserData_CONFIG_DRIVE_MIME_MULTIPART:
		volumeSource = kubevirtv1.VolumeSource{
			CloudInitConfigDrive: &kubevirtv1.CloudInitConfigDriveSource{
				UserDataSecretRef: secretRef,
			},
		}
	case vivnfm.UserData_NO_CLOUD:
		volumeSource = kubevirtv1.VolumeSource{
			CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
				UserDataSecretRef: secretRef,
			},
		}
	case vivnfm.UserData_METADATA_SERVICE:
		return nil, nil, fmt.Errorf("userData metadata service method not supported in KubeVirt natively: %w", apperrors.ErrUnsupported)
	default:
		return nil, nil, fmt.Errorf("unsupported userData method '%v': %w", userData.Method, apperrors.ErrUnsupported)
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
