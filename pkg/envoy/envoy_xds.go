package envoy

import (
	"context"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/test/v3"
	"github.com/phayes/freeport"
	"k8s.io/klog/v2"

	eutils "github.com/inercia/envoy-e2e-utils/pkg/utils"
)

// EnvoyXDSProcess is a local envoy process that reads xDS
// configuration from a channel.
type EnvoyXDSProcess struct {
	*EnvoyProcess

	// Debug must be set to true for enabling debug
	Debug bool

	xdsServer server.Server
	cache     cache.SnapshotCache
	snapshots chan *cache.Snapshot
}

// NewEnvoyXDSProcess creates a new, local envoy process that reads xDS
// configuration from a channel.
func NewEnvoyXDSProcess(snapshots chan *cache.Snapshot, bv BootstrapConfig) *EnvoyXDSProcess {
	if bv.ADSPort == 0 {
		p, err := freeport.GetFreePort()
		if err != nil {
			return nil
		}
		bv.ADSPort = p
	}

	return &EnvoyXDSProcess{
		snapshots:    snapshots,
		EnvoyProcess: NewEnvoyProcess(bv),
		cache:        cache.NewSnapshotCache(false, cache.IDHash{}, eutils.KLogger{}),
	}
}

// Start starts the Envoy process and a xDS server, connected to the channel.
func (e *EnvoyXDSProcess) Start(ctx context.Context) error {
	signal := make(chan struct{})

	klog.V(3).Infof("Running xDS server at :%d...", e.ADSPort)
	e.xdsServer = server.NewServer(ctx, e.cache, &test.Callbacks{
		Signal:   signal,
		Fetches:  0,
		Requests: 0,
		Debug:    e.Debug,
	})

	// run the xDS server in a goroutine
	go func() {
		RunXDSServer(ctx, e.xdsServer, uint(e.ADSPort))
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

	return e.EnvoyProcess.Start(ctx)
}
