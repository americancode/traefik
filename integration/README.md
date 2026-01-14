# Integration tests

## Kubernetes (k3s default)

The default Kubernetes integration tests spin up a k3s server using testcontainers.
This requires a running Docker daemon.

## Kubernetes (KIND mode)

You can run the Kubernetes integration tests against an existing local KIND cluster
instead of k3s by setting `K8S_USE_KIND=1` and providing a kubeconfig.

Example:

```bash
export K8S_USE_KIND=1
kind get kubeconfig --name traefik-sticky > /tmp/kind-kubeconfig
export KUBECONFIG=/tmp/kind-kubeconfig

go test ./integration -run TestK8sSuite -testify.m SessionPersistence
```

Notes:
- The suite applies the base fixtures in `integration/fixtures/k8s` and the session
  persistence fixtures in `integration/fixtures/k8s-session-persistence`.
- Cleanup deletes those same fixtures from the cluster at teardown.
 - When `-testify.m` filters to `SessionPersistence`, only the CRDs and Gateway
   fixtures are applied from `integration/fixtures/k8s` to avoid name collisions
   with the session fixtures.
- In KIND mode, the session tests install Traefik with Helm using the per-test
  values files in `script/kind-session-persistence/tests/<test>/traefik-values.yaml`
  and port-forward `svc/traefik` to `localhost:8180`. Load your local image into
  KIND before running the tests (e.g. `podman build` + `kind load image-archive`).
