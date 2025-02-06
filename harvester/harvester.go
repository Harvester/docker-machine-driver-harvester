package harvester

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rancher/machine/libmachine/drivers"
	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/mcnutils"
	"github.com/rancher/machine/libmachine/state"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubevirtv1 "kubevirt.io/api/core/v1"

	harvesterutil "github.com/harvester/harvester/pkg/util"
)

const (
	driverName    = "harvester"
	vmResource    = "virtualmachines"
	actionStart   = "start"
	actionStop    = "stop"
	actionRestart = "restart"

	removeAllPVCsAnnotationKey = "harvesterhci.io/removeAllPersistentVolumeClaims"
)

// Driver is the driver used when no driver is selected. It is used to
// connect to existing Docker hosts by specifying the URL of the host as
// an option.
type Driver struct {
	*drivers.BaseDriver

	client *Client
	ctx    context.Context

	KubeConfigContent string

	VMNamespace string
	VMAffinity  string
	ClusterType string
	ClusterID   string
	ClusterName string

	ServerVersion string

	CPU        int
	MemorySize string
	DiskSize   string
	DiskBus    string

	ImageName string

	DiskInfo *DiskInfo

	KeyPairName       string
	SSHPrivateKeyPath string
	SSHPublicKey      string
	SSHPassword       string

	AddUserToDockerGroup bool

	NetworkType string

	NetworkName  string
	NetworkModel string

	NetworkInfo *NetworkInfo

	CloudConfig string
	UserData    string
	NetworkData string

	EnableEFI        bool
	EnableSecureBoot bool
	VGPUInfo         *VGPUInfo

	CPUPinning            bool
	IsolateEmulatorThread bool
}

func NewDriver(hostName, storePath string) *Driver {
	return &Driver{
		BaseDriver: &drivers.BaseDriver{
			MachineName: hostName,
			StorePath:   storePath,
		},
		ctx: context.Background(),
	}
}

// DriverName returns the name of the driver
func (d *Driver) DriverName() string {
	return driverName
}

func (d *Driver) GetSSHHostname() (string, error) {
	return d.GetIP()
}

func (d *Driver) GetURL() (string, error) {
	if err := drivers.MustBeRunning(d); err != nil {
		return "", err
	}

	ip, err := d.GetIP()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("tcp://%s", net.JoinHostPort(ip, "2376")), nil
}

func (d *Driver) GetIP() (string, error) {
	if err := drivers.MustBeRunning(d); err != nil {
		return "", err
	}

	vmi, err := d.getVMI()
	if err != nil {
		return "", err
	}

	addr := strings.Split(vmi.Status.Interfaces[0].IP, "/")[0]
	if ip := net.ParseIP(addr); ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("%s is not a valid IPv4 address", addr)
	}

	return addr, nil
}

func (d *Driver) GetState() (state.State, error) {
	if _, err := d.getVM(); err != nil {
		return state.None, err
	}

	vmi, err := d.getVMI()
	if err != nil {
		if apierrors.IsNotFound(err) {
			return state.Stopped, nil
		}
		return state.None, err
	}
	return getStateFormVMI(vmi), nil
}

func getStateFormVMI(vmi *kubevirtv1.VirtualMachineInstance) state.State {
	switch vmi.Status.Phase {
	case "Pending", "Scheduling", "Scheduled":
		return state.Starting
	case "Running":
		return state.Running
	case "Succeeded":
		return state.Stopping
	case "Failed":
		return state.Error
	default:
		return state.None
	}
}

func (d *Driver) waitRemoved() error {
	removed := func() bool {
		if _, err := d.getVM(); err != nil {
			if apierrors.IsNotFound(err) {
				return true
			}
		}
		return false
	}
	log.Debugf("Waiting for node removed")
	if err := mcnutils.WaitForSpecific(removed, 120, 5*time.Second); err != nil {
		return fmt.Errorf("Too many retries waiting for machine removed.  Last error: %s", err)
	}
	return nil
}

func (d *Driver) Remove() error {
	log.Debugf("Remove node")
	vm, err := d.getVM()
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	removeAll := false
	if value, ok := vm.Annotations[removeAllPVCsAnnotationKey]; ok && value == "true" {
		log.Debugf("Force the removal of all persistent volume claims")
		removeAll = true
	}

	removedPVCs := make([]string, 0, len(vm.Spec.Template.Spec.Volumes))
	for _, volume := range vm.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil || (!removeAll && volume.PersistentVolumeClaim.Hotpluggable) {
			continue
		}
		removedPVCs = append(removedPVCs, volume.PersistentVolumeClaim.ClaimName)
	}
	vmCopy := vm.DeepCopy()
	vmCopy.Annotations[harvesterutil.RemovedPVCsAnnotationKey] = strings.Join(removedPVCs, ",")
	if _, err = d.updateVM(vmCopy); err != nil {
		return err
	}
	if err = d.deleteVM(); err != nil {
		return err
	}
	return d.waitRemoved()
}

func (d *Driver) Restart() error {
	log.Debugf("Restart node")
	vmi, err := d.getVMI()
	if err != nil {
		return err
	}
	oldUID := string(vmi.UID)

	if err = d.putVMSubResource(actionRestart); err != nil {
		return err
	}

	return d.waitForRestart(oldUID)
}

func (d *Driver) Start() error {
	log.Debugf("Start node")
	if err := d.putVMSubResource(actionStart); err != nil {
		return err
	}
	return d.waitForReady()
}

func (d *Driver) Stop() error {
	log.Debugf("Stop node")
	if err := d.putVMSubResource(actionStop); err != nil {
		return err
	}
	return d.waitForState(state.Stopped)
}

func (d *Driver) Kill() error {
	return d.Stop()
}
