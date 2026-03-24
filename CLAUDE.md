# CLAUDE.md — FedMinIO (MinIO Community Edition)

## Active Work Plan

See **`PLAN.md`** in this repo for the full reduction plan, phase-by-phase task list, and completion status. Always check it at the start of a session to know where we left off.

## Project Overview

This is the **MinIO Community Edition** source code — a high-performance, S3-compatible object storage server written in Go. The repository is in maintenance mode; active development has moved to AIStor. This codebase is AGPLv3 licensed and fully open source.

## Permissions

- Read, search, and analyze all code freely
- Edit any file as needed
- Run build and test commands

## Repository Structure

```
FedMinIO/
├── main.go                  # Entry point — calls cmd.Main()
├── cmd/                     # Core application (~900 .go files)
│   ├── admin-*.go           # Admin API handlers
│   ├── object-*.go          # S3 object operation handlers
│   ├── bucket-*.go          # Bucket management handlers
│   ├── iam-*.go             # Identity & access management
│   ├── xl-storage.go        # Erasure-coded local storage backend
│   ├── site-replication.go  # Multi-site replication
│   ├── metrics-v2.go        # Prometheus metrics
│   └── sts-*.go             # Security Token Service handlers
├── internal/                # Reusable internal packages (36 packages)
│   ├── auth/                # JWT and credential handling
│   ├── config/              # Configuration system
│   ├── crypto/              # Encryption (SSE-C, SSE-S3, SSE-KMS)
│   ├── dsync/               # Distributed lock/sync protocol
│   ├── grid/                # MinIO's internal gRPC-like RPC layer
│   ├── hash/                # Content hashing utilities
│   ├── http/                # HTTP server helpers
│   ├── jwt/                 # JWT token handling
│   ├── kms/                 # Key Management System integration
│   ├── logger/              # Structured logging
│   ├── s3select/            # S3 Select query engine
│   └── store/               # Generic persistent store
├── docs/                    # Documentation (36 topics)
├── buildscripts/            # Build automation scripts
├── helm/                    # Kubernetes Helm charts
├── Makefile                 # Build targets
├── Dockerfile               # Container image definitions
├── go.mod                   # Go 1.24.0 module definition
└── LICENSE                  # AGPLv3
```

## Build & Test

```bash
# Build the binary
make build

# Run all tests
make test

# Run specific package tests
go test ./cmd/...
go test ./internal/...

# Lint
make lint

# Build Docker image
make docker

# Cross-compile
./buildscripts/cross-compile.sh
```

## Key Concepts

### Storage Architecture
- **Erasure coding** via `cmd/xl-storage.go` and `cmd/erasure-*.go` — data is split across drives with parity
- **Server pools** — multiple MinIO nodes grouped into pools (`cmd/server-pool.go`)
- **Healing** — automatic data repair (`cmd/heal-*.go`)

### S3 API Implementation
- Object handlers: `cmd/object-handlers.go`
- Bucket handlers: `cmd/bucket-handlers.go`
- Multipart: `cmd/object-handlers-common.go`, `cmd/multipart-*.go`
- S3 Select: `internal/s3select/`

### IAM & Auth
- IAM system: `cmd/iam.go`, `cmd/iam-*.go`
- Policies: `cmd/policy-*.go`
- STS (temporary credentials): `cmd/sts-handlers.go`
- OpenID Connect / LDAP integration in `cmd/iam-store.go`

### Distributed Coordination
- `internal/dsync/` — distributed locking
- `internal/grid/` — internal node-to-node communication
- `cmd/peer-rest-*.go` — peer REST API between cluster nodes

### Configuration
- `internal/config/` — typed configuration subsystem
- Config is stored as objects in `.minio.sys/` bucket
- Environment variables override config (`MINIO_*` prefix)

### Metrics & Observability
- Prometheus metrics: `cmd/metrics-v2.go`
- Audit logging: `cmd/logger.go`, `internal/logger/`
- Health checks: `cmd/admin-health-info-handler.go`

## Common Code Patterns

### Adding a new S3 handler
1. Register route in `cmd/routers.go`
2. Implement handler function in appropriate `cmd/*-handlers.go` file
3. Call through to `ObjectLayer` interface (`cmd/object-api-interface.go`)

### ObjectLayer interface
All storage operations go through `ObjectLayer` in `cmd/object-api-interface.go`. Implementations:
- `cmd/erasure-*.go` — erasure-coded local storage
- `cmd/gateway-*.go` — cloud storage backends (GCS, Azure, etc.)

### Error handling
Use `toAPIError()` and `toAPIErrorCode()` in `cmd/api-errors.go` to map internal errors to S3 XML error responses.

## Notable Files

| File | Purpose |
|------|---------|
| `cmd/object-api-interface.go` | Core storage interface all backends implement |
| `cmd/api-errors.go` | S3 error code mapping |
| `cmd/routers.go` | HTTP route registration |
| `cmd/globals.go` | Global state and singletons |
| `cmd/config-current.go` | Runtime configuration |
| `internal/grid/grid.go` | Inter-node communication layer |
| `internal/dsync/drwmutex.go` | Distributed read-write mutex |

## Dependencies of Note

- `github.com/minio/minio-go` — S3 client SDK (used internally for gateway/replication)
- `github.com/IBM/sarama` — Kafka event notification
- `github.com/minio/pkg` — MinIO shared utilities
- `gopkg.in/ldap.v3` — LDAP authentication
- `github.com/go-jose/go-jose` — JWT/JWK handling
- `github.com/prometheus/client_golang` — Prometheus metrics
