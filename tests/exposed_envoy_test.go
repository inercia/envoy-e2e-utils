package test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/inercia/kubetnl/pkg/portforward"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/inercia/envoy-e2e-utils/pkg/envoy"
)

// Test that exposes an Envoy service on the kubernetes cluster, while keeping the xDS server running locally.
//
// * This test creates a local xDS server and "exposes" this service in a kubernetes cluster with the help
//   of a tunnel that forwards the traffic from the kubernetes cluster to the local xDS server
//
// * And Envoy Pod is created and pointed to this xDS forwarder for getting the configuration.
//
// * On the other hand, a port-forward is created for sending HTTP requests to the Envoy Pod.
//
// * As part of the e2e test, we create a snapshot for the xDS server that sends everything to "ifconfig.me",
//   and we send HTTP requests to the Envoy pod (through the port-forward) that ideally should return the
//   ifconfig.me output...
//
//
//      ┌─────────────────────────────────────────────────┐
//      │                              kubernetes cluster │
//      │        ┌──────────┐       ┌───────────────┐     │
// ┌────┼─►:80   │          │       │               │     │
// │    │  :19000│  envoy   ├───────► xDS forwarder ├─┐   │
// │    │        │          │       │               │ │   │
// │    │        └──────────┘       └───────────────┘ │   │
// │    └─────────────────────────────────────────────┼───┘
// │                                            ┌─────▼─────┐
// │                                            │ local xDS │
// │                                            │ server    │
// │                                            └─────▲─────┘
// │   ───────────────────────────────────────────────│─────────────────
// │    ┌───────────────┐             ┌───────────────┴────┐     e2e
// │    │ HTTP          │             │ snapshosts creator │     test
// └────┤ Requests      │             └────────────────────┘
//      │ Generator     │
//      └───────────────┘
//
func TestExposedEnvoyInCluster(t *testing.T) {
	exposeEnvoyService := features.New("expose envoy service").
		Assess("expose envoy service", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var err error

			origContext := ctx
			ctx, cancelContext := context.WithCancel(ctx)

			snapshots := make(chan *cache.Snapshot)

			rest := cfg.Client().RESTConfig()
			cs, err := kubernetes.NewForConfig(rest)
			if err != nil {
				t.Fatal(err)
			}

			// the upstream service
			const upstreamHost = "ifconfig.me"
			const upstreamPort = 80

			// upstream path
			const upstreamPath = "/all"

			config := envoy.EnvoyPodExposedXDSServerConfig{
				envoy.EnvoyPodConfig{
					Name:         "envoy",
					Namespace:    cfg.Namespace(),
					ListenerPort: 80,
					Debug:        true,
					RESTConfig:   rest,
				},
				envoy.ExposedXDSServerConfig{
					Name:       "envoy-xds-forwarder",
					Namespace:  cfg.Namespace(),
					Debug:      true,
					RESTConfig: rest,
				},
			}

			exposedEnvoy, err := envoy.NewEnvoyPodExposedXDSServer(snapshots, config)
			if err != nil {
				t.Fatal(err)
			}

			klog.Info("Starting exposed Envoy with local xDS...")
			exposedEnvoyReady, err := exposedEnvoy.Start(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// generate and send a snapshot to envoy
			klog.Infof("Generating snapshot for sending traffic from envoy:%d -> %s:%d",
				config.EnvoyPodConfig.ListenerPort, upstreamHost, upstreamPort)
			snapshots <- envoy.GenerateForwarderSnapshot(1, config.EnvoyPodConfig.ListenerPort, upstreamHost, upstreamPort)

			// create a port-forward for the envoy pod
			// so we can send HTTP request through this port-forward.
			hereToEnvoy, err := portforward.NewKubeForwarder(portforward.KubeForwarderConfig{
				PodName:      config.EnvoyPodConfig.Name,
				PodNamespace: config.EnvoyPodConfig.Namespace,
				RemotePort:   config.EnvoyPodConfig.ListenerPort,
				RESTConfig:   rest,
				ClientSet:    cs,
			})
			if err != nil {
				t.Fatal(err)
			}

			klog.Infof("Starting the here:%d -> Envoy [%s:%d] port-forward...",
				hereToEnvoy.LocalPort,
				hereToEnvoy.PodName,
				hereToEnvoy.RemotePort)
			if _, err = hereToEnvoy.Run(ctx); err != nil {
				t.Fatal(err)
			}
			defer hereToEnvoy.Stop()

			klog.Infof("Waiting until everything is ready for starting HTTP requests...")
			select {
			case <-exposedEnvoyReady:
			case <-ctx.Done():
				klog.Info("Context cancelled: aborting")
				cancelContext()
				return origContext
			}

			select {
			case <-hereToEnvoy.Ready():
			case <-ctx.Done():
				klog.Info("Context cancelled: aborting")
				cancelContext()
				return origContext
			}

			success := false

			klog.Infof("Everything ready: sending HTTP queries to Envoy (through the port-forward to the Envoy pod)")
		loop:
			for {
				select {
				case <-ctx.Done():
					klog.Infof("Quitting HTTP requests generator")
					break loop

				case <-time.After(1 * time.Second):
					addr := fmt.Sprintf("http://127.0.0.1:%d%s", hereToEnvoy.LocalPort, upstreamPath)
					klog.Infof("Checking that we can send a HTTP request to %q", addr)
					response, _ := http.Get(addr)
					if response != nil {
						if response.StatusCode != http.StatusOK {
							klog.Infof("Response Status code was %d", response.StatusCode)
						}
						b, err := io.ReadAll(response.Body)
						if err != nil {
							klog.Infof("Error when reading response: %w", err)
						}
						klog.Infof("Good! Received a response to out request:")
						for _, line := range strings.Split(string(b), "\n") {
							klog.Infof("     - %s", line)
						}
						klog.Infof("... test finished SUCCESSFULLY: cancelling context...")
						success = true
						cancelContext()
						break loop
					} else {
						klog.Infof("No response received")
					}
				}
			}

			<-ctx.Done()
			if !success {
				t.Errorf("Test failed")
			}

			return origContext
		}).Feature()

	// test feature
	testenv.Test(t, exposeEnvoyService)
}
