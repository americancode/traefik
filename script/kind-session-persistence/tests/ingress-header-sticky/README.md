# ingress-header-sticky

Purpose: Kubernetes Ingress service annotations for header-based stickiness.

Host:
- whoami-ingress-sticky.localhost

## Setup (one-time per session)

```bash
# Ensure KIND cluster exists and kubeconfig is set.
kind get kubeconfig --name traefik-sticky > /tmp/kind-kubeconfig
export KUBECONFIG=/tmp/kind-kubeconfig

# Build the Traefik binary so the image includes your local changes.
make -C /Users/nathaniel/source/traefik-src/traefik binary-linux-amd64

# Build/load the image into KIND.
podman build -t localhost/traefik:dev /Users/nathaniel/source/traefik-src/traefik
podman save -o /tmp/traefik-dev.tar localhost/traefik:dev
kind load image-archive /tmp/traefik-dev.tar --name traefik-sticky
```


## Test script

```bash
set -euo pipefail

TEST_DIR="/Users/nathaniel/source/traefik-src/traefik/script/kind-session-persistence/tests/ingress-header-sticky"

pkill -f "kubectl -n traefik port-forward svc/traefik" >/dev/null 2>&1 || true
helm uninstall traefik -n traefik >/dev/null 2>&1 || true
kubectl delete namespace traefik --ignore-not-found >/dev/null 2>&1 || true
kubectl delete --ignore-not-found -f "/Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik-crds/crds-files/gatewayAPI/gateway-experimental-install.yaml" >/dev/null 2>&1 || true
kubectl delete --ignore-not-found -f "/Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik-crds/crds-files/traefik" >/dev/null 2>&1 || true
find /Users/nathaniel/source/traefik-src/traefik/script/kind-session-persistence/tests -maxdepth 2 -name '*.yaml' ! -name 'traefik-values.yaml' -print0 | \
  xargs -0 -n1 kubectl delete --ignore-not-found -f >/dev/null 2>&1 || true

kubectl apply --validate=false -f "/Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik-crds/crds-files/traefik"
kubectl apply --server-side --force-conflicts --validate=false -f "/Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik-crds/crds-files/gatewayAPI/gateway-experimental-install.yaml"

helm upgrade --install traefik /Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik \
  --namespace traefik --create-namespace \
  -f "$TEST_DIR/traefik-values.yaml" --skip-crds

kubectl -n traefik rollout status deployment/traefik --timeout=180s

find "$TEST_DIR" -maxdepth 1 -name '*.yaml' ! -name 'traefik-values.yaml' -print0 | \
  xargs -0 -n1 kubectl apply --validate=false -f

kubectl get deploy -n default -o name | \
  xargs -r -n1 kubectl -n default rollout status --timeout=180s

kubectl -n traefik port-forward svc/traefik 8001:80 >/tmp/port-forward.log 2>&1 &
PF=$!
sleep 2

STICKY=$(curl -s -D - -H 'Host: whoami-ingress-sticky.localhost' http://localhost:8001/ | rg -i '^X-Ingress-Sticky:' | awk '{print $2}' | tr -d '\r')
curl -s -H 'Host: whoami-ingress-sticky.localhost' -H "X-Ingress-Sticky: ${STICKY}" http://localhost:8001/ | rg -n '^Hostname:'

kill $PF >/dev/null 2>&1 || true
```

## Known issues and fixes

- If responses are missing headers right after apply, wait a few seconds and retry; Traefik may still be reconciling.
- If port-forward fails, kill the old process: `pkill -f "kubectl -n traefik port-forward svc/traefik"`.
- If a test fails after code changes, rebuild the binary and image (`make -C traefik binary-linux-amd64`, then `podman build`/`kind load`).
- XBackendTrafficPolicy resources require the experimental Gateway API CRDs (standard CRDs will reject the resource).
- For failover tests, the first request after pod deletion can return 503 before the new pod is ready; retry until a 200 appears.


## Cleanup

```bash
pkill -f "kubectl -n traefik port-forward svc/traefik" >/dev/null 2>&1 || true
helm uninstall traefik -n traefik >/dev/null 2>&1 || true
kubectl delete namespace traefik --ignore-not-found >/dev/null 2>&1 || true
kubectl delete --ignore-not-found -f "/Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik-crds/crds-files/gatewayAPI/gateway-experimental-install.yaml" >/dev/null 2>&1 || true
kubectl delete --ignore-not-found -f "/Users/nathaniel/source/traefik-src/traefik-helm-chart/traefik-crds/crds-files/traefik" >/dev/null 2>&1 || true
find /Users/nathaniel/source/traefik-src/traefik/script/kind-session-persistence/tests -maxdepth 2 -name '*.yaml' ! -name 'traefik-values.yaml' -print0 |   xargs -0 -n1 kubectl delete --ignore-not-found -f >/dev/null 2>&1 || true
```
