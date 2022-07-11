package envoy

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
	"k8s.io/klog/v2"
)

const (
	EnvoyNodeID     = "envoy"
	EnvoyClusterID  = "envoy"
	EnvoyADSAddress = "127.0.0.1"
	EnvoyADSPort    = 18000
	EnvoyAdminPort  = 19000
)

//go:embed bootstrap.yaml
var bootstrapTemplate string

type BootstrapConfig struct {
	NodeID     string
	ClusterID  string
	ADSAddress string
	ADSPort    int
	AdminPort  int
}

func (b BootstrapConfig) AsString() (string, error) {
	templateOutput := bytes.NewBufferString("")

	klog.V(3).Infof("Creating bootstrap config file for envoy...")

	if b.NodeID == "" {
		klog.V(3).Infof("Bootstrap config: setting default node ID")
		b.NodeID = EnvoyNodeID
	}
	if b.ClusterID == "" {
		klog.V(3).Infof("Bootstrap config: setting default cluster ID")
		b.ClusterID = EnvoyClusterID
	}
	if b.ADSAddress == "" {
		klog.V(3).Infof("Bootstrap config: setting default ADS address")
		b.ADSAddress = EnvoyADSAddress
	}
	if b.ADSPort == 0 {
		klog.V(3).Infof("Bootstrap config: setting default ADS port")
		b.ADSPort = EnvoyADSPort
	}
	if b.AdminPort == 0 {
		klog.V(3).Infof("Bootstrap config: setting default admin port")
		b.AdminPort = EnvoyAdminPort
	}

	t := template.Must(template.New("bootstrap").Parse(bootstrapTemplate))
	if err := t.Execute(templateOutput, b); err != nil {
		return "", fmt.Errorf("when replacing in envoy config template: %w", err)
	}

	// simple validation of the YAML file
	var out interface{}
	if err := yaml.Unmarshal(templateOutput.Bytes(), &out); err != nil {
		return "", fmt.Errorf("when validating envoy config: %w", err)
	}

	return templateOutput.String(), nil
}

func (b BootstrapConfig) WriteToFile(f *os.File) error {
	out, err := b.AsString()
	if err != nil {
		return err
	}

	klog.V(3).Infof("Writting bootstrap config file to %q...", f.Name())
	if _, err := f.WriteString(out); err != nil {
		return fmt.Errorf("when creating envoy config file: %w", err)
	}

	return nil
}
