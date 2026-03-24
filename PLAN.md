# FedMinIO: Local Test Program + MVP Reduction Plan

## Context

MinIO has archived this repository. The goal is to maintain it as a purpose-built, minimum-surface S3-compatible object storage server for secure federal environments (SCIF / air-gapped networks). S3 storage is local-only — no cloud connectivity, no external dependencies, reduced attack surface. This plan covers: (1) a small local test program for development, and (2) a phased approach to strip ~280KB of code and 30+ external dependency packages down to the essential S3 core.

## Part 1: Local Test Program

**Location:** `localtest/minio_test.go` (package `localtest_test`, part of root module)

**Approach:** Subprocess — build the binary, launch it against a temp dir, poll the health endpoint, then run tests via `github.com/minio/minio-go/v7` (already in go.mod).

**Run with:** `go test ./localtest/ -v -timeout 120s`

**Design:**
- `TestMain`: build binary (`go build -o /tmp/minio-test .`), start subprocess with `--address :0` on a random port, poll `GET /minio/health/live` every 100ms up to 30s, run tests, teardown
- Fixed test credentials: `MINIO_ROOT_USER=testadmin` / `MINIO_ROOT_PASSWORD=testpassword`
- Test cases (sequential, single bucket `fed-test-bucket`):
  1. `TestCreateBucket` — create bucket, verify exists
  2. `TestPutGetObject` — upload 64KB object, download and verify bytes match
  3. `TestListObjects` — verify object appears in listing
  4. `TestMultipartUpload` — upload 7MB random object (forces minio-go multipart), verify size
  5. `TestDeleteObject` — delete, verify gone from listing
  6. `TestObjectLockWORM` — create bucket with object lock enabled, put object with COMPLIANCE retention, verify delete is rejected (federal compliance validation)
  7. `TestDeleteBucket` — cleanup

**Key file to create:** `localtest/minio_test.go`

---

## Part 2: Feature Removal — MVP for Federal S3

### What to Keep
- Core S3 API: object CRUD, multipart upload, bucket ops, versioning
- Built-in IAM: access keys + bucket policies (no external IdP)
- Erasure coding (local drives)
- TLS (mandatory for federal)
- Server-side encryption (SSE-S3, SSE-C, SSE-KMS)
- Object Lock / WORM (FedRAMP compliance)
- Lifecycle management / ILM (retention policies)
- S3 SigV4 authentication
- Basic health check endpoint (`/minio/health/live`)
- Webhook audit log target (for local sidecar audit log)

### What to Remove (by phase)

#### ~~Phase 1: FTP / SFTP / Auto-Update~~ ✅ COMPLETE
Files deleted, startup blocks removed, update handlers stubbed, deps removed from go.mod.

#### Phase 2: Divorce from MinIO Dependency Ecosystem

**Goal:** Make the build entirely self-contained and independent of MinIO-controlled repositories. A federal fork must not rely on external repos that could be taken down, relicensed, or changed without notice.

**Step 1 — Vendor all dependencies (immediate, zero code changes)**
```bash
go mod vendor
```
This copies every dependency into `vendor/` in the repo. The build becomes fully air-gappable — no internet access, no module proxy, no github.com required. Add to Makefile `sanity` and `build` targets: use `-mod=vendor` flag.

**Step 2 — Rename the module path**
Change `go.mod` module declaration from `github.com/minio/minio` to a neutral path (e.g., `github.com/fedminio/server` or an org-controlled path). This requires a global find-and-replace of all internal import paths. Affects every `.go` file in the repo — use `sed` or a Go refactoring tool.

**Step 3 — Incremental replacement of `github.com/minio/*` deps**

Full audit of all 15 direct `minio/*` dependencies (see table below). Priority order: replace/inline easiest first to reduce supply-chain exposure.

| Dependency | Files | Strategy | Effort |
|---|---|---|---|
| `madmin-go/v3` | 126 | Fork into repo as `internal/madmin/`; strip to types actually used post-phase-6 | High |
| `pkg/v3` | 169 | Fork into repo as `internal/minio-pkg/`; trim unused subpackages | High |
| `minio-go/v7` | 73 (but mostly warm-tier, removed in Phase 3) | After Phase 3, only `localtest/` needs it; keep for tests, replace with `aws-sdk-go` if needed | Low |
| `console` | 3 | Removed entirely in Phase 7 | Done in Phase 7 |
| `kms-go/kes` + `kms-go/kms` | 10 | Fork or vendor; KES is MinIO-specific — evaluate if needed for SCIF | Medium |
| `sio` | 10 | Must keep/fork; implements DAREv2 format — changing breaks existing encrypted data | Fork |
| `mux` | 40 | Replace with `github.com/gorilla/mux` (public, widely maintained) or stdlib `http.ServeMux` | Low |
| `cli` | 4 | Replace with `github.com/urfave/cli/v2` (what minio/cli is based on) | Low |
| `xxml` | 3 | Replace with stdlib `encoding/xml` | Low |
| `highwayhash` | 6 | Replace with `github.com/cespare/xxhash/v2` (already in go.mod) | Low |
| `dnscache` | 1 | Inline ~100 lines locally | Low |
| `dperf` | 1 | Inline in `cmd/speedtest.go` or remove speedtest endpoint | Low |
| `zipindex` | 2 | Inline with stdlib `archive/zip` | Low |
| `csvparser` | 4 | Remove with S3 Select (Phase 7) | Done in Phase 7 |
| `simdjson-go` | 9 | Remove with S3 Select (Phase 7) | Done in Phase 7 |

**Indirect `minio/*` deps** (pulled transitively, no direct calls):
`colorjson`, `crc64nvme`, `filepath`, `mc`, `md5-simd`, `websocket` — all eliminated once the packages that pull them are vendored/forked.

**Recommended order within Phase 2:**
1. `go mod vendor` — immediate air-gap capability
2. Module rename — breaks all external references to the old path
3. Quick replacements: `mux`, `cli`, `xxml`, `highwayhash`, `dnscache`, `dperf`, `zipindex`
4. Fork `sio` into `internal/sio/`
5. Fork `madmin-go` into `internal/madmin/` (do after Phase 6+ when many madmin types are no longer needed)
6. Fork `pkg/v3` into `internal/minio-pkg/` (can be done incrementally, subpackage by subpackage)
7. Evaluate `kms-go` — if SCIF deployments use an external KMS, keep; if all-local, stub it out

**Verification:**
```bash
go build -mod=vendor ./...   # must succeed with no network access
go test -mod=vendor ./localtest/ -v -timeout 120s
```

---

#### Phase 3: Cloud/Warm Tiering (Medium Risk — api-errors.go surgery, was Phase 2)
**Delete:**
```
cmd/warm-backend*.go (5 files)
cmd/tier*.go (7 files including tier_gen*, tier-last-day-stats*, tier-sweeper*)
```
**Edit:**
- `cmd/api-errors.go`: remove Azure (`azcore`) and Google API (`googleapi`) imports and their error case blocks
- `cmd/globals.go`: remove `globalTierConfigMgr` and `globalTransitionState`
- `cmd/server-main.go`: remove tier/transition init blocks (lines 1067-1078)
- `cmd/admin-router.go`: remove tier route registrations

**Deps removed:** `cloud.google.com/go/storage`, all `github.com/Azure/azure-sdk-for-go/sdk/*`, `google.golang.org/api`

#### Phase 4: External Notifications (High Risk — config-current.go surgery, was Phase 3)
**Delete:**
```
internal/event/target/{amqp,elasticsearch,kafka,kafka_scram_client_contrib,mqtt,mysql,nats,nsq,postgresql,redis}.go
internal/logger/target/kafka/
```
Keep: `internal/event/target/webhook.go` (no external broker dependency, used for audit forwarding)

**Edit:**
- `cmd/config-current.go`: remove `notify` import, all `config.NotifySubSys` / `config.AuditKafkaSubSys` KVS and Help entries and lookup cases
- `internal/logger/config.go` + `targets.go`: remove kafka import and `initKafkaTargets`
- `cmd/config-migrate.go` + `cmd/config-versions.go`: remove removed-target migration code

**Deps removed:** `github.com/IBM/sarama`, `github.com/rabbitmq/amqp091-go`, all `nats-io/*`, `github.com/elastic/go-elasticsearch/v7`, `github.com/go-sql-driver/mysql`, `github.com/lib/pq`, `github.com/gomodule/redigo`, `github.com/nsqio/go-nsq`, `github.com/eclipse/paho.mqtt.golang`, `github.com/xdg/scram`, `github.com/jcmturner/*`

#### Phase 5: LDAP / OpenID / OPA (Highest Risk — IAM surgery, was Phase 4)
**Delete:**
```
internal/config/identity/ldap/   (entire directory)
internal/config/identity/openid/ (entire directory)
internal/config/identity/plugin/ (entire directory)
internal/config/policy/opa/      (entire directory)
cmd/admin-handlers-idp-ldap.go
cmd/admin-handlers-idp-openid.go
```
**Edit (surgical — do not delete):**
- `cmd/iam-store.go`: remove `openid.Config` field, `openid.LookupConfig` call; replace `openid.DummyRoleARN.String()` with literal ARN string constant `"arn:aws:iam:::role/FedMVP_NoRole"` — verify this value is only used as a sentinel, not stored in IAM data
- `cmd/sts-handlers.go`: stub LDAP/WebIdentity/SSO/ClientGrants STS handlers with `ErrNotImplemented`; remove LDAP STS route from `registerSTSRouter`; keep `AssumeRole` (internal IAM)
- `cmd/utils.go`: remove `MockOpenIDTestUserInteraction`, `OpenIDClientAppParams`; remove `oidc` and `oauth2` imports
- `cmd/config-current.go`: remove `xldap`, `openid`, `idplugin`, `xtls`, `opa`, `polplugin` imports + all their KVS/Help/lookup entries
- `cmd/config-migrate.go` + `cmd/config-versions.go`: remove LDAP/OpenID migration code
- `cmd/admin-handlers-idp-config.go`: remove LDAP/OpenID imports; stub or remove handler bodies

**Deps removed:** `github.com/go-ldap/ldap/v3`, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, all `github.com/lestrrat-go/*`, `github.com/go-jose/go-jose/v4`, `github.com/Azure/go-ntlmssp`

#### Phase 6: Site Replication + Bucket Replication + Batch Jobs (was Phase 5)
**Important:** `internal/bucket/replication/` package is KEPT — 20+ core files import its `StatusType` constants. Only `cmd/` engine files are deleted.

**Delete:**
```
cmd/site-replication*.go (all variants + admin handler)
cmd/bucket-replication*.go (all variants)
cmd/batch-*.go (all variants)
```
**Create (same commit):** `cmd/replication-stub.go` — defines no-op `ReplicationPool` struct with all methods called by surviving files (`deleteResyncMetadata`, `getMRF`, `queueMRFSave`, `initResync`, `GetNonBlocking`, `Get`, `IsSet`, `Set`), stub `initBackgroundReplication`, and `var globalReplicationPool = once.NewSingleton[ReplicationPool]()`

**Edit:**
- `cmd/server-main.go`: remove replication/site-replication/batch init calls (lines 1058-1059, 1113-1115, 1118-1120, 1132-1138)
- `cmd/globals.go`: remove `globalSiteReplicationSys`, `globalBatchJobsMetrics`, `globalBatchJobPool`
- `cmd/config-current.go`: remove `batch` import and `config.BatchSubSys` entries
- `cmd/bucket-handlers.go`, `cmd/handler-api.go`, `cmd/notification.go`: remove surviving calls to replication pool methods

**Risk:** Getting `replication-stub.go` method signatures wrong causes type mismatch errors — read the generic `once.Singleton[T]` interface carefully before writing the stub.

#### Phase 7: Lambda + S3 Select + Console UI + Call-home + Metrics (was Phase 6)
**Delete:**
```
cmd/object-lambda-handlers.go, cmd/object-lambda-handlers_test.go
internal/config/lambda/           (entire directory)
internal/s3select/                 (entire directory)
cmd/callhome.go
cmd/metrics*.go                    (all metrics files including metrics-v3-*.go, metrics-router.go)
```
**Edit:**
- `cmd/api-errors.go`: remove `levent` import and lambda ARN error cases
- `cmd/object-handlers.go`: stub `SelectObjectContentHandler` with `ErrNotImplemented`; remove `s3select` import
- `cmd/bucket-lifecycle.go`: remove `s3select` import — verify which symbols are used and extract/stub them
- `cmd/globals.go`: remove `globalLambdaTargetList`, `consoleapi` import, `globalConsoleSrv`
- `cmd/config-current.go`: remove `lambda`, `callhome`, `subnet` imports and KVS/Help/lookup entries
- `cmd/common-main.go`: remove `consoleapi`, `operations`, `consoleoauth2`, `consoleCerts` imports; remove `initConsoleServer` function body
- `cmd/server-main.go`: remove console init block (lines 1009-1023), remove `startResourceMetricsCollection()` call
- `cmd/routers.go`: remove `registerMetricsRouter(router)` call
- `cmd/http-stats.go`: replace Prometheus histogram/counter types with `sync/atomic` int64 no-op implementations

**Deps removed:** `github.com/minio/console`, all `go-openapi/*`, `github.com/minio/simdjson-go`, `github.com/fraugster/parquet-go`, `github.com/minio/csvparser`, `github.com/cosnicolaou/pbzip2`, `github.com/apache/thrift`, all `prometheus/*` packages

#### Phase 8: Etcd DNS Federation (Optional, was Phase 7)
Low value for single-node SCIF. `globalDNSConfig` is already nil-guarded so runtime works without this. Defer unless binary size is a priority.

**Delete:** `cmd/iam-etcd-store.go`, `cmd/etcd.go`, `internal/config/etcd/`
**Edit:** `cmd/iam.go` (always use IAMObjectStore), `cmd/globals.go`, `cmd/common-main.go`, `cmd/config-current.go`
**Deps removed:** all `go.etcd.io/etcd/*`, `google.golang.org/grpc` (verify no other uses first)

---

## Part 3: Federal-Specific Additions

### 1. FIPS 140-2 Build Mode
Create `cmd/crypto_fips.go`:
```go
//go:build boringcrypto
package cmd
import _ "crypto/tls/fipsonly"
```
Build: `GOEXPERIMENT=boringcrypto CGO_ENABLED=0 go build -o minio-fips .`
Add `make fips-build` target to Makefile.

### 2. TLS Hardening
In TLS config assembly (likely `internal/http/server.go`): enforce `MinVersion: tls.VersionTLS12`, remove non-forward-secret cipher suites, set `CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}`.

### 3. Air-Gapped Mode Flag
Add `MINIO_AIR_GAPPED=on` env var that sets `globalInplaceUpdateDisabled = true` and prints a startup banner confirming air-gapped mode. Allows config-as-code auditing in SCIF environments.

### 4. Local File Audit Logger
Create `internal/logger/target/localfile/localfile.go` — a `Target` that writes JSON audit events to a rotating local file. Config: `MINIO_AUDIT_LOCAL_FILEPATH=/var/log/minio/audit.jsonl`. No outbound network dependency.

---

## Build Strategy

**Delete code, not build tags.** Rationale: direct deletion is auditable (git history preserved), attack surface is genuinely reduced, SCIF auditors can verify absence by absence. Build tags create long-term maintenance overhead and can be accidentally re-enabled. Exception: `//go:build boringcrypto` for FIPS additions.

**After each phase:** `go mod tidy && go build ./...`

## Critical Files (must be edited carefully at each phase)

| File | Role |
|------|------|
| `cmd/server-main.go` | ~15 call sites to remove across phases; edit before deleting dependent files |
| `cmd/config-current.go` | Imports 12+ packages being removed; central knot for phases 3-6 |
| `cmd/globals.go` | Global vars for console, callhome, lambda, replication, tier, batch; edit lockstep with deletions |
| `cmd/bucket-replication.go` | Defines `globalReplicationPool` used by 5 surviving files; stub must be created same commit as deletion |
| `cmd/api-errors.go` | Imports Azure, Google, lambda; edit in phases 3 and 7 |
| `cmd/iam-store.go` | Contains `openid.DummyRoleARN`; verify sentinel value before replacing with literal |

## Verification

**After each phase:**
1. `go build ./...` — must pass
2. `go mod tidy` — confirm dep count reduction
3. `go test ./cmd/... -count=1 -run TestErasure` — verify core storage tests pass

**End-to-end:**
1. `go build -o /tmp/minio-fed .`
2. `go test ./localtest/ -v -timeout 120s` — all 7 test cases pass
3. `GOEXPERIMENT=boringcrypto CGO_ENABLED=0 go build -o /tmp/minio-fips .` — FIPS binary compiles

## Estimated Outcome

| Metric | Before | After |
|--------|--------|-------|
| Direct dependencies | ~48 | ~20-22 |
| Indirect dependencies | ~280 | ~80-100 |
| Removed code (cmd/ + internal/) | — | ~280KB |
| Binary size reduction | — | ~20-30% |
| External network dependencies at runtime | Many | Zero (health check only) |
