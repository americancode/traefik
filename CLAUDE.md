# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Traefik?

Traefik (pronounced "traffic") is a modern HTTP reverse proxy and load balancer written in Go. It integrates with orchestrators (Docker, Kubernetes, Consul, ECS, etc.) and automatically configures itself by watching their APIs.

## Build Commands

```bash
make                      # Run generate + binary
make binary               # Build binary with version info (output: dist/<os>/<arch>/traefik)
make generate             # Run go generate for code generation
make generate-webui       # Build Vue.js dashboard (requires Docker)
make generate-crd         # Generate Kubernetes CRD clientset and manifests
make build-image          # Build Docker image (rebuilds webui)
make build-image-dirty    # Build Docker image (uses existing webui if present)
```

## Testing

```bash
make test                 # Run all tests (unit + integration + ui-unit)
make test-unit            # Run unit tests with coverage
make test-integration     # Run integration tests (requires Docker, 20m timeout)

# Run a single test
go test -v ./pkg/middlewares/headers -run TestSecureHeader

# Run tests in a specific package
go test -v ./pkg/server/...
```

Integration tests use testcontainers and require Docker. Fixtures are in `integration/fixtures/`.

## Linting

```bash
make lint                 # Run golangci-lint
make validate             # Run lint + validate-files (misspell, shellcheck)
make fmt                  # Format code with gofmt
```

## Architecture Overview

### Entry Point
- `cmd/traefik/traefik.go` - Main entry point, sets up CLI and calls runCmd()

### Core Packages (`/pkg/`)

**Configuration System:**
- `pkg/config/static/` - Boot-time configuration (entrypoints, providers)
- `pkg/config/dynamic/` - Runtime configuration (routers, services, middlewares)
- `pkg/config/runtime/` - Runtime data structures

**Server:**
- `pkg/server/` - Main server orchestration
- `pkg/server/router/` - Request routing
- `pkg/server/middleware/` - Middleware chain building
- `pkg/server/provider/` - Provider aggregation

**Providers** (configuration sources in `pkg/provider/`):
- `docker/`, `kubernetes/`, `file/`, `consulcatalog/`, `ecs/`, `nomad/`
- `acme/` - Let's Encrypt certificate automation
- `kv/` - Key-value stores (Consul, Etcd, Redis, ZooKeeper)
- Kubernetes has sub-providers: `crd/`, `gateway/`, `ingress/`

**Middleware** (`pkg/middlewares/`):
- 39+ implementations: auth, ratelimit, retry, compress, headers, redirect, etc.
- Each middleware handles HTTP/TCP request/response modification

**Protocol Handling:**
- `pkg/tcp/` - TCP proxy implementation
- `pkg/udp/` - UDP proxy implementation
- `pkg/proxy/` - HTTP reverse proxy

### Request Flow

1. Providers emit configuration updates to aggregator
2. ConfigurationWatcher receives updates via channel
3. Server rebuilds routes based on new configuration
4. Incoming requests are routed through middleware chain to backend services

### Code Generation

- `generate.go` triggers `go generate`
- `cmd/internal/gen/` contains generation scripts
- Generates dynamic configuration structures for plugin system

## Import Aliases (enforced by linter)

```go
// Kubernetes
import corev1 "k8s.io/api/core/v1"
import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
import kerror "k8s.io/apimachinery/pkg/api/errors"
import ktypes "k8s.io/apimachinery/pkg/types"

// Traefik CRD
import traefikv1alpha1 "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
import traefikclientset "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/generated/clientset/versioned"
```

## Key Linting Rules

- Use stdlib `errors` package, not `github.com/pkg/errors`
- No `fmt.Print*` or `spew.Print*` in production code
- Cyclomatic complexity threshold: 14
- Function statements max: 120
- Tag order in structs: description, json, toml, yaml, yml, label

## Branch Strategy

- `master` - New features (v3.x)
- `v3.6` - Bug fixes for v3
- `v2.11` - Security fixes only
