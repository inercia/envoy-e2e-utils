package envoy

import (
	"context"
	"fmt"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/test/v3"
	tnet "github.com/inercia/kubetnl/pkg/net"
	prt "github.com/inercia/kubetnl/pkg/port"
	"github.com/inercia/kubetnl/pkg/tunnel"
	"github.com/phayes/freeport"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	eutils "github.com/inercia/envoy-e2e-utils/pkg/utils"
)

const (
	DefaultADSPort = 18000
)

// ExposedXDSServerConfig is the configuration for the exposed service
type ExposedXDSServerConfig struct {
	// Name is the name for the pod/configmap/serviceaccount/etc
	Name string

	// Namespace is the namespace for the pod/configmap/serviceaccount/etc
	Namespace string

	// NodeID is the node ID
	NodeID string

	// the port where the XDS server is listening on the Pod.
	// Will set a default value when not provided.
	ADSPort int

	// Debug must be set to true for enabling debug
	Debug bool

	// RESTConfig is the config used for accessing the Kubernetes cluster.
	RESTConfig *rest.Config
}

// ExposedXDSServer is a XDS server that is exposed to the world.
type ExposedXDSServer struct {
	ExposedXDSServerConfig

	localADSPort int

	cache           cache.SnapshotCache
	snapshots       chan *cache.Snapshot
	tun             *tunnel.Tunnel
	xdsServer       server.Server
	kubeToHereReady chan struct{}
}

// NewExposedXDSServer creates a new exposed XDS server.
func NewExposedXDSServer(snapshots chan *cache.Snapshot, config ExposedXDSServerConfig) (*ExposedXDSServer, error) {
	var err error

	// Start by checking we have everything we need
	if config.NodeID == "" {
		klog.V(3).Infof("Exposed XDS server: setting default node ID: %s", EnvoyNodeID)
		config.NodeID = EnvoyNodeID
	}
	if config.Name == "" {
		return nil, fmt.Errorf("the name must be set")
	}
	if config.Namespace == "" {
		return nil, fmt.Errorf("the namespace must be set")
	}
	if config.RESTConfig == nil {
		return nil, fmt.Errorf("cannot start with not REST config")
	}
	if config.ADSPort == 0 {
		klog.V(3).Infof("Exposed XDS server: setting default ADS port: %d", DefaultADSPort)
		config.ADSPort = DefaultADSPort
	}

	localADSPort, err := freeport.GetFreePort()
	if err != nil {
		return nil, fmt.Errorf("unexpected error: %w", err)
	}

	res := &ExposedXDSServer{
		ExposedXDSServerConfig: config,
		localADSPort:           localADSPort,
		snapshots:              snapshots,
		cache:                  cache.NewSnapshotCache(false, cache.IDHash{}, eutils.KLogger{}),
	}

	return res, nil
}

// Start creates a tunnel from a servixe in a Kubernetes cluster to a
// locally started ADS service.
//
// All the traffic that is sent to the exposed service at the given port will be
// redirected and processed by the handler function.
func (e *ExposedXDSServer) Start(ctx context.Context) (chan struct{}, error) {
	// First, start a XDS server
	signal := make(chan struct{})

	klog.V(3).Infof("Creating a local-xDS server at :%d...", e.localADSPort)
	e.xdsServer = server.NewServer(ctx, e.cache, &test.Callbacks{
		Signal:   signal,
		Fetches:  0,
		Requests: 0,
		Debug:    e.Debug,
	})

	// run the xDS server in a goroutine
	go func() {
		klog.V(3).Infof("Running local-xDS server at :%d...", e.localADSPort)
		RunXDSServer(ctx, e.xdsServer, uint(e.localADSPort))
	}()

	// consume the snapshots channel, setting snapshots in the cache
	go func() {
		for {
			klog.V(3).Infof("Snapshots consumer: consuming snapshots that will be passed to Envoy...")
			select {
			case <-ctx.Done():
				klog.V(3).Infof("Snapshots consumer: context cancelled...")
				return

			case snapshot := <-e.snapshots:
				klog.V(3).Infof("Snapshots consumer: new snapshot received: checking consistency...")
				if err := snapshot.Consistent(); err != nil {
					klog.Errorf("snapshot inconsistency: %+v\n%+v", snapshot, err)
					time.Sleep(1 * time.Second)
					continue
				}

				klog.V(3).Infof("Snapshots consumer: snapshot is consistent: setting for node=%q in cache.", e.NodeID)
				if err := e.cache.SetSnapshot(ctx, e.NodeID, snapshot); err != nil {
					klog.Errorf("snapshot error %q for %+v", err, snapshot)
					time.Sleep(1 * time.Second)
					continue
				}
			}
		}
	}()

	cs, err := kubernetes.NewForConfig(e.RESTConfig)
	if err != nil {
		return nil, err
	}

	kubeToHereConfig := tunnel.TunnelConfig{
		Name:             e.Name,
		IOStreams:        eutils.KLogIOStreams(),
		Image:            tunnel.DefaultTunnelImage,
		Namespace:        e.Namespace,
		EnforceNamespace: true,
		PortMappings: []prt.Mapping{
			{
				TargetIP:            "127.0.0.1",
				TargetPortNumber:    e.localADSPort,
				ContainerPortNumber: e.ADSPort,
			},
		},
		ContinueOnTunnelError: true,
		RESTConfig:            e.RESTConfig,
		ClientSet:             cs,
	}

	// Configure the local and remote SSH control connection
	kubeToHereConfig.LocalSSHPort, err = freeport.GetFreePort()
	if err != nil {
		return nil, err
	}

	kubeToHereConfig.RemoteSSHPort, err = tnet.GetFreeSSHPortInContainer(kubeToHereConfig.PortMappings)
	if err != nil {
		return nil, err
	}

	klog.Infof("Creating a tunnel kubernetes[%s:%d] -> local-xDS:%d",
		e.Name, e.ADSPort, e.localADSPort)
	e.tun = tunnel.NewTunnel(kubeToHereConfig)

	klog.Infof("Starting tunnel kubernetes[%s:%d] -> local-xDS:%d",
		e.Name, e.ADSPort, e.localADSPort)
	e.kubeToHereReady, err = e.tun.Run(ctx)
	if err != nil {
		return nil, err
	}

	return e.kubeToHereReady, nil
}

func (e *ExposedXDSServer) Ready() <-chan struct{} {
	return e.kubeToHereReady
}

func (e *ExposedXDSServer) Stop() error {
	if e.tun != nil {
		klog.Infof("Stopping tunnel kubernetes[%s:%d] -> local-xDS:%d...",
			e.Name, e.ADSPort, e.localADSPort)
		_ = e.tun.Stop(context.Background())
	}

	// NOTE: the xDS server will be stopped when the context is cancelled

	return nil
}
