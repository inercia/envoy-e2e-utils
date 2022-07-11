package test

import (
	"context"
	"flag"
	"os"
	"testing"

	e2eutils "github.com/inercia/kubernetes-e2e-utils/pkg/envfuncs"
	"k8s.io/klog/v2"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
)

var (
	testenv env.Environment
)

var (
	imageTags = []string{"inercia/envoy:latest"}
)

func TestMain(m *testing.M) {
	klog.InitFlags(flag.CommandLine) // initializing the flags
	defer klog.Flush()               // flushes all pending log I/O

	flag.Set("v", "5")

	testenv = env.New()

	// NOTE: we should use some unique cluster name in Jenkins...
	clusterName := "kubernetes-e2e-utils-tests"
	if cn := os.Getenv("KE2E_CLUSTER_NAME"); cn != "" {
		clusterName = cn
	}

	namespace := envconf.RandomName("e2e", 16)

	// use Kind/k3d, depending on the environment
	setupClusterFunc := e2eutils.CreateK3dCluster(clusterName)
	deleteClusterFunc := e2eutils.DestroyK3dCluster(clusterName)

	// override the cluster provider with the KE2E_CLUSTER_PROVIDER env. variable
	if os.Getenv("KE2E_CLUSTER_PROVIDER") == "kind" {
		setupClusterFunc = envfuncs.CreateKindCluster(clusterName)
		deleteClusterFunc = envfuncs.DestroyKindCluster(clusterName)
	}

	// destroy the cluster only if KE2E_CLUSTER_DESTROY is defined in the env.
	if _, ok := os.LookupEnv("KE2E_CLUSTER_DESTROY"); !ok {
		deleteClusterFunc = env.Func(func(c context.Context, _ *envconf.Config) (context.Context, error) {
			klog.Info("Keeping the cluster alive, as KE2E_CLUSTER_DESTROY is not in env. variables.")
			return c, nil
		})
	}

	// Use pre-defined environment funcs to create a kind cluster prior to test run
	testenv.Setup(
		setupClusterFunc,
		envfuncs.CreateNamespace(namespace),
	)

	// Use pre-defined environment funcs to teardown kind cluster after tests
	testenv.Finish(
		envfuncs.DeleteNamespace(namespace),
		deleteClusterFunc,
	)

	// launch package tests
	os.Exit(testenv.Run(m))
}
