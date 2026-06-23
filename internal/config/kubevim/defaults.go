package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// SetViperDefaults registers every kube-vim configuration default with Viper.
// It is called from main before reading the configuration file so that values
// omitted from the file land in the unmarshaled Config as non-nil pointers.
//
// Defaults that depend on other resolved values (for example, deriving the
// NetworkAttachmentDefinition name from the management network name) are not
// expressed here — they live in Normalize, which is called after the file
// has been read.
func SetViperDefaults(podNamespace string) {
	viper.SetDefault("service.logLevel", "info")
	viper.SetDefault("service.server.port", 50051)

	viper.SetDefault("image.storageClass", "default")

	viper.SetDefault("k8s.namespace", podNamespace)

	viper.SetDefault("network.managementNetwork.enabled", false)
	viper.SetDefault("network.managementNetwork.name", "osm-mgmt")
	viper.SetDefault("network.managementNetwork.cidr", "10.240.0.0/24")

	viper.SetDefault("network.sriov.socketFile", "unix:/var/run/openvswitch/db.sock")
}

// Normalize fills in defaults that depend on other already-loaded values. It
// is called from NewKubeVimManager after Viper has unmarshaled the config so
// that downstream code (network manager, compute manager, etc.) can treat the
// resulting Config as fully resolved.
//
// k8sNamespace is the kube-vim deployment namespace, used to default the
// NetworkAttachmentDefinition namespace when the operator did not override it.
func (c *Config) Normalize(k8sNamespace string) error {
	if c == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if c.Network == nil {
		return nil
	}
	mgmt := c.Network.ManagementNetwork
	if mgmt == nil || mgmt.Enabled == nil || !*mgmt.Enabled {
		return nil
	}

	if mgmt.Name == nil || *mgmt.Name == "" {
		return fmt.Errorf("network.managementNetwork.name is required when enabled")
	}
	if mgmt.Cidr == nil || *mgmt.Cidr == "" {
		return fmt.Errorf("network.managementNetwork.cidr is required when enabled")
	}

	subnetName := fmt.Sprintf("%s-subnet-0", *mgmt.Name)
	if mgmt.NetAttachDefName == nil || *mgmt.NetAttachDefName == "" {
		derived := subnetName + "-netattach"
		mgmt.NetAttachDefName = &derived
	}
	if mgmt.NetAttachDefNamespace == nil || *mgmt.NetAttachDefNamespace == "" {
		if k8sNamespace == "" {
			return fmt.Errorf("k8s namespace is required to default network.managementNetwork.netAttachDefNamespace")
		}
		ns := k8sNamespace
		mgmt.NetAttachDefNamespace = &ns
	}
	return nil
}
