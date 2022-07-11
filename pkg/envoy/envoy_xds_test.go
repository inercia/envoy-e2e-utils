package envoy

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/phayes/freeport"
	"k8s.io/klog/v2"
)

func TestEnvoyXDSProc_Run(t *testing.T) {
	// the upstream service
	const upstreamHost = "ifconfig.me"
	const upstreamPort = 80

	// upstream path
	const upstreamPath = "/all"

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(60*time.Second))

	// enable debug logs for klog
	klog.InitFlags(flag.CommandLine)
	defer klog.Flush()
	flag.Set("v", "5")

	bv := BootstrapConfig{
		NodeID:    "envoy",
		ClusterID: "envoy",
	}

	snapshots := make(chan *cache.Snapshot)

	envoy := NewEnvoyXDSProcess(snapshots, bv)
	err := envoy.Start(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	localListenerPort, err := freeport.GetFreePort()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// generate and send a snapshot to envoy
	klog.Infof("Generating snapshot for :%d -> %s:%d", localListenerPort, upstreamHost, upstreamPort)
	snapshots <- GenerateForwarderSnapshot(1, localListenerPort, upstreamHost, upstreamPort)

	success := false

	// keep sending HTTP requests
	go func() {
		for {
			select {
			case <-ctx.Done():
				klog.Infof("Quitting HTTP requests generator")
				return
			case <-time.After(1 * time.Second):
				addr := fmt.Sprintf("http://127.0.0.1:%d%s", localListenerPort, upstreamPath)
				klog.Infof("Sending a HTTP request to \"http://%s:%d%s\" through the Envoy at %q",
					upstreamHost, upstreamPort, upstreamPath, addr)
				if response, _ := http.Get(addr); response != nil {
					if response.StatusCode != http.StatusOK {
						klog.Errorf("Response Status code was %d: retrying", response.StatusCode)
					}
					b, err := io.ReadAll(response.Body)
					if err != nil {
						klog.Errorf("When reading response: %w: retrying...", err)
						return
					}
					klog.Infof("... good! received a response:")
					for _, line := range strings.Split(string(b), "\n") {
						klog.Infof("     - %s", line)
					}
					klog.Infof("... test finished SUCCESSFULLY: cancelling context...")
					success = true
					cancel()
				} else {
					klog.Errorf("... no response received")
				}
			}
		}
	}()

	// let the test run for a while
	select {
	case <-envoy.Done():
		klog.Infof("Envoy is done...")
		klog.Infof("Envoy exit code: %s", envoy.ExitError)
		cancel()

	case <-time.After(20 * time.Second):
		klog.Infof("Timeout: canceling context...")
		cancel()

	case <-ctx.Done():
		klog.Infof("We are done.")
	}

	if !success {
		t.Errorf("Test failed")
	}
}
