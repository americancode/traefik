### What does this PR do?

Adds Gateway API HTTPRoute sessionPersistence support for cookie and header stickiness, updates CRDs, and includes follow-up fixes. 

This is associated with GEP-1619 https://gateway-api.sigs.k8s.io/geps/gep-1619/#api

#11243 

The Kubernetes provider now also consumes experimental `XBackendTrafficPolicy` resources (commit `d96c0f6d6839e9e2b08288cb22eb31a30f04d48e`) so policies can emit the sticky header/cookie described in their `sessionPersistence`, the logic honors `experimentalChannel`, and TraefikService-level stickiness remains authoritative when a TraefikService backend is selected.  The `script/kind-session-persistence/tests/gateway-xbackendpolicy-*` suites document the new coverage for the policy itself, the HTTPRoute precedence, and the TraefikService precedence cases.


### Motivation

Enable cookie/header-based sticky sessions through the Kubernetes Gateway API without requiring Traefik-specific CRDs, and clarify behavior when TraefikService is used as the backend.


### More

- [x] Added/updated tests
- [x] Added/updated documentation


### Testing (Gateway API stickiness)

- [x] HTTPRoute sessionPersistence -> K8s Service (cookie/header)
- [x] HTTPRoute -> TraefikService (WRR sticky only)
- [x] HTTPRoute -> TraefikService (WRR + per-service sticky)

Test coverage & results (KIND):
- `basic-routing-gateway`: Gateway + HTTPRoute -> Service (baseline routing). Result: requests stay on a single backend.
- `basic-routing-ingress`: Ingress -> Service (kubernetesIngress enabled). Result: requests stay on a single backend.
- `basic-routing-ingressroute`: IngressRoute -> Service. Result: requests stay on a single backend.
- `gateway-sticky`: HTTPRoute sessionPersistence cookie/header -> Service (pod-level sticky). Result: cookie/header stick to a single pod.
- `gateway-sticky-failover`: Cookie sticky -> Service; delete the sticky pod to verify the fallback path. Result: sticky values persist until pod deletion, then requests reassign to other pods.
- `gateway-sticky-multiclient`: Cookie sticky -> Service; two clients stick to different pods. Result: each client keeps hitting its own pod.
- `gateway-sticky-missing`: No cookie -> Service; simulates requests after the sticky pod/cookie are deleted to ensure traffic spreads across pods. Result: the requests now hit multiple hostnames.
- `ingressroute-sticky`: IngressRoute sticky cookie/header -> Service (pod-level sticky). Result: cookie/header stick to a pod.
- `ingress-header-sticky`: Ingress -> Service (header sticky via service annotations). Result: header stickiness keeps the same pod across 10 requests.
- `ingress-cookie-sticky`: Ingress -> Service (cookie sticky via service annotations). Result: cookie stickiness keeps the same pod across 10 requests.
- `gateway-traefikservice-multilevel`: HTTPRoute (no sessionPersistence) -> TraefikService (WRR + per-service sticky) -> Service. Result: TraefikService cookies/headers keep the pod fixed.
- `gateway-traefikservice-route-sticky`: HTTPRoute sessionPersistence + TraefikService multi-level -> Service. Result: sessionPersistence stickiness and TraefikService sticky values align.
- `gateway-traefikservice-route-wrr`: HTTPRoute sessionPersistence + TraefikService WRR-only -> Service. Result: WRR-only cookie keeps the pod consistent.
- `gateway-xbackendpolicy-header`: Gateway API HTTPRoute or Service with an `XBackendTrafficPolicy` header sessionPersistence. Result: policy header is emitted and can replay sticky requests across 20 loops.
- `gateway-xbackendpolicy-disabled`: Same policy with `experimentalChannel: false`. Result: no policy header and hostname output varies.
- `gateway-xbackendpolicy-precedence`: HTTPRoute with its own sessionPersistence overrides the policy. Result: the response reflects the HTTPRoute-defined header.
- `gateway-xbackendpolicy-traefikservice`: TraefikService sticky cookies/headers sit alongside an `XBackendTrafficPolicy`. Result: TraefikService stickiness overrides the policy; the policy header/cookie disappears.

NOTE: When an HTTPRoute targets a TraefikService backend, HTTPRoute sessionPersistence is not applied. TraefikService stickiness controls behavior, and only TraefikService sticky values are returned:
- HTTPRoute -> TraefikService (multi-level): two values (WRR + per-service).
- HTTPRoute sessionPersistence + TraefikService (multi-level): still only two values (WRR + per-service).
- HTTPRoute sessionPersistence + TraefikService (WRR-only): one value (WRR).
No gateway-level cookie/header is added or overridden in these cases.

Cases validated locally:
- Gateway + HTTPRoute -> Service (basic routing).
- Ingress -> Service (basic routing).
- IngressRoute -> Service (basic routing).
- HTTPRoute sessionPersistence -> Service (cookie/header).
- HTTPRoute sessionPersistence -> Service with failover.
- HTTPRoute sessionPersistence -> Service with two clients.
- HTTPRoute sessionPersistence -> Service with missing cookie.
- IngressRoute sticky -> Service (cookie/header).
- Ingress -> Service with header sticky (service annotations).
- Ingress -> Service with cookie sticky (service annotations).
- HTTPRoute -> TraefikService (WRR + per-service sticky) -> Service.
- HTTPRoute sessionPersistence + TraefikService (WRR + per-service sticky) -> Service.
- HTTPRoute sessionPersistence + TraefikService (WRR-only) -> Service.
- HTTPRoute/sessionPersistence scenarios that also touch `XBackendTrafficPolicy` header coverage (`gateway-xbackendpolicy-*`).

How to test (YAML + bash per case; results in previous section):

basic-routing-gateway (Gateway + HTTPRoute -> Service)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gw-basic-whoami
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-gw-basic.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-gw-basic
          port: 80
```
```bash
kubectl apply --validate=false -f <basic-routing-gateway-yamls>
for i in $(seq 1 20); do curl -s -H 'Host: whoami-gw-basic.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

basic-routing-ingress (Ingress -> Service)
```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-basic
spec:
  rules:
    - host: whoami-ingress-basic.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: whoami-ingress-basic
                port:
                  number: 80
```
```bash
kubectl apply --validate=false -f <basic-routing-ingress-yamls>
for i in $(seq 1 20); do curl -s -H 'Host: whoami-ingress-basic.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

basic-routing-ingressroute (IngressRoute -> Service)
```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: ir-basic
spec:
  entryPoints:
    - web
  routes:
    - match: Host(`whoami-ir-basic.localhost`)
      kind: Rule
      services:
        - name: whoami-ir-basic
          port: 80
```
```bash
kubectl apply --validate=false -f <basic-routing-ingressroute-yamls>
for i in $(seq 1 20); do curl -s -H 'Host: whoami-ir-basic.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-sticky (HTTPRoute sessionPersistence -> Service)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sticky-route
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-cookie
          port: 80
      sessionPersistence:
        sessionName: traefik-sticky
        type: Cookie
```
```bash
kubectl apply --validate=false -f <gateway-sticky-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami.localhost' http://localhost:8000/ -o /dev/null)
COOKIE=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done

RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-header.localhost' http://localhost:8000/ -o /dev/null)
STICKY=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-Session-Id:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-header.localhost' -H "X-Session-Id: ${STICKY}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-sticky-failover (HTTPRoute sessionPersistence -> Service, delete sticky pod)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sticky-failover
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-failover.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-failover
          port: 80
      sessionPersistence:
        sessionName: failover-cookie
        type: Cookie
```
```bash
kubectl apply --validate=false -f <gateway-sticky-failover-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-failover.localhost' http://localhost:8000/ -o /dev/null)
COOKIE=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 5); do curl -s -H 'Host: whoami-failover.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done
kubectl -n default delete pod -l app=whoami-failover
sleep 5
for i in $(seq 6 20); do curl -s -H 'Host: whoami-failover.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-sticky-multiclient (HTTPRoute sessionPersistence -> Service, two clients)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sticky-multiclient
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-mc.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-mc
          port: 80
      sessionPersistence:
        sessionName: mc-cookie
        type: Cookie
```
```bash
kubectl apply --validate=false -f <gateway-sticky-multiclient-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-mc.localhost' http://localhost:8000/ -o /dev/null)
COOKIE_A=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-mc.localhost' http://localhost:8000/ -o /dev/null)
COOKIE_B=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami-mc.localhost' -H "Cookie: ${COOKIE_A}" http://localhost:8000/ | rg -n '^Hostname:'; done
for i in $(seq 1 20); do curl -s -H 'Host: whoami-mc.localhost' -H "Cookie: ${COOKIE_B}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-sticky-missing (HTTPRoute sessionPersistence -> Service, no cookie)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sticky-missing
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-missing.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-missing
          port: 80
      sessionPersistence:
        sessionName: missing-cookie
        type: Cookie
```
```bash
kubectl apply --validate=false -f <gateway-sticky-missing-yamls>
for i in $(seq 1 20); do curl -s -H 'Host: whoami-missing.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done
```
This test emulates requests after the sticky pod and cookie have been deleted so the client sends no session cookie; the repeated hostname checks prove the traffic now spreads across multiple pods.
---

ingressroute-sticky (IngressRoute sticky -> Service)
```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: ir-sticky-cookie
spec:
  entryPoints:
    - web
  routes:
    - match: Host(`whoami-ir-cookie.localhost`)
      kind: Rule
      services:
        - name: whoami-ir-cookie
          port: 80
          sticky:
            cookie:
              name: ir-cookie
```
```bash
kubectl apply --validate=false -f <ingressroute-sticky-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ir-cookie.localhost' http://localhost:8000/ -o /dev/null)
COOKIE=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami-ir-cookie.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done

RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ir-header.localhost' http://localhost:8000/ -o /dev/null)
STICKY=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-IR-Sticky:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-ir-header.localhost' -H "X-IR-Sticky: ${STICKY}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

ingress-header-sticky (Ingress sticky header via service annotations)
```yaml
apiVersion: v1
kind: Service
metadata:
  name: whoami-ingress-sticky
  annotations:
    traefik.ingress.kubernetes.io/service.sticky.header: "true"
    traefik.ingress.kubernetes.io/service.sticky.header.name: X-Ingress-Sticky
spec:
  ports:
    - name: http
      port: 80
  selector:
    app: whoami-ingress-sticky
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-sticky-header
spec:
  rules:
    - host: whoami-ingress-sticky.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: whoami-ingress-sticky
                port:
                  number: 80
```
```bash
kubectl apply --validate=false -f <ingress-header-sticky-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ingress-sticky.localhost' http://localhost:8000/ -o /dev/null)
STICKY=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-Ingress-Sticky:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 10); do curl -s -H 'Host: whoami-ingress-sticky.localhost' -H "X-Ingress-Sticky: ${STICKY}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

ingress-cookie-sticky (Ingress sticky cookie via service annotations)
```yaml
apiVersion: v1
kind: Service
metadata:
  name: whoami-ingress-cookie-sticky
  annotations:
    traefik.ingress.kubernetes.io/service.sticky.cookie: "true"
    traefik.ingress.kubernetes.io/service.sticky.cookie.name: ingress-sticky
spec:
  ports:
    - name: http
      port: 80
  selector:
    app: whoami-ingress-cookie-sticky
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-cookie-sticky
spec:
  rules:
    - host: whoami-ingress-cookie.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: whoami-ingress-cookie-sticky
                port:
                  number: 80
```
```bash
kubectl apply --validate=false -f <ingress-cookie-sticky-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ingress-cookie.localhost' http://localhost:8000/ -o /dev/null)
COOKIE=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'ingress-sticky' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 10); do curl -s -H 'Host: whoami-ingress-cookie.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-traefikservice-multilevel (HTTPRoute -> TraefikService WRR + per-service sticky -> Service)
```yaml
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata:
  name: ts-multi-cookie
spec:
  weighted:
    sticky:
      cookie:
        name: ts-wrr-cookie
    services:
      - name: whoami-ts-a-cookie
        port: 80
        sticky:
          cookie:
            name: ts-svc-cookie
      - name: whoami-ts-b-cookie
        port: 80
        sticky:
          cookie:
            name: ts-svc-cookie
```
```bash
kubectl apply --validate=false -f <gateway-traefikservice-multilevel-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ts-cookie.localhost' http://localhost:8000/ -o /dev/null)
COOKIE1=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'ts-wrr-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
COOKIE2=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'ts-svc-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami-ts-cookie.localhost' -H "Cookie: ${COOKIE1}; ${COOKIE2}" http://localhost:8000/ | rg -n '^Hostname:'; done

RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-ts-header.localhost' http://localhost:8000/ -o /dev/null)
H1=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TS-WRR:' | awk '{print $2}' | tr -d '\r')
H2=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TS-SVC:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-ts-header.localhost' -H "X-TS-WRR: ${H1}" -H "X-TS-SVC: ${H2}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-traefikservice-route-sticky (HTTPRoute sessionPersistence + TraefikService multi-level -> Service)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gw-tsr-cookie
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-tsr-cookie.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: tsr-multi-cookie
          kind: TraefikService
          group: traefik.io
          port: 80
      sessionPersistence:
        sessionName: route-cookie
        type: Cookie
```
```bash
kubectl apply --validate=false -f <gateway-traefikservice-route-sticky-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-tsr-cookie.localhost' http://localhost:8000/ -o /dev/null)
COOKIE1=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'tsr-wrr-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
COOKIE2=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'tsr-svc-cookie' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami-tsr-cookie.localhost' -H "Cookie: ${COOKIE1}; ${COOKIE2}" http://localhost:8000/ | rg -n '^Hostname:'; done

RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-tsr-header.localhost' http://localhost:8000/ -o /dev/null)
H1=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TSR-WRR:' | awk '{print $2}' | tr -d '\r')
H2=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TSR-SVC:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-tsr-header.localhost' -H "X-TSR-WRR: ${H1}" -H "X-TSR-SVC: ${H2}" http://localhost:8000/ | rg -n '^Hostname:'; done
```
---

gateway-traefikservice-route-wrr (HTTPRoute sessionPersistence + TraefikService WRR-only -> Service)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gw-tsr-wrr-cookie
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-tsr-wrr-cookie.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: tsr-wrr-cookie
          kind: TraefikService
          group: traefik.io
          port: 80
      sessionPersistence:
        sessionName: route-cookie
        type: Cookie
```
```bash
kubectl apply --validate=false -f <gateway-traefikservice-route-wrr-yamls>
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-tsr-wrr-cookie.localhost' http://localhost:8000/ -o /dev/null)
COOKIE=$(printf '%s' "$RESP_HEADERS" | rg -i '^Set-Cookie:' | rg 'tsr-wrr-only' | head -n1 | sed 's/Set-Cookie: //I' | cut -d';' -f1)
for i in $(seq 1 20); do curl -s -H 'Host: whoami-tsr-wrr-cookie.localhost' -H "Cookie: ${COOKIE}" http://localhost:8000/ | rg -n '^Hostname:'; done

RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-tsr-wrr-header.localhost' http://localhost:8000/ -o /dev/null)
H1=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TSR-WRR-ONLY:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-tsr-wrr-header.localhost' -H "X-TSR-WRR-ONLY: ${H1}" http://localhost:8000/ | rg -n '^Hostname:'; done
```

---

gateway-xbackendpolicy-header (XBackendTrafficPolicy header -> Service)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: whoami-xbtp
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-xbtp.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-xbtp
          port: 80
```
```yaml
apiVersion: gateway.networking.x-k8s.io/v1alpha1
kind: XBackendTrafficPolicy
metadata:
  name: whoami-xbtp
  namespace: default
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: whoami-xbtp
  sessionPersistence:
    type: Header
    sessionName: X-Policy-Session
```
```bash
kubectl apply --validate=false -f script/kind-session-persistence/tests/gateway-xbackendpolicy-header/
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-xbtp.localhost' http://localhost:8000/ -o /dev/null)
POLICY=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-Policy-Session:' | awk '{print $2}' | tr -d '\r')
for i in $(seq 1 20); do curl -s -H 'Host: whoami-xbtp.localhost' -H "X-Policy-Session: ${POLICY}" http://localhost:8000/ | rg -n '^Hostname:'; done
```

gateway-xbackendpolicy-disabled (policy ignored when experimentalChannel=false)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: whoami-xbtp-disabled
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-xbtp-disabled.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-xbtp-disabled
          port: 80
```
```yaml
apiVersion: gateway.networking.x-k8s.io/v1alpha1
kind: XBackendTrafficPolicy
metadata:
  name: whoami-xbtp-disabled
  namespace: default
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: whoami-xbtp-disabled
  sessionPersistence:
    type: Header
    sessionName: X-Policy-Session
```
```bash
kubectl apply --validate=false -f script/kind-session-persistence/tests/gateway-xbackendpolicy-disabled/
curl -s -D - -H 'Host: whoami-xbtp-disabled.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-Policy-Session:' || true
for i in $(seq 1 20); do curl -s -H 'Host: whoami-xbtp-disabled.localhost' http://localhost:8000/ | rg -n '^Hostname:'; done | sort -u
```

gateway-xbackendpolicy-precedence (HTTPRoute overrides the policy)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: whoami-xbtp-precedence
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-xbtp-precedence.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-xbtp-precedence
          port: 80
      sessionPersistence:
        type: Header
        sessionName: X-Route-Session
```
```yaml
apiVersion: gateway.networking.x-k8s.io/v1alpha1
kind: XBackendTrafficPolicy
metadata:
  name: whoami-xbtp-precedence
  namespace: default
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: whoami-xbtp-precedence
  sessionPersistence:
    type: Header
    sessionName: X-Policy-Session
```
```bash
kubectl apply --validate=false -f script/kind-session-persistence/tests/gateway-xbackendpolicy-precedence/
curl -s -D - -H 'Host: whoami-xbtp-precedence.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-Session-Id:' | awk '{print $2}' | tr -d '\r'
curl -s -D - -H 'Host: whoami-xbtp-precedence.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-Policy-Session:' || true
```

gateway-xbackendpolicy-traefikservice (TraefikService stickiness wins)
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: whoami-xbtp-ts
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-xbtp-ts.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-xbtp-ts
          kind: TraefikService
          group: traefik.io
          port: 80
```
```yaml
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata:
  name: whoami-xbtp-ts
  namespace: default
spec:
  weighted:
    sticky:
      header:
        name: X-TS-Sticky
    services:
      - name: whoami-xbtp-ts
        port: 80
```
```yaml
apiVersion: gateway.networking.x-k8s.io/v1alpha1
kind: XBackendTrafficPolicy
metadata:
  name: whoami-xbtp-ts
  namespace: default
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: whoami-xbtp-ts
  sessionPersistence:
    type: Header
    sessionName: X-Policy-Session
```
```bash
kubectl apply --validate=false -f script/kind-session-persistence/tests/gateway-xbackendpolicy-traefikservice/
RESP_HEADERS=$(curl -s -D - -H 'Host: whoami-xbtp-ts.localhost' http://localhost:8000/ -o /dev/null)
TSR=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TS-WRR:' | awk '{print $2}' | tr -d '\r')
TSVC=$(printf '%s' "$RESP_HEADERS" | rg -i '^X-TS-SVC:' | awk '{print $2}' | tr -d '\r')
curl -s -H 'Host: whoami-xbtp-ts.localhost' -H "X-TS-WRR: ${TSR}" -H "X-TS-SVC: ${TSVC}" http://localhost:8000/ | rg -n '^Hostname:'
curl -s -D - -H 'Host: whoami-xbtp-ts.localhost' http://localhost:8000/ -o /dev/null | rg -i '^X-Policy-Session:' || true
```

Example HTTPRoute sessionPersistence (cookie):

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sticky-route
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-cookie
          port: 80
      sessionPersistence:
        sessionName: traefik-sticky
        type: Cookie
```

Example HTTPRoute sessionPersistence (header):

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sticky-route-header
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-header.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: whoami-header
          port: 80
      sessionPersistence:
        sessionName: X-Session-Id
        type: Header
```

Example HTTPRoute -> TraefikService (multi-level sticky):

```yaml
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata:
  name: ts-multi-cookie
  namespace: default
spec:
  weighted:
    sticky:
      cookie:
        name: ts-wrr-cookie
    services:
      - name: whoami-ts-a-cookie
        port: 80
        sticky:
          cookie:
            name: ts-svc-cookie
      - name: whoami-ts-b-cookie
        port: 80
        sticky:
          cookie:
            name: ts-svc-cookie
```

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gw-ts-multi-cookie
  namespace: default
spec:
  parentRefs:
    - name: traefik-gateway
  hostnames:
    - whoami-ts-cookie.localhost
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: ts-multi-cookie
          kind: TraefikService
          group: traefik.io
          port: 80
```

### Additional Notes

Header stickiness for IngressRoute and TraefikService requires CRDs that include `sticky.header`. Also, multi-level stickiness requires capturing cookies/headers from the same response to keep values aligned.
