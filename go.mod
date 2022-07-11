module github.com/inercia/envoy-e2e-utils

go 1.16

require (
	github.com/envoyproxy/go-control-plane v0.10.3
	github.com/inercia/kubernetes-e2e-utils v0.0.0-20220707165028-d70af38e4226
	github.com/inercia/kubetnl v0.0.0-20220711145606-40f8ab2b8ad1
	sigs.k8s.io/e2e-framework v0.0.7
)

require (
	github.com/phayes/freeport v0.0.0-20220201140144-74d24b5ae9f5
	google.golang.org/grpc v1.47.0
	google.golang.org/protobuf v1.28.0
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.23.0
	k8s.io/apimachinery v0.23.0
	k8s.io/cli-runtime v0.23.0
	k8s.io/client-go v0.23.0
	k8s.io/klog/v2 v2.70.1
)
