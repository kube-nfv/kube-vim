package kubevirt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	netattv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	commonv1 "github.com/kube-nfv/kube-vim-api/pkg/apis/common/v1"
	computev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/compute/v1"
	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	common "github.com/kube-nfv/kube-vim/internal/config"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s"
	"github.com/kube-nfv/kube-vim/internal/kubevim/compute"
	"github.com/kube-nfv/kube-vim/internal/kubevim/flavour"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	"github.com/kube-nfv/kube-vim/internal/kubevim/network"
	"github.com/kube-nfv/kube-vim/internal/misc"
	"google.golang.org/protobuf/encoding/protojson"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	KubevirtVirtualMachineInstanceTypeKind = "VirtualMachineInstanceType"
	KubevirtVirtualMachinePreferenceKind   = "VirtualMachinePreference"

	KubevirtVmMgmtNetworkName       = "default"
	KubevirtVmCloudInitSecretSuffix = "-cloud-init"
)

const (
	// vmiCreationTimeout bounds how long CreateCompute waits for KubeVirt
	// to create the VMI after the VM is created (scheduler + virt-controller).
	vmiCreationTimeout = time.Second * 30
	vmiPollInterval    = time.Millisecond * 500
)

// IdIndexes are the cache indexes the manager looks computes up by.
var IdIndexes = []k8s.LabelIndex{
	{Obj: &kubevirtv1.VirtualMachine{}, Label: compute.K8sComputeIdLabel},
}

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

func (m *manager) CreateCompute(ctx context.Context, req *computev1.CreateComputeRequest) (*computev1.Compute, error) {
	if err := checkSupported(req); err != nil {
		return nil, err
	}
	namespace := *m.cfg.Namespace
	stored := storedRequest(req)
	nameInterfaces(stored.NetworkInterfaces)
	c := stored.GetCompute()

	flav, err := m.flavourManager.GetFlavour(ctx, c.GetFlavourId())
	if err != nil {
		return nil, fmt.Errorf("get flavour '%s': %w", c.GetFlavourId(), err)
	}
	img, err := m.imageManager.GetImage(ctx, c.GetImageId())
	if err != nil {
		return nil, fmt.Errorf("get image '%s': %w", c.GetImageId(), err)
	}
	computeId := uuid.NewString()
	vmName := "vm-" + computeId
	bootDv, err := bootDataVolume(img, flav.GetStorageAttributes(), vmName, computeId, namespace)
	if err != nil {
		return nil, err
	}
	dvs := []kubevirtv1.DataVolumeTemplateSpec{*bootDv}
	volumes, disks := initVolumesDisksFromDataVolumes(dvs)

	networks, interfaces, netAnnotations, err := m.buildInterfaces(ctx, stored.NetworkInterfaces)
	if err != nil {
		return nil, fmt.Errorf("initialize kubevirt networks: %w", err)
	}
	raw, err := protojson.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal compute: %w", err)
	}

	var secret *corev1.Secret
	if req.GetUserData() != nil {
		var volume *kubevirtv1.Volume
		var disk *kubevirtv1.Disk
		secret, volume, disk, err = m.createUserDataSecret(ctx, namespace, vmName, req.GetUserData())
		if err != nil {
			return nil, fmt.Errorf("initialize vm userdata volume: %w", err)
		}
		volumes = append(volumes, *volume)
		disks = append(disks, *disk)
	}

	vmSpec := m.virtualMachine(vmName, computeId, c, flav.GetMetadata(), dvs, volumes, disks, networks, interfaces, netAnnotations)
	vmSpec.Annotations = map[string]string{compute.K8sComputeAnnotation: string(raw)}
	if c.GetComputeName() != "" {
		if vmSpec.Spec.Template.Spec.Hostname, err = formatVmName(c.GetComputeName()); err != nil {
			return nil, err
		}
	}
	if err := m.client.Create(ctx, vmSpec); err != nil {
		if secret != nil {
			_ = m.client.Delete(context.WithoutCancel(ctx), secret)
		}
		return nil, fmt.Errorf("create kubevirt VirtualMachine '%s': %w", vmName, err)
	}
	vmi, err := m.waitForVmi(ctx, vmName, namespace)
	if err != nil {
		err = fmt.Errorf("await VMI for VM '%s': %w", vmName, err)
		if rbErr := m.rollback(ctx, vmSpec, secret); rbErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback compute '%s': %w", computeId, rbErr))
		}
		return nil, err
	}
	return computeFromVm(vmSpec, vmi, m.getLauncherInfo(ctx, vmi))
}

// rollback deletes the VM and user-data Secret of a failed create; the caller never got their id.
func (m *manager) rollback(ctx context.Context, vm *kubevirtv1.VirtualMachine, secret *corev1.Secret) error {
	ctx = context.WithoutCancel(ctx)
	var errs []error
	if err := m.client.Delete(ctx, vm); err != nil && !k8s_errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete VirtualMachine '%s': %w", vm.Name, err))
	}
	if secret != nil {
		if err := m.client.Delete(ctx, secret); err != nil && !k8s_errors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete Secret '%s': %w", secret.Name, err))
		}
	}
	return errors.Join(errs...)
}

// virtualMachine builds the VM of compute c, sized by the flavour with metadata flavourMeta.
func (m *manager) virtualMachine(vmName, computeId string, c *computev1.Compute, flavourMeta map[string]string, dvs []kubevirtv1.DataVolumeTemplateSpec, volumes []kubevirtv1.Volume, disks []kubevirtv1.Disk, networks []kubevirtv1.Network, interfaces []kubevirtv1.Interface, annotations map[string]string) *kubevirtv1.VirtualMachine {
	runStrategy := kubevirtv1.RunStrategyAlways
	useSecureBoot := false
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name:      vmName,
			Namespace: *m.cfg.Namespace,
			Labels: map[string]string{
				kubevirtv1.VirtualMachineLabel: vmName,
				common.K8sManagedByLabel:       common.KubeNfvName,
				compute.K8sComputeIdLabel:      computeId,
				flavour.K8sFlavourIdLabel:      c.GetFlavourId(),
				image.K8sImageIdLabel:          c.GetImageId(),
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			DataVolumeTemplates: dvs,
			Instancetype: &kubevirtv1.InstancetypeMatcher{
				Kind: KubevirtVirtualMachineInstanceTypeKind,
				Name: flavourMeta[flavour.InstancetypeMetadataKey],
			},
			Preference: &kubevirtv1.PreferenceMatcher{
				Kind: KubevirtVirtualMachinePreferenceKind,
				Name: flavourMeta[flavour.PreferenceMetadataKey],
			},
			RunStrategy: &runStrategy,
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						common.K8sManagedByLabel:       common.KubeNfvName,
						kubevirtv1.VirtualMachineLabel: vmName,
					},
					Annotations: annotations,
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
			vm.Spec.Template.Spec.NodeSelector = *m.computeCfg.NodeSelector
		}
		if m.computeCfg.Tolerations != nil {
			vm.Spec.Template.Spec.Tolerations = misc.ToK8sTolerations(*m.computeCfg.Tolerations)
		}
	}
	return vm
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

func (m *manager) ListComputes(ctx context.Context) ([]*computev1.Compute, error) {
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

	res := make([]*computev1.Compute, 0, len(vmList.Items))
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		vmi, ok := vmiByName[vm.Name]
		if !ok {
			// VMI not present yet (VM just created, or brief cache lag). Skip it;
			// it will appear on a subsequent list rather than failing the whole call.
			continue
		}
		launcher := launcherInfoFromPod(selectLauncherPod(podsByVmiUID[string(vmi.UID)], vmi.Status.NodeName))
		c, err := computeFromVm(vm, vmi, launcher)
		if err != nil {
			return nil, err
		}
		res = append(res, c)
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

func (m *manager) GetCompute(ctx context.Context, computeId string) (*computev1.Compute, error) {
	vm, err := m.getVm(ctx, computeId)
	if err != nil {
		return nil, err
	}
	vmi := &kubevirtv1.VirtualMachineInstance{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vmi); err != nil {
		return nil, fmt.Errorf("get kubevirt VirtualMachineInstance of compute '%s': %w", computeId, err)
	}
	return computeFromVm(vm, vmi, m.getLauncherInfo(ctx, vmi))
}

// getVm returns the kube-nfv-owned VM of compute computeId.
func (m *manager) getVm(ctx context.Context, computeId string) (*kubevirtv1.VirtualMachine, error) {
	vmList := &kubevirtv1.VirtualMachineList{}
	if err := m.client.List(ctx, vmList, client.InNamespace(*m.cfg.Namespace),
		client.MatchingFields{compute.K8sComputeIdLabel: computeId}); err != nil {
		return nil, fmt.Errorf("list kubevirt VirtualMachines for compute '%s': %w", computeId, err)
	}
	switch len(vmList.Items) {
	case 0:
		return nil, &apperrors.ErrNotFound{Entity: "compute", Identifier: computeId}
	case 1:
	default:
		return nil, fmt.Errorf("%d VirtualMachines carry compute id '%s': %w", len(vmList.Items), computeId, apperrors.ErrInternal)
	}
	vm := &vmList.Items[0]
	if !misc.IsObjectManagedByKubeNfv(vm) {
		return nil, &apperrors.ErrK8sObjectNotManagedByKubeNfv{ObjectType: "VirtualMachine", ObjectName: vm.Name, ObjectId: string(vm.UID)}
	}
	return vm, nil
}

func (m *manager) DeleteCompute(ctx context.Context, computeId string) error {
	vm, err := m.getVm(ctx, computeId)
	if err != nil {
		return err
	}
	if err = m.client.Delete(ctx, vm); err != nil {
		return fmt.Errorf("delete kubevirt VirtualMachine '%s' of compute '%s': %w", vm.Name, computeId, err)
	}
	secret := &corev1.Secret{ObjectMeta: v1.ObjectMeta{Name: vm.Name + KubevirtVmCloudInitSecretSuffix, Namespace: vm.Namespace}}
	if err := m.client.Delete(ctx, secret); err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("delete cloud-init secret '%s' of compute '%s': %w", secret.Name, computeId, err)
	}
	return nil
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

// bootDataVolume builds the boot disk of a VM: a clone of the image's PVC, sized by the flavour's
// boot storage attribute, its only one kube-vim supports.
func bootDataVolume(img *imagev1.Image, storage []*commonv1.VirtualStorageData, vmName, computeId, namespace string) (*kubevirtv1.DataVolumeTemplateSpec, error) {
	if len(storage) != 1 || !storage[0].GetIsBoot() {
		return nil, fmt.Errorf("flavour storage other than exactly one boot disk: %w", apperrors.ErrUnsupported)
	}
	boot := storage[0]
	if boot.GetTypeOfStorage() != commonv1.TypeOfStorage_TYPE_OF_STORAGE_VOLUME || boot.GetStorageClass() != "" {
		return nil, fmt.Errorf("flavour boot disk other than a volume of the default storage class: %w", apperrors.ErrUnsupported)
	}
	if img.GetStatus() != imagev1.ImageStatus_IMAGE_STATUS_AVAILABLE {
		return nil, &apperrors.ErrInvalidState{Entity: "software image", Identifier: img.GetImageId(), Reason: fmt.Sprintf("is not available (status %s)", img.GetStatus())}
	}
	bootSize, err := resource.ParseQuantity(boot.GetSizeOfStorage())
	if err != nil {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour boot disk size", Reason: err.Error()}
	}
	if imgSize, err := resource.ParseQuantity(img.GetSize()); err == nil && imgSize.Cmp(bootSize) == 1 {
		return nil, &apperrors.ErrInvalidArgument{Field: "flavour boot disk size", Reason: fmt.Sprintf("%s is smaller than the image (%s)", boot.GetSizeOfStorage(), img.GetSize())}
	}
	return &kubevirtv1.DataVolumeTemplateSpec{
		ObjectMeta: v1.ObjectMeta{
			Name: vmName + "-boot-dv",
			Labels: map[string]string{
				common.K8sManagedByLabel:  common.KubeNfvName,
				compute.K8sComputeIdLabel: computeId,
				image.K8sImageIdLabel:     img.GetImageId(),
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				PVC: &v1beta1.DataVolumeSourcePVC{
					Namespace: namespace,
					Name:      img.GetMetadata()[image.PvcMetadataKey],
				},
			},
			Storage: &v1beta1.StorageSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: bootSize,
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

// createUserDataSecret stores the user data in a Secret and returns it with the VM volume and disk that pass it to the guest.
func (m *manager) createUserDataSecret(ctx context.Context, namespace, vmName string, userData *computev1.UserData) (*corev1.Secret, *kubevirtv1.Volume, *kubevirtv1.Disk, error) {
	// KubeVirt enforces a 2048-byte limit on inline userData for both
	// cloudInitConfigDrive and cloudInitNoCloud. Persisting the content in
	// a Secret and referencing it via UserDataSecretRef removes that limit.
	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      vmName + KubevirtVmCloudInitSecretSuffix,
			Namespace: namespace,
			Labels: map[string]string{
				common.K8sManagedByLabel:       common.KubeNfvName,
				kubevirtv1.VirtualMachineLabel: vmName,
			},
		},
		Data: map[string][]byte{
			"userdata": []byte(userData.GetContent()),
		},
	}
	if err := m.client.Create(ctx, secret); err != nil {
		return nil, nil, nil, fmt.Errorf("create cloud-init secret '%s': %w", secret.Name, err)
	}

	volumeName := "cloudinitdisk"
	secretRef := &corev1.LocalObjectReference{Name: secret.Name}
	var volumeSource kubevirtv1.VolumeSource
	switch userData.GetMethod() {
	case computev1.UserDataTransportationMethod_USER_DATA_TRANSPORTATION_METHOD_CONFIG_DRIVE,
		computev1.UserDataTransportationMethod_USER_DATA_TRANSPORTATION_METHOD_CONFIG_DRIVE_MIME_MULTIPART:
		volumeSource.CloudInitConfigDrive = &kubevirtv1.CloudInitConfigDriveSource{UserDataSecretRef: secretRef}
	default:
		// NoCloud when unset.
		volumeSource.CloudInitNoCloud = &kubevirtv1.CloudInitNoCloudSource{UserDataSecretRef: secretRef}
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
	return secret, volume, disk, nil
}
