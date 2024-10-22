package harvester

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/rancher/machine/libmachine/drivers"
	rpcdriver "github.com/rancher/machine/libmachine/drivers/rpc"
	"github.com/rancher/machine/libmachine/mcnflag"
)

const (
	defaultNamespace = "default"

	defaultCPU          = 2
	defaultMemorySize   = 4
	defaultDiskBus      = "virtio"
	defaultNetworkModel = "virtio"
)

func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_KUBECONFIG_CONTENT",
			Name:   "harvester-kubeconfig-content",
			Usage:  "contents of kubeconfig file for harvester cluster, base64 is supported",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_CLUSTER_TYPE",
			Name:   "harvester-cluster-type",
			Usage:  "harvester cluster type",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_CLUSTER_ID",
			Name:   "harvester-cluster-id",
			Usage:  "harvester cluster id",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_CLUSTER_NAME",
			Name:   "harvester-cluster-name",
			Usage:  "harvester cluster name",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_VM_NAMESPACE",
			Name:   "harvester-vm-namespace",
			Usage:  "harvester vm namespace",
			Value:  defaultNamespace,
		},
		mcnflag.IntFlag{
			EnvVar: "HARVESTER_CPU_COUNT",
			Name:   "harvester-cpu-count",
			Usage:  "number of CPUs for machine",
			Value:  defaultCPU,
		},
		mcnflag.IntFlag{
			EnvVar: "HARVESTER_MEMORY_SIZE",
			Name:   "harvester-memory-size",
			Usage:  "size of memory for machine (in GiB)",
			Value:  defaultMemorySize,
		},
		mcnflag.IntFlag{
			EnvVar: "HARVESTER_DISK_SIZE",
			Name:   "harvester-disk-size",
			Usage:  "size of disk for machine (in GiB)",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_DISK_BUS",
			Name:   "harvester-disk-bus",
			Usage:  "bus of disk for machine",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_IMAGE_NAME",
			Name:   "harvester-image-name",
			Usage:  "harvester image name",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_DISK_INFO",
			Name:   "harvester-disk-info",
			Usage:  "harvester disk info",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_SSH_USER",
			Name:   "harvester-ssh-user",
			Usage:  "SSH username",
			Value:  drivers.DefaultSSHUser,
		},
		mcnflag.IntFlag{
			EnvVar: "HARVESTER_SSH_PORT",
			Name:   "harvester-ssh-port",
			Usage:  "SSH port",
			Value:  drivers.DefaultSSHPort,
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_SSH_PASSWORD",
			Name:   "harvester-ssh-password",
			Usage:  "SSH password",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_KEY_PAIR_NAME",
			Name:   "harvester-key-pair-name",
			Usage:  "harvester key pair name",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_SSH_PRIVATE_KEY_PATH",
			Name:   "harvester-ssh-private-key-path",
			Usage:  "SSH private key path ",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_NETWORK_TYPE",
			Name:   "harvester-network-type",
			Usage:  "harvester network type",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_NETWORK_NAME",
			Name:   "harvester-network-name",
			Usage:  "harvester network name",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_NETWORK_MODEL",
			Name:   "harvester-network-model",
			Usage:  "harvester network model",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_NETWORK_INFO",
			Name:   "harvester-network-info",
			Usage:  "harvester network info",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_CLOUD_CONFIG",
			Name:   "harvester-cloud-config",
			Usage:  "just keep it empty, this value will be filled by rancher-machine",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_USER_DATA",
			Name:   "harvester-user-data",
			Usage:  "userData content of cloud-init for machine, base64 is supported",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_NETWORK_DATA",
			Name:   "harvester-network-data",
			Usage:  "networkData content of cloud-init for machine, base64 is supported",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_VM_AFFINITY",
			Name:   "harvester-vm-affinity",
			Usage:  "harvester vm affinity, base64 is supported",
		},
		mcnflag.BoolFlag{
			EnvVar: "HARVESTER_ENABLE_EFI",
			Name:   "harvester-enable-efi",
			Usage:  "enable vm efi",
		},
		mcnflag.BoolFlag{
			EnvVar: "HARVESTER_ENABLE_SECURE_BOOT",
			Name:   "harvester-enable-secure-boot",
			Usage:  "enable vm secure boot, only works when enable efi",
		},
		mcnflag.StringFlag{
			EnvVar: "HARVESTER_VGPU_INFO",
			Name:   "harvester-vgpu-info",
			Usage:  "harvester-vgpu-info",
		},
	}
}

func (d *Driver) UnmarshalJSON(data []byte) error {
	// use type alias to prevent recursively calling UnmarshalJSON
	type targetDriver Driver

	// copy data from existing driver
	target := targetDriver(*d)

	if err := json.Unmarshal(data, &target); err != nil {
		return fmt.Errorf("error unmarshalling driver config JSON: %w", err)
	}

	*d = Driver(target)

	// make sure to reload values that are subject to change from environment or
	// os.Args
	driverOpts := rpcdriver.GetDriverOpts(d.GetCreateFlags(), os.Args)

	if _, ok := driverOpts.Values["harvester-kubeconfig-content"]; ok {
		d.KubeConfigContent = stringSupportBase64(driverOpts.String("harvester-kubeconfig-content"))
	}

	return nil
}

func (d *Driver) SetConfigFromFlags(flags drivers.DriverOptions) error {
	d.KubeConfigContent = stringSupportBase64(flags.String("harvester-kubeconfig-content"))

	d.VMNamespace = flags.String("harvester-vm-namespace")
	d.VMAffinity = stringSupportBase64(flags.String("harvester-vm-affinity"))
	d.ClusterType = flags.String("harvester-cluster-type")
	d.ClusterID = flags.String("harvester-cluster-id")
	d.ClusterName = flags.String("harvester-cluster-name")

	d.CPU = flags.Int("harvester-cpu-count")
	d.MemorySize = fmt.Sprintf("%dGi", flags.Int("harvester-memory-size"))
	d.DiskSize = strconv.Itoa(flags.Int("harvester-disk-size"))
	d.DiskBus = flags.String("harvester-disk-bus")

	d.ImageName = flags.String("harvester-image-name")

	diskInfoStr := flags.String("harvester-disk-info")
	if diskInfoStr != "" {
		diskInfo, err := UnmarshalDiskInfo([]byte(diskInfoStr))
		if err != nil {
			return err
		}
		d.DiskInfo = &diskInfo
	}

	d.SSHUser = flags.String("harvester-ssh-user")
	d.SSHPort = flags.Int("harvester-ssh-port")

	d.KeyPairName = flags.String("harvester-key-pair-name")
	d.SSHPrivateKeyPath = flags.String("harvester-ssh-private-key-path")
	d.SSHPassword = flags.String("harvester-ssh-password")

	d.NetworkType = flags.String("harvester-network-type")

	d.NetworkName = flags.String("harvester-network-name")
	d.NetworkModel = flags.String("harvester-network-model")

	networkInfoStr := flags.String("harvester-network-info")
	if networkInfoStr != "" {
		networkInfo, err := UnmarshalNetworkInfo([]byte(networkInfoStr))
		if err != nil {
			return err
		}
		d.NetworkInfo = &networkInfo
	}

	d.CloudConfig = flags.String("harvester-cloud-config")
	d.UserData = stringSupportBase64(flags.String("harvester-user-data"))
	d.NetworkData = stringSupportBase64(flags.String("harvester-network-data"))

	d.EnableEFI = flags.Bool("harvester-enable-efi")
	d.EnableSecureBoot = flags.Bool("harvester-enable-secure-boot")
	if d.EnableSecureBoot && !d.EnableEFI {
		return fmt.Errorf("enable secure boot requires enable EFI")
	}

	d.SetSwarmConfigFromFlags(flags)

	vGPUInfoString := flags.String("harvester-vgpu-info")
	if vGPUInfoString != "" {
		vGPUInfo, err := parseVGPUInfo(vGPUInfoString)
		if err != nil {
			return err
		}
		d.VGPUInfo = vGPUInfo
	}
	return d.checkConfig()
}

func stringSupportBase64(value string) string {
	if value == "" {
		return value
	}
	valueByte, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		valueByte = []byte(value)
	}
	return string(valueByte)
}
