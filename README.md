# Envoy e2e test utils

Some utilities for testing Envoy in the context of the [Kubernetes e2e framework](sigs.k8s.io/e2e-framework).

Features:

* functions for running Envoy in a Pod while running the xDS server locally. See [this example](tests/exposed_envoy_test.go) for more details.
