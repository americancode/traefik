# KIND session persistence test

This setup creates a local KIND cluster, installs Traefik via the local Helm chart, and runs stickiness tests for Gateway API and Traefik CRDs. Each test lives in its own directory with all manifests needed to run it.

## Prerequisites

- `kind`, `kubectl`, `helm` available in PATH.
- `podman` (or `docker`) for building/loading the Traefik image.

Notes:
- If you are running commands via Codex, Podman/KIND access may be blocked by sandboxing; rerun with escalated permissions as needed.
- For multi-terminal workflows, export `KUBECONFIG` in your shell profile so any terminal can reach the cluster created below.

## Podman + KIND setup (macOS)

These steps are required when using the Podman provider for KIND.

1. Create and start the Podman machine (one-time init).

```bash
podman machine init
podman machine start
```

2. Point KIND to the Podman provider.

```bash
export KIND_EXPERIMENTAL_PROVIDER=podman
```

3. Verify Podman is reachable.

```bash
podman info
```

## Cluster + Traefik install

1. Create a KIND cluster.

```bash
kind create cluster --name traefik-sticky
```

2. Export the kubeconfig so you do not need `--kubeconfig` on every command.

```bash
kind get kubeconfig --name traefik-sticky > /tmp/kind-kubeconfig
export KUBECONFIG=/tmp/kind-kubeconfig
```

If you want this to persist across terminals, add to your shell profile:

```bash
export KUBECONFIG=/tmp/kind-kubeconfig
```

3. If the node is `NotReady` due to CNI not initialized (Podman + KIND issue), install kindnet.

```bash
POD_CIDR=$(kubectl get node traefik-sticky-control-plane -o jsonpath='{.spec.podCIDR}')
podman exec traefik-sticky-control-plane \
  sed "s/{{ .PodSubnet }}/${POD_CIDR//\//\\/}/" /kind/manifests/default-cni.yaml | \
  kubectl apply -f -
kubectl wait --for=condition=Ready node/traefik-sticky-control-plane --timeout=180s
```

4. On single-node clusters, remove the control-plane taint so Traefik can schedule.

```bash
kubectl taint nodes traefik-sticky-control-plane node-role.kubernetes.io/control-plane-
```

5. Build and load a local Traefik image from this repo.

If you have local code changes, rebuild the binary first so the image uses them:

```bash
make binary-linux-amd64
```

```bash
podman build -t localhost/traefik:dev .
podman save -o /tmp/traefik-dev.tar localhost/traefik:dev
kind load image-archive /tmp/traefik-dev.tar --name traefik-sticky
```

6. Install CRDs (Traefik + Gateway API).

Option A: install Traefik CRDs via chart, then apply Gateway API experimental CRDs.

```bash
helm install traefik-crds ../traefik-helm-chart/traefik-crds --namespace traefik --create-namespace
kubectl apply --server-side --force-conflicts --validate=false -f integration/fixtures/k8s/00-experimental-v1.4.0.yml
```

Option B: install Gateway API experimental CRDs via the chart (required for XBackendTrafficPolicy tests).

```bash
helm install traefik-crds ../traefik-helm-chart/traefik-crds \
  --namespace traefik --create-namespace \
  -f script/kind-session-persistence/values/traefik-crds-gateway-experimental.yaml
```

Option C: install Gateway API standard CRDs only (XBackendTrafficPolicy should fail to apply).

```bash
helm install traefik-crds ../traefik-helm-chart/traefik-crds \
  --namespace traefik --create-namespace \
  -f script/kind-session-persistence/values/traefik-crds-gateway-standard.yaml
```

If you run any XBackendTrafficPolicy test after Option C, `kubectl apply` should fail with
`no matches for kind "XBackendTrafficPolicy" in version "gateway.networking.x-k8s.io/v1alpha1"`.

Note: header stickiness for IngressRoute/TraefikService requires CRDs that include `sticky.header`.
If header fields are pruned, upgrade the `traefik-crds` chart to a version that contains
`sticky.header` in the CRD schema.

7. Install Traefik via Helm using the values file from the test you want to run.

```bash
helm install traefik ../traefik-helm-chart/traefik \
  --namespace traefik \
  -f script/kind-session-persistence/tests/<test>/traefik-values.yaml \
  --skip-crds
```

If Traefik is already installed and you need to switch values:

```bash
helm upgrade traefik ../traefik-helm-chart/traefik \
  --namespace traefik \
  -f script/kind-session-persistence/tests/<test>/traefik-values.yaml \
  --skip-crds
```

8. Port-forward Traefik web entrypoint.

```bash
kubectl -n traefik port-forward svc/traefik 8000:80
```

## Fixtures directory intent

The `integration/fixtures/k8s` directory contains CRDs and base Kubernetes resources used by Traefik integration tests. For these KIND tests we only reuse the Gateway API experimental CRD manifest from that directory; the rest of the test resources live under `script/kind-session-persistence/tests`.

## Tests

Apply a test by pointing `kubectl` at the directory:

```bash
kubectl apply --validate=false -f script/kind-session-persistence/tests/<test>/
```

## Full suite script (per-test cleanup)

This script rebuilds the local Traefik image, then runs every test directory independently. Each test performs a full cleanup of Traefik, CRDs, and applied resources before and after running.

```bash
#!/usr/bin/env bash
set -u

ROOT="/Users/nathaniel/source/traefik-src"
TEST_ROOT="$ROOT/traefik/script/kind-session-persistence/tests"
CRDS_TRAEFIK="$ROOT/traefik-helm-chart/traefik-crds/crds-files/traefik"
CRDS_GATEWAY_EXP="$ROOT/traefik-helm-chart/traefik-crds/crds-files/gatewayAPI/gateway-experimental-install.yaml"
KUBECONFIG_PATH="/tmp/kind-kubeconfig"
PORT=8001

export KUBECONFIG="$KUBECONFIG_PATH"

log() {
  printf '\n[%s] %s\n' "$(date +%H:%M:%S)" "$*"
}

cleanup_all() {
  pkill -f "kubectl -n traefik port-forward svc/traefik" >/dev/null 2>&1 || true
  helm uninstall traefik -n traefik >/dev/null 2>&1 || true
  helm uninstall traefik-crds -n traefik >/dev/null 2>&1 || true
  kubectl delete namespace traefik --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete --ignore-not-found -f "$CRDS_GATEWAY_EXP" >/dev/null 2>&1 || true
  kubectl delete --ignore-not-found -f "$CRDS_TRAEFIK" >/dev/null 2>&1 || true
  find "$TEST_ROOT" -maxdepth 2 -name '*.yaml' ! -name 'traefik-values.yaml' -print0 | \
    xargs -0 -n1 kubectl delete --ignore-not-found -f >/dev/null 2>&1 || true
}

install_crds() {
  kubectl apply --validate=false -f "$CRDS_TRAEFIK"
  kubectl apply --server-side --force-conflicts --validate=false -f "$CRDS_GATEWAY_EXP"
}

install_traefik() {
  local test_dir="$1"
  helm upgrade --install traefik "$ROOT/traefik-helm-chart/traefik" \
    --namespace traefik --create-namespace \
    -f "$test_dir/traefik-values.yaml" --skip-crds
  kubectl -n traefik rollout status deployment/traefik --timeout=180s
}

apply_test_manifests() {
  local test_dir="$1"
  find "$test_dir" -maxdepth 1 -name '*.yaml' ! -name 'traefik-values.yaml' -print0 | \
    xargs -0 -n1 kubectl apply --validate=false -f

  kubectl get deploy -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' | \
    rg -v '^(kube-system|local-path-storage|traefik)/' | rg 'whoami' | while IFS= read -r dep; do
      [ -z "$dep" ] && continue
      ns="${dep%%/*}"
      name="${dep##*/}"
      kubectl -n "$ns" rollout status deployment/"$name" --timeout=180s
    done
}

start_port_forward() {
  kubectl -n traefik port-forward svc/traefik "$PORT":80 >/tmp/port-forward.log 2>&1 &
  echo $!
}

retry_cmd() {
  local tries="$1"
  shift
  local i
  for i in $(seq 1 "$tries"); do
    if "$@"; then
      return 0
    fi
    sleep 2
  done
  return 1
}

run_test() {
  local name="$1"
  local test_dir="$TEST_ROOT/$name"
  local pf_pid=""

  log "START $name"

  cleanup_all
  install_crds
  install_traefik "$test_dir"
  apply_test_manifests "$test_dir"

  pf_pid=$(start_port_forward)
  sleep 2

  case "$name" in
    basic-routing-gateway)
      retry_cmd 5 curl -s -H 'Host: whoami-gw-basic.localhost' "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    basic-routing-ingress)
      retry_cmd 5 curl -s -H 'Host: whoami-ingress-basic.localhost' "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    basic-routing-ingressroute)
      retry_cmd 5 curl -s -H 'Host: whoami-ir-basic.localhost' "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-sticky)
      resp=$(curl -s -D - -H 'Host: whoami.localhost' "http://localhost:${PORT}/")
      cookie=$(printf '%s' "$resp" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      curl -s -H 'Host: whoami.localhost' -H "Cookie: ${cookie}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      sticky=$(curl -s -D - -H 'Host: whoami-header.localhost' "http://localhost:${PORT}/" | rg -i '^X-Session-Id:' | awk '{print $2}' | tr -d '\r')
      curl -s -H 'Host: whoami-header.localhost' -H "X-Session-Id: ${sticky}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-sticky-cookie-attrs)
      curl -s -D - -H 'Host: whoami-cookie-attrs.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^Set-Cookie:'
      ;;
    gateway-sticky-failover)
      cookie=$(curl -s -D - -H 'Host: whoami-failover.localhost' "http://localhost:${PORT}/" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      curl -s -H 'Host: whoami-failover.localhost' -H "Cookie: ${cookie}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      kubectl -n default delete pod -l app=whoami-failover
      retry_cmd 10 curl -s -H 'Host: whoami-failover.localhost' -H "Cookie: ${cookie}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-sticky-multiclient)
      cookie_a=$(curl -s -D - -H 'Host: whoami-mc.localhost' "http://localhost:${PORT}/" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      cookie_b=$(curl -s -D - -H 'Host: whoami-mc.localhost' "http://localhost:${PORT}/" | rg -i '^Set-Cookie:' | tail -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      curl -s -H 'Host: whoami-mc.localhost' -H "Cookie: ${cookie_a}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      curl -s -H 'Host: whoami-mc.localhost' -H "Cookie: ${cookie_b}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-sticky-missing)
      for _ in $(seq 1 10); do curl -s -H 'Host: whoami-missing.localhost' "http://localhost:${PORT}/" | rg -n '^Hostname:'; done | sort -u
      ;;
    ingressroute-sticky)
      cookie=$(curl -s -D - -H 'Host: whoami-ir-cookie.localhost' "http://localhost:${PORT}/" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      curl -s -H 'Host: whoami-ir-cookie.localhost' -H "Cookie: ${cookie}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      sticky=$(curl -s -D - -H 'Host: whoami-ir-header.localhost' "http://localhost:${PORT}/" | rg -i '^X-IR-Sticky:' | awk '{print $2}' | tr -d '\r')
      curl -s -H 'Host: whoami-ir-header.localhost' -H "X-IR-Sticky: ${sticky}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-traefikservice-route-sticky)
      curl -s -D - -H 'Host: whoami-tsr-header.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^X-'
      curl -s -D - -H 'Host: whoami-tsr-cookie.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^Set-Cookie:'
      ;;
    gateway-traefikservice-route-wrr)
      curl -s -D - -H 'Host: whoami-tsr-wrr-header.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^X-'
      curl -s -D - -H 'Host: whoami-tsr-wrr-cookie.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^Set-Cookie:'
      ;;
    gateway-traefikservice-multilevel)
      resp=$(curl -s -D - -H 'Host: whoami-ts-cookie.localhost' "http://localhost:${PORT}/" -o /dev/null)
      cookie1=$(printf '%s' "$resp" | rg -i '^Set-Cookie:' | rg 'ts-wrr-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      cookie2=$(printf '%s' "$resp" | rg -i '^Set-Cookie:' | rg 'ts-svc-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
      curl -s -H 'Host: whoami-ts-cookie.localhost' -H "Cookie: ${cookie1}; ${cookie2}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      resp2=$(curl -s -D - -H 'Host: whoami-ts-header.localhost' "http://localhost:${PORT}/" -o /dev/null)
      hwr=$(printf '%s' "$resp2" | rg -i '^X-TS-WRR:' | awk '{print $2}' | tr -d '\r')
      hsvc=$(printf '%s' "$resp2" | rg -i '^X-TS-SVC:' | awk '{print $2}' | tr -d '\r')
      curl -s -H 'Host: whoami-ts-header.localhost' -H "X-TS-WRR: ${hwr}" -H "X-TS-SVC: ${hsvc}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-traefikservice-cookie-attrs)
      curl -s -D - -H 'Host: whoami-ts-attrs.localhost' "http://localhost:${PORT}/sticky" -o /dev/null | rg -i '^Set-Cookie:'
      ;;
    gateway-xbackendpolicy-header)
      policy=$(curl -s -D - -H 'Host: whoami-xbtp.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^X-Policy-Session:' | awk '{print $2}' | tr -d '\r')
      curl -s -H 'Host: whoami-xbtp.localhost' -H "X-Policy-Session: ${policy}" "http://localhost:${PORT}/" | rg -n '^Hostname:'
      ;;
    gateway-xbackendpolicy-precedence)
      curl -s -D - -H 'Host: whoami-xbtp-precedence.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^X-'
      ;;
    gateway-xbackendpolicy-traefikservice)
      curl -s -D - -H 'Host: whoami-xbtp-ts.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^X-'
      ;;
    gateway-xbackendpolicy-disabled)
      curl -s -D - -H 'Host: whoami-xbtp-disabled.localhost' "http://localhost:${PORT}/" -o /dev/null | rg -i '^X-Policy-Session:' || true
      for _ in $(seq 1 10); do curl -s -H 'Host: whoami-xbtp-disabled.localhost' "http://localhost:${PORT}/" | rg -n '^Hostname:'; done | sort -u
      ;;
    *)
      echo "Unknown test: $name" >&2
      return 1
      ;;
  esac

  kill "$pf_pid" >/dev/null 2>&1 || true
  return 0
}

log "Rebuilding Traefik binary"
make -C "$ROOT/traefik" binary-linux-amd64

log "Building and loading Traefik image into KIND"
podman build -t localhost/traefik:dev "$ROOT/traefik"
podman save -o /tmp/traefik-dev.tar localhost/traefik:dev
kind load image-archive /tmp/traefik-dev.tar --name traefik-sticky

log "Running tests"
results=""
for test_name in $(ls "$TEST_ROOT"); do
  if run_test "$test_name"; then
    log "PASS $test_name"
    results="${results}PASS ${test_name}\n"
  else
    log "FAIL $test_name"
    results="${results}FAIL ${test_name}\n"
  fi
  cleanup_all
  sleep 2
  log "Cleaned up after $test_name"
done

log "Results summary"
printf "%b" "$results"
```

### basic-routing-gateway
- Path: `script/kind-session-persistence/tests/basic-routing-gateway`
- Host: `whoami-gw-basic.localhost`

```bash
curl -H 'Host: whoami-gw-basic.localhost' http://localhost:8000/
```

### basic-routing-ingress
- Path: `script/kind-session-persistence/tests/basic-routing-ingress`
- Host: `whoami-ingress-basic.localhost`
- This test requires `providers.kubernetesIngress.enabled: true` (already set in the test values file).

```bash
curl -H 'Host: whoami-ingress-basic.localhost' http://localhost:8000/
```

### basic-routing-ingressroute
- Path: `script/kind-session-persistence/tests/basic-routing-ingressroute`
- Host: `whoami-ir-basic.localhost`

```bash
curl -H 'Host: whoami-ir-basic.localhost' http://localhost:8000/
```

### gateway-sticky
- Path: `script/kind-session-persistence/tests/gateway-sticky`
- Pod-level stickiness using Gateway API `sessionPersistence` to a regular K8s Service.
- Hosts:
  - Cookie: `whoami.localhost`
  - Header: `whoami-header.localhost`

Cookie test:

```bash
COOKIE=$(curl -s -D - -H 'Host: whoami.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
curl -H 'Host: whoami.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'
```

Header test:

```bash
STICKY=$(curl -s -D - -H 'Host: whoami-header.localhost' http://localhost:8000/ | rg -i '^X-Session-Id:' | awk '{print $2}' | tr -d '\r')
curl -H 'Host: whoami-header.localhost' -H "X-Session-Id: ${STICKY}" http://localhost:8000/ | rg -n '^Hostname:'
```

### gateway-sticky-cookie-attrs
- Path: `script/kind-session-persistence/tests/gateway-sticky-cookie-attrs`
- Cookie attributes from HTTPRoute sessionPersistence (Permanent + AbsoluteTimeout).
- Host: `whoami-cookie-attrs.localhost`

```bash
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-cookie-attrs.localhost' http://localhost:8000/ -o /dev/null)
printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:'
```

### gateway-sticky-failover
- Path: `script/kind-session-persistence/tests/gateway-sticky-failover`
- Pod-level stickiness with failover when the sticky pod is deleted.
- Host: `whoami-failover.localhost`

```bash
COOKIE=$(curl -s -D - -H 'Host: whoami-failover.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
curl -H 'Host: whoami-failover.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'
kubectl -n default delete pod -l app=whoami-failover
sleep 5
curl -H 'Host: whoami-failover.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'
```

### gateway-sticky-multiclient
- Path: `script/kind-session-persistence/tests/gateway-sticky-multiclient`
- Two independent clients should stick to different pods.
- Host: `whoami-mc.localhost`

```bash
COOKIE_A=$(curl -s -D - -H 'Host: whoami-mc.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
COOKIE_B=$(curl -s -D - -H 'Host: whoami-mc.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
curl -H 'Host: whoami-mc.localhost' -H "Cookie: ${COOKIE_A}" http://localhost:8000/ | rg -n '^Hostname:'
curl -H 'Host: whoami-mc.localhost' -H "Cookie: ${COOKIE_B}" http://localhost:8000/ | rg -n '^Hostname:'
```

### gateway-xbackendpolicy-header
- Path: `script/kind-session-persistence/tests/gateway-xbackendpolicy-header`
- XBackendTrafficPolicy header stickiness for a Service.
- Host: `whoami-xbtp.localhost`

```bash
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-xbtp.localhost' http://localhost:8000/ -o /dev/null)
POLICY=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-Policy-Session:' | awk '{print $2}' | tr -d '\r')
curl -H 'Host: whoami-xbtp.localhost' -H "X-Policy-Session: ${POLICY}" http://localhost:8000/ | rg -n '^Hostname:'
```

### gateway-xbackendpolicy-precedence
- Path: `script/kind-session-persistence/tests/gateway-xbackendpolicy-precedence`
- HTTPRoute sessionPersistence should take precedence over XBackendTrafficPolicy.
- Host: `whoami-xbtp-precedence.localhost`

```bash
curl -s -D - -H 'Host: whoami-xbtp-precedence.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-'
```

### gateway-xbackendpolicy-traefikservice
- Path: `script/kind-session-persistence/tests/gateway-xbackendpolicy-traefikservice`
- TraefikService stickiness should take precedence over HTTPRoute and XBackendTrafficPolicy.
- Host: `whoami-xbtp-ts.localhost`

```bash
curl -s -D - -H 'Host: whoami-xbtp-ts.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-'
```

### gateway-xbackendpolicy-disabled
- Path: `script/kind-session-persistence/tests/gateway-xbackendpolicy-disabled`
- XBackendTrafficPolicy should be ignored when experimentalChannel is false.
- Host: `whoami-xbtp-disabled.localhost`

```bash
curl -s -D - -H 'Host: whoami-xbtp-disabled.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-Policy-Session:'
for i in $(seq 1 10); do curl -s -H 'Host: whoami-xbtp-disabled.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done
```

### gateway-sticky-missing
- Path: `script/kind-session-persistence/tests/gateway-sticky-missing`
- Missing cookie should not stick; expect more than one hostname across requests.
- Host: `whoami-missing.localhost`

```bash
for i in $(seq 1 10); do curl -s -H 'Host: whoami-missing.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done
```

### ingressroute-sticky
- Path: `script/kind-session-persistence/tests/ingressroute-sticky`
- Pod-level stickiness using IngressRoute sticky on a K8s Service.
- Hosts:
  - Cookie: `whoami-ir-cookie.localhost`
  - Header: `whoami-ir-header.localhost`

Cookie test:

```bash
COOKIE=$(curl -s -D - -H 'Host: whoami-ir-cookie.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
curl -H 'Host: whoami-ir-cookie.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'
```

Header test:

```bash
STICKY=$(curl -s -D - -H 'Host: whoami-ir-header.localhost' http://localhost:8000/ | rg -i '^X-IR-Sticky:' | awk '{print $2}' | tr -d '\r')
curl -H 'Host: whoami-ir-header.localhost' -H "X-IR-Sticky: ${STICKY}" http://localhost:8000/ | rg -n '^Hostname:'
```

### gateway-traefikservice-multilevel
- Path: `script/kind-session-persistence/tests/gateway-traefikservice-multilevel`
- HTTPRoute has no sticky settings; stickiness is configured on the TraefikService at two levels.
- Hosts:
  - Cookie: `whoami-ts-cookie.localhost`
  - Header: `whoami-ts-header.localhost`

Cookie test:

```bash
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ts-cookie.localhost' http://localhost:8000/ -o /dev/null)
COOKIE1=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'ts-wrr-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
COOKIE2=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'ts-svc-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
curl -H 'Host: whoami-ts-cookie.localhost' -H "Cookie: ${COOKIE1}; ${COOKIE2}" http://localhost:8000/ | rg -n '^Hostname:'
```

Header test:

```bash
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ts-header.localhost' http://localhost:8000/ -o /dev/null)
H1=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TS-WRR:' | awk '{print $2}' | tr -d '\r')
H2=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TS-SVC:' | awk '{print $2}' | tr -d '\r')
curl -H 'Host: whoami-ts-header.localhost' -H "X-TS-WRR: ${H1}" -H "X-TS-SVC: ${H2}" http://localhost:8000/ | rg -n '^Hostname:'
```

### gateway-traefikservice-cookie-attrs
- Path: `script/kind-session-persistence/tests/gateway-traefikservice-cookie-attrs`
- TraefikService cookie attributes applied to sticky responses.
- Host: `whoami-ts-attrs.localhost`

```bash
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ts-attrs.localhost' http://localhost:8000/sticky -o /dev/null)
printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:'
```

### gateway-traefikservice-route-sticky
- Path: `script/kind-session-persistence/tests/gateway-traefikservice-route-sticky`
- HTTPRoute sets sessionPersistence while TraefikService defines multi-level sticky.
- Hosts:
  - Cookie: `whoami-tsr-cookie.localhost`
  - Header: `whoami-tsr-header.localhost`

Cookie test (expect TraefikService cookies, not `route-cookie`):

```bash
curl -s -D - -H 'Host: whoami-tsr-cookie.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:'
```

Header test (expect `X-TSR-WRR` and `X-TSR-SVC`, not `X-Route-Sticky`):

```bash
curl -s -D - -H 'Host: whoami-tsr-header.localhost' http://localhost:8000/ | rg -i '^X-'
```

### gateway-traefikservice-route-wrr
- Path: `script/kind-session-persistence/tests/gateway-traefikservice-route-wrr`
- HTTPRoute sets sessionPersistence while TraefikService sets WRR sticky only (no per-service sticky).
- Hosts:
  - Cookie: `whoami-tsr-wrr-cookie.localhost`
  - Header: `whoami-tsr-wrr-header.localhost`

Cookie test (expect `tsr-wrr-only`, not `route-cookie`):

```bash
curl -s -D - -H 'Host: whoami-tsr-wrr-cookie.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:'
```

Header test (expect `X-TSR-WRR-ONLY`, not `X-Route-Sticky`):

```bash
curl -s -D - -H 'Host: whoami-tsr-wrr-header.localhost' http://localhost:8000/ | rg -i '^X-'
```

## 20-request stickiness checks

Use these loops to verify pod-level stickiness stability.

```bash
COOKIE=$(curl -s -D - -H 'Host: whoami.localhost' http://localhost:8000/ | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done

STICKY=$(curl -s -D - -H 'Host: whoami-header.localhost' http://localhost:8000/ | rg -i '^X-Session-Id:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-header.localhost' -H "X-Session-Id: ${STICKY}" http://localhost:8000/ | rg -n '^Hostname:'; done
```

## Cleanup

```bash
kind delete cluster --name traefik-sticky
```
