package envoy

import (
	"context"
	"flag"
	"testing"
	"time"

	"k8s.io/klog/v2"
)

func TestEnvoyProc_Run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// enable debug logs for klog
	klog.InitFlags(flag.CommandLine)
	defer klog.Flush()
	flag.Set("v", "5")

	bv := BootstrapConfig{
		NodeID: "test",
	}

	envoy := NewEnvoyProcess(bv)
	err := envoy.Start(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// let the test run for a while
	select {
	case <-envoy.Done():
		klog.Infof("Envoy is done...")
		klog.Infof("Envoy exit code: %s", envoy.ExitError)
		cancel()

	case <-time.After(10 * time.Second):
		klog.Infof("Timeout: canceling context...")
		cancel()

	case <-ctx.Done():
		klog.Infof("We are done.")
	}
}
