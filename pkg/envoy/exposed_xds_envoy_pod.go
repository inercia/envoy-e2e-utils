package envoy

import (
	"context"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"k8s.io/klog/v2"
)

// ExposedXDSServerConfig is the configuration for the exposed service
type EnvoyPodExposedXDSServerConfig struct {
	EnvoyPodConfig
	ExposedXDSServerConfig
}

// ExposedXDSServer is a XDS server that is exposed to the world.
type EnvoyPodExposedXDSServer struct {
	EnvoyPodExposedXDSServerConfig

	ready chan struct{}

	envoyPod  *EnvoyPod
	xdsServer *ExposedXDSServer
}

// NewExposedXDSServer creates:
// * a exposed XDS server, that is exposed in the kubernetes cluster but sending
//   xDS requests to a local xDS server.
// * an Envoy pod, that gets the xDS configuration from the exposed XDS server.
func NewEnvoyPodExposedXDSServer(snapshots chan *cache.Snapshot, config EnvoyPodExposedXDSServerConfig) (*EnvoyPodExposedXDSServer, error) {
	var err error

	xdsServer, err := NewExposedXDSServer(snapshots, config.ExposedXDSServerConfig)
	if err != nil {
		return nil, err
	}

	// instruct the Pod to get the xDS configuration from the exposed XDS server
	// and match any other configuration that is provided by the user.
	config.EnvoyPodConfig.ADSPort = xdsServer.ADSPort
	config.EnvoyPodConfig.ADSAddress = xdsServer.Name
	config.EnvoyPodConfig.NodeID = xdsServer.NodeID
	config.EnvoyPodConfig.Namespace = xdsServer.Namespace

	envoyPod, err := NewEnvoyPod(config.EnvoyPodConfig)
	if err != nil {
		return nil, err
	}

	res := &EnvoyPodExposedXDSServer{
		envoyPod:                       envoyPod,
		xdsServer:                      xdsServer,
		EnvoyPodExposedXDSServerConfig: config,
	}

	return res, nil
}

// Start creates a tunnel from a servixe in a Kubernetes cluster to a
// locally started ADS service.
//
// All the traffic that is sent to the exposed service at the given port will be
// redirected and processed by the handler function.
func (e *EnvoyPodExposedXDSServer) Start(ctx context.Context) (chan struct{}, error) {
	klog.V(3).Infof("Starting a local xDS server exposed in the cluster...")
	ready, err := e.xdsServer.Start(ctx)
	if err != nil {
		return nil, err
	}

	klog.V(3).Infof("Starting the Envoy Pod...")
	if err := e.envoyPod.Start(ctx); err != nil {
		return nil, err
	}

	return ready, nil
}

func (e *EnvoyPodExposedXDSServer) Ready() <-chan struct{} {
	return e.ready
}

func (e *EnvoyPodExposedXDSServer) Stop(ctx context.Context) error {
	if err := e.envoyPod.Stop(ctx); err != nil {
		return err
	}
	if err := e.xdsServer.Stop(); err != nil {
		return err
	}
	return nil
}
