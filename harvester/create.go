package harvester

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"time"

	"github.com/rancher/machine/libmachine/drivers"
	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/mcnutils"
	"github.com/rancher/machine/libmachine/ssh"
	"github.com/rancher/machine/libmachine/state"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/pointer"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/harvester/harvester/pkg/builder"
)

const (
	diskNamePrefix      = "disk"
	interfaceNamePrefix = "nic"
	poolNameLabelKey    = "harvesterhci.io/machineSetName"
	clusterNameLabelKey = "cluster.kubernetes.io/name"
)

func (d *Driver) Create() error {
	// create keypair
	if err := d.createKeyPair(); err != nil {
		return err
	}
	// create vm
	cloudInitSource, cloudConfigSecret, err := d.buildCloudInit()
	if err != nil {
		return err
	}
	vmBuilder := builder.NewVMBuilder("docker-machine-driver-harvester").
		Namespace(d.VMNamespace).Name(d.MachineName).CPU(d.CPU).Memory(d.MemorySize).
		CloudInitDisk(builder.CloudInitDiskName, builder.DiskBusVirtio, false, 0, *cloudInitSource).
		EvictionStrategy(true).RunStrategy(kubevirtv1.RunStrategyRerunOnFailure)

	if d.ClusterName != "" {
		vmBuilder.Labels(labels.Set{
			clusterNameLabelKey: d.ClusterName,
		})
	}

	// affinity
	var affinity *corev1.Affinity
	if d.VMAffinity != "" {
		if err = json.Unmarshal([]byte(d.VMAffinity), &affinity); err != nil {
			return err
		}
		//VM naming convention is of form: clusterName-poolName-generatedString
		//we can reverse split this to identify unique machinesets name, to label nodes
		//with this unique machine set. This can then be used for populating affinity rules
		machineSetSplit := strings.Split(d.MachineName, "-")
		machineSetSplit = append([]string{d.VMNamespace}, machineSetSplit...)
		machineSetName := strings.Join(machineSetSplit[:len(machineSetSplit)-2], "-")
		vmBuilder = vmBuilder.Labels(map[string]string{poolNameLabelKey: machineSetName})
		addtionalPodAffinityTerm := corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					poolNameLabelKey: machineSetName,
				},
			},
			TopologyKey: "kubernetes.io/hostname",
		}
		additionalWeightPodAffinity := corev1.WeightedPodAffinityTerm{
			Weight:          1,
			PodAffinityTerm: addtionalPodAffinityTerm,
		}
		if affinity.PodAffinity != nil {
			affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution, additionalWeightPodAffinity)
		}

		if affinity.PodAntiAffinity != nil {
			affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, additionalWeightPodAffinity)
		}
	}
	vmBuilder = vmBuilder.Affinity(affinity)
	// ssh key
	if d.KeyPairName != "" {
		vmBuilder = vmBuilder.SSHKey(d.KeyPairName)
	}
	// network interfaces
	vmBuilder = d.NetworkInterfaces(vmBuilder)

	// add vGPU info
	vmBuilder = d.ConfigureVGPU(vmBuilder)
	// disks
	vmBuilder, err = d.Disks(vmBuilder)
	if err != nil {
		return err
	}
	// vm
	vm, err := vmBuilder.VM()
	if err != nil {
		return err
	}
	vm.Kind = kubevirtv1.VirtualMachineGroupVersionKind.Kind
	vm.APIVersion = kubevirtv1.GroupVersion.String()

	if d.EnableEFI {
		if vm.Spec.Template.Spec.Domain.Features == nil {
			vm.Spec.Template.Spec.Domain.Features = &kubevirtv1.Features{}
		}
		v := d.EnableSecureBoot
		vm.Spec.Template.Spec.Domain.Features.SMM = &kubevirtv1.FeatureState{Enabled: &v}
		vm.Spec.Template.Spec.Domain.Firmware = &kubevirtv1.Firmware{Bootloader: &kubevirtv1.Bootloader{EFI: &kubevirtv1.EFI{SecureBoot: &v}}}
	}
	createdVM, err := d.createVM(vm)
	if err != nil {
		return err
	}
	// create secret
	if cloudConfigSecret != nil {
		cloudConfigSecret.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: vm.APIVersion,
				Kind:       vm.Kind,
				Name:       vm.Name,
				UID:        createdVM.UID,
			},
		}
		if _, err = d.createSecret(cloudConfigSecret); err != nil {
			return err
		}
	}
	// wait vm ready
	if err = d.waitForReady(); err != nil {
		return err
	}
	ip, err := d.GetIP()
	if err != nil {
		return err
	}
	d.IPAddress = ip
	log.Debugf("Get machine ip: %s", d.IPAddress)
	return nil
}

func (d *Driver) waitForState(desiredState state.State) error {
	log.Debugf("Waiting for node become %s", desiredState)
	if err := mcnutils.WaitForSpecific(drivers.MachineInState(d, desiredState), 120, 5*time.Second); err != nil {
		return fmt.Errorf("Too many retries waiting for machine to be %s.  Last error: %s", desiredState, err)
	}
	return nil
}

func (d *Driver) waitForIP() error {
	ipIsNotEmpty := func() bool {
		ip, _ := d.GetIP()
		return ip != ""
	}
	log.Debugf("Waiting for node get ip")
	if err := mcnutils.WaitForSpecific(ipIsNotEmpty, 120, 5*time.Second); err != nil {
		return fmt.Errorf("Too many retries waiting for get machine's ip.  Last error: %s", err)
	}
	return nil
}

func (d *Driver) waitForReady() error {
	if err := d.waitForState(state.Running); err != nil {
		return err
	}
	return d.waitForIP()
}

func (d *Driver) waitForRestart(oldUID string) error {
	restarted := func() bool {
		vmi, err := d.getVMI()
		if err != nil {
			return false
		}
		return oldUID != string(vmi.UID)
	}
	log.Debugf("Waiting for node restarted")
	if err := mcnutils.WaitForSpecific(restarted, 120, 5*time.Second); err != nil {
		return fmt.Errorf("Too many retries waiting for machine restart.  Last error: %s", err)
	}
	return d.waitForReady()
}

func (d *Driver) createKeyPair() error {
	keyPath := d.GetSSHKeyPath()
	publicKeyFile := keyPath + ".pub"
	if d.SSHPrivateKeyPath == "" {
		log.Debugf("Creating New SSH Key")
		if err := ssh.GenerateSSHKey(keyPath); err != nil {
			return err
		}
	} else {
		log.Debugf("Using SSHPrivateKeyPath: %s", d.SSHPrivateKeyPath)
		if err := mcnutils.CopyFile(d.SSHPrivateKeyPath, keyPath); err != nil {
			return err
		}
		if d.KeyPairName != "" {
			log.Debugf("Using existing harvester key pair: %s", d.KeyPairName)
			return nil
		}
		if err := mcnutils.CopyFile(d.SSHPrivateKeyPath+".pub", publicKeyFile); err != nil {
			return err
		}
	}

	publicKey, err := ioutil.ReadFile(publicKeyFile)
	if err != nil {
		return err
	}
	log.Debugf("Using public Key: %s", publicKeyFile)
	d.SSHPublicKey = string(publicKey)
	return nil
}

func (d *Driver) addDisk(vmBuilder *builder.VMBuilder, disk *Disk, diskIndex int) (*builder.VMBuilder, error) {
	diskName := fmt.Sprintf("%s-%d", diskNamePrefix, diskIndex)
	if disk.Bus == "" {
		disk.Bus = defaultDiskBus
	}
	if disk.Type == "" {
		disk.Type = builder.DiskTypeDisk
	}
	isCDRom := disk.Type == builder.DiskTypeCDRom
	var imageID string
	if disk.ImageName != "" {
		imageNamespace, imageName, err := NamespacedNamePartsByDefault(disk.ImageName, d.VMNamespace)
		if err != nil {
			return nil, err
		}
		vmimage, err := d.client.HarvesterClient.HarvesterhciV1beta1().VirtualMachineImages(imageNamespace).Get(d.ctx, imageName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		imageID = fmt.Sprintf("%s/%s", imageNamespace, imageName)
		disk.StorageClassName = vmimage.Status.StorageClassName
	}
	pvcOption := &builder.PersistentVolumeClaimOption{
		ImageID:          imageID,
		StorageClassName: pointer.StringPtr(disk.StorageClassName),
		VolumeMode:       corev1.PersistentVolumeBlock,
		AccessMode:       corev1.ReadWriteMany,
	}
	return vmBuilder.PVCDisk(diskName, disk.Bus, isCDRom, disk.HotPlugAble, disk.BootOrder, fmt.Sprintf("%dGi", disk.Size), "", pvcOption), nil
}

func (d *Driver) Disks(vmBuilder *builder.VMBuilder) (*builder.VMBuilder, error) {
	var err error
	if d.DiskInfo != nil {
		for i, disk := range d.DiskInfo.Disks {
			vmBuilder, err = d.addDisk(vmBuilder, &disk, i)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// Compatible with older versions
		diskSize, err := strconv.Atoi(d.DiskSize)
		if err != nil {
			return nil, err
		}
		disk := Disk{
			ImageName:   d.ImageName,
			Bus:         d.DiskBus,
			Type:        builder.DiskTypeDisk,
			Size:        diskSize,
			BootOrder:   1,
			HotPlugAble: false,
		}
		vmBuilder, err = d.addDisk(vmBuilder, &disk, 1)
		if err != nil {
			return nil, err
		}
	}
	return vmBuilder, nil
}

func (d *Driver) AddNetworkInterface(vmBuilder *builder.VMBuilder, networkInterface *NetworkInterface, interfaceIndex int) *builder.VMBuilder {
	interfaceName := fmt.Sprintf("%s-%d", interfaceNamePrefix, interfaceIndex)
	if networkInterface.Type == "" {
		networkInterface.Type = builder.NetworkInterfaceTypeBridge
	}
	if networkInterface.Model == "" {
		networkInterface.Model = defaultNetworkModel
	}
	return vmBuilder.NetworkInterface(interfaceName, networkInterface.Model, networkInterface.MACAddress, networkInterface.Type, networkInterface.NetworkName)
}

func (d *Driver) NetworkInterfaces(vmBuilder *builder.VMBuilder) *builder.VMBuilder {
	if d.NetworkInfo != nil {
		for i, networkInterface := range d.NetworkInfo.NetworkInterfaces {
			d.AddNetworkInterface(vmBuilder, &networkInterface, i)
		}
	} else {
		// Compatible with older versions
		networkInterface := NetworkInterface{
			NetworkName: d.NetworkName,
			Model:       d.NetworkModel,
			MACAddress:  "",
			Type:        builder.NetworkInterfaceTypeBridge,
		}
		d.AddNetworkInterface(vmBuilder, &networkInterface, 0)
	}
	return vmBuilder
}

// ConfigureVGPU will configure vmBuilder with vGPUInfo passed through driver
func (d *Driver) ConfigureVGPU(vmBuilder *builder.VMBuilder) *builder.VMBuilder {
	if d.VGPUInfo == nil {
		return vmBuilder
	}

	for _, v := range d.VGPUInfo.VGPURequests {
		// pass name, deviceName, tags(if any), and vGPUOptions if any
		vmBuilder = vmBuilder.GPU(v.Name, v.DeviceName, "", nil)
	}
	return vmBuilder
}
