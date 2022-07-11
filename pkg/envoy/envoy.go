package envoy

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	"github.com/inercia/envoy-e2e-utils/pkg/utils"
)

var (
	ErrEnvoyAlreadyRunning = errors.New("envoy is already running")

	ErrEnvoyNotRunning = errors.New("envoy is not running")
)

type EnvoyProcess struct {
	sync.Mutex

	BootstrapConfig

	proc   *exec.Cmd
	config *os.File
	done   chan struct{}

	ExitError error
	ExitCode  int
}

func NewEnvoyProcess(bc BootstrapConfig) *EnvoyProcess {
	res := &EnvoyProcess{
		BootstrapConfig: bc,
		done:            make(chan struct{}, 1),
	}

	if res.NodeID == "" {
		res.NodeID = EnvoyNodeID
	}
	if res.ClusterID == "" {
		res.ClusterID = EnvoyClusterID
	}
	if res.ADSAddress == "" {
		res.ADSAddress = EnvoyADSAddress
	}
	if res.ADSPort == 0 {
		res.ADSPort = EnvoyADSPort
	}

	return res
}

func (e *EnvoyProcess) Start(ctx context.Context) error {
	e.Lock()
	defer e.Unlock()

	var err error

	if e.proc != nil {
		return ErrEnvoyAlreadyRunning
	}

	klog.V(3).Infof("Bootstrap config:")
	templateOutput, err := e.BootstrapConfig.AsString()
	if err != nil {
		return fmt.Errorf("when rendering envoy config: %w", err)
	}
	for _, line := range strings.Split(templateOutput, "\n") {
		klog.V(3).Info(line)
	}

	klog.V(3).Infof("Creating temporary bootstrap file for envoy...")
	if e.config, err = ioutil.TempFile("", "envoy-bootstrap-*.yaml"); err != nil {
		return fmt.Errorf("when creating envoy config file: %w", err)
	}
	if err := e.BootstrapConfig.WriteToFile(e.config); err != nil {
		return fmt.Errorf("when rendering envoy config: %w", err)
	}

	args := []string{
		"-c", e.config.Name(),
	}
	if e.NodeID != "" {
		args = append(args, "--service-node", e.NodeID)
	}
	if e.ClusterID != "" {
		args = append(args, "--service-cluster", e.ClusterID)
	}

	klog.V(3).Infof("Starting envoy with 'envoy %s'", strings.Join(args, " "))

	proc := exec.Command("envoy", args...)
	proc.Stdout = envoyLoglineToKlog
	proc.Stderr = envoyLoglineToKlog
	e.proc = proc

	err = e.proc.Start()
	if err != nil {
		return fmt.Errorf("when starting envoy: %w", err)
	}

	go func() {
		// This will kill the program when the context is done
		<-ctx.Done()
		klog.V(3).Infof("Context done: cleaning up things for envoy...")
		e.Kill()
	}()

	go func() {
		// This will block until is program is killed/done, and then it will close the "done" channel.
		res := proc.Wait()
		klog.V(3).Infof("Envoy did quit: closing done channel")

		e.Lock()
		defer e.Unlock()

		e.ExitError = res
		e.ExitCode = proc.ProcessState.ExitCode()

		close(e.done)
	}()

	return nil
}

func (e *EnvoyProcess) Kill() error {
	e.Lock()
	defer e.Unlock()

	if e.proc != nil && e.proc.Process != nil {
		klog.V(3).Infof("Stopping envoy...")
		e.proc.Process.Kill()
		e.proc = nil
	}

	if e.config != nil {
		n := e.config.Name()
		klog.V(3).Infof("Closing config file...")
		e.config.Close()
		klog.V(3).Infof("Removing config file %q...", n)
		_ = os.Remove(n)
		e.config = nil
	}

	return nil
}

func (e *EnvoyProcess) Done() <-chan struct{} {
	e.Lock()
	defer e.Unlock()

	return e.done
}

/////////////////////////////////////////////////////////////////////

// envoyLoglineToKlog is a helper function to log envoy output to klog.
var envoyLoglineToKlog = utils.WriteFunc(func(p []byte) (n int, err error) {
	const envoyLogsToken = "] " // the last token (a "]") that marks the beginning of the text

	for _, line := range bytes.Split(p, []byte("\n")) {
		// remove the first "[time] [file] ..."
		idx := strings.LastIndex(string(line), envoyLogsToken)
		if idx > 0 {
			line = line[idx+len(envoyLogsToken):]
		}

		if strings.TrimSpace(string(line)) == "" {
			continue
		}

		klog.Infof("[envoy] %s", line)
	}
	return len(p), nil
})
