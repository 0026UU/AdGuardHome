# AdGuard Home Native Cluster Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional multi-node aggregation, configuration synchronization, health monitoring, and management UI while preserving stock single-node behavior.

**Architecture:** A new `internal/cluster` package wraps the existing HTTP mux and exposes a separate Bearer-authenticated node API.  Original AGH handlers execute locally first; merge and sync policies live in focused subpackages.  The default React client receives cluster pages and a dashboard node selector.

**Tech Stack:** Go 1.27.2, `net/http`, `go.yaml.in/yaml/v4`, React 16, Redux, react-i18next, Webpack, Vitest.

## Global Constraints

- Base commit is `71edcc505341752a34aafc5792958dd6e1eb9d9c`.
- Do not change `AdGuardHome.yaml` schema, GPL-3.0 license, NOTICE, or copyright headers.
- No raft, etcd, message queue, WebSocket, DNS traffic forwarding, or shared DHCP service.
- Missing/disabled `cluster.yaml` must preserve original behavior.
- Never expose tokens, raw request bodies, or raw `error.message` in UI responses.
- Node API requests require Bearer authentication; browser UI uses existing AGH sessions.
- Use TDD: write each focused failing test before its implementation and run it after each change.

---

### Task 1: Configuration and cluster lifecycle

**Files:**
- Create: `internal/cluster/config.go`, `internal/cluster/cluster.go`, `internal/cluster/config_test.go`
- Modify: `internal/home/web.go`, `internal/home/home.go`

**Interfaces:**
- Produces: `LoadConfig(workDir string) (*Config, error)`, `New(*Config, Dependencies) (*Cluster, error)`, `(*Cluster).Middleware(http.Handler) http.Handler`, `(*Cluster).Start(context.Context) error`, `(*Cluster).Close(context.Context) error`.
- `Dependencies` contains `Logger *slog.Logger`, `Local http.Handler`, `Version string`, and `WorkDir string`.

- [ ] **Step 1: Write config tests** covering absent files, valid durations/members, duplicate names, missing tokens, invalid modes and URLs, and atomic round-trip saving.

```go
func TestLoadConfigMissing(t *testing.T) {
    conf, err := LoadConfig(t.TempDir())
    if err != nil || conf.Enabled { t.Fatalf("conf=%+v err=%v", conf, err) }
}
```

- [ ] **Step 2: Run `go test ./internal/cluster`** and expect undefined-symbol failure.
- [ ] **Step 3: Implement typed YAML config**, validation, restrictive atomic save, and no-op middleware when disabled.
- [ ] **Step 4: Run `go test ./internal/cluster`** and expect PASS.
- [ ] **Step 5: Wire lifecycle into Web startup/shutdown**, without registering anything during first run or disabled mode.
- [ ] **Step 6: Run `go test ./internal/cluster ./internal/home`**, then commit `feat: add cluster configuration lifecycle`.

### Task 2: Token API, members, and health state

**Files:**
- Create: `internal/cluster/nodes.go`, `internal/cluster/api.go`, `internal/cluster/nodes_test.go`, `internal/cluster/api_test.go`
- Create: `internal/cluster/types.go`

**Interfaces:**
- Produces `NodeStatus`, `Status()`, `Register(Member) error`, `Unregister(name string) error`, `Ping()`, and authenticated internal `read`/`apply` dispatch.
- `NodeStatus` exposes `name`, `api_base`, `online`, `last_heartbeat`, `rtt_ms`, `version`, `pending_sync`, and `degraded` without tokens.

- [ ] **Step 1: Add API tests** for empty, malformed, and wrong authorization headers returning 401 and valid Bearer tokens reaching ping.

```go
req := httptest.NewRequest(http.MethodGet, "/control/cluster/ping", nil)
rr := httptest.NewRecorder()
api.ServeHTTP(rr, req)
if rr.Code != http.StatusUnauthorized { t.Fatalf("code=%d", rr.Code) }
```

- [ ] **Step 2: Run tests and verify authentication failures.**
- [ ] **Step 3: Implement constant-time token checks, sanitized JSON errors, ping, and internal dispatch allowlists.**
- [ ] **Step 4: Add health tests** using fake ping transports: three failures produce offline; one subsequent success restores online and queues alignment.
- [ ] **Step 5: Implement member snapshots and a 30-second health loop** with RTT and version collection.
- [ ] **Step 6: Run `go test ./internal/cluster -race`** and commit `feat: add cluster node health API`.

### Task 3: Read aggregation

**Files:**
- Create: `internal/cluster/merge/rules.go`, `stats.go`, `querylog.go`, `clients.go`, and corresponding `_test.go` files
- Create: `internal/cluster/read.go`, `internal/cluster/read_test.go`

**Interfaces:**
- `merge.Stats(responses []Response) (map[string]any, error)`
- `merge.QueryLog(responses []Response, limit int) (map[string]any, error)`
- `merge.Clients(responses []Response) (map[string]any, error)`
- `Response` contains `Node string`, `Body []byte`, and `Status int`.

- [ ] **Step 1: Write exact-value stats tests** for sums, weighted averages, shortest-series right alignment, top-10 maps, upstream weighting, and degraded metadata.

```go
// Two nodes with 100 queries at 0.10 and 300 at 0.20 must produce 0.175.
```

- [ ] **Step 2: Run `go test ./internal/cluster/merge`** and verify initial failure.
- [ ] **Step 3: Implement generic JSON-preserving merge functions** and explicit rule registration.
- [ ] **Step 4: Add query-log and client tests** for newest-first truncation, `agh_node`, name/IP merging, array responses, and legacy `found` wrappers.
- [ ] **Step 5: Implement concurrent local/remote reads**, three-second timeouts, per-target recent clamping, node selection, and 502 when every target fails.
- [ ] **Step 6: Run `go test ./internal/cluster/... -race`** and commit `feat: aggregate cluster read APIs`.

### Task 4: Broadcast, retry, audit, and alignment

**Files:**
- Create: `internal/cluster/sync/sync.go`, `align.go`, `audit.go` and tests
- Create: `internal/cluster/write.go`, `internal/cluster/write_test.go`

**Interfaces:**
- `Broadcaster.Broadcast(ctx, Operation) Report`
- `Operation` contains ID, source, method, path, query, body, and content type.
- `Aligner.Align(ctx, target string) Report`; `Audit.List()` returns sanitized entries.

- [ ] **Step 1: Write recorder/middleware tests** proving non-2xx local writes never broadcast and successful writes preserve every body byte and query parameter.
- [ ] **Step 2: Implement local-first write capture** and source-loop suppression for internal dispatch.
- [ ] **Step 3: Write retry tests** with a fake clock/transport, verifying 30-second scheduling, ten attempts, pending state, and exhaustion audit.
- [ ] **Step 4: Implement concurrent broadcast and bounded retry queues**, rejecting new duplicate operation IDs per target.
- [ ] **Step 5: Write alignment tests** using complete filtering, DNS, stats, and client fixtures; verify canonical diffs, master-wins, reject policy, redaction, and result counts.
- [ ] **Step 6: Implement table-driven full alignment** and native collection operations where no whole-object PUT exists.
- [ ] **Step 7: Run `go test ./internal/cluster/... -race`** and commit `feat: synchronize cluster configuration`.

### Task 5: React management UI and localization

**Files:**
- Create: `client/src/components/cluster/NodeStatusBadge.tsx`, `NodeSwitcher.tsx`, `DegradedBanner.tsx`, `ClusterSettingsForm.tsx`, `client/src/pages/ClusterNodes.tsx`, `ClusterSettings.tsx`, `client/src/services/cluster.ts`
- Create: `client/src/actions/cluster.ts`, `client/src/reducers/cluster.ts`, component tests
- Modify: `client/src/components/App/index.tsx`, `Header/Menu.tsx`, `Dashboard/index.tsx`, `helpers/constants.ts`, `reducers/index.ts`, `initialState.ts`, `__locales/en.json`, `__locales/zh-cn.json`

**Interfaces:**
- Service methods call `cluster/status`, `cluster/nodes`, `cluster/register`, `cluster/unregister`, `cluster/settings`, `cluster/align`, and `cluster/audit` through the existing API base.
- Redux stores `enabled`, `nodes`, `selectedNode`, `degradedNodes`, and settings; `selectedNode` persists in localStorage.

- [ ] **Step 1: Write service/component tests** for loading, empty, degraded, registration-error, and selection states using localized copy.
- [ ] **Step 2: Run focused Vitest tests** and verify missing-module failure.
- [ ] **Step 3: Implement API service, Redux state, badges, banners, and settings form** with stable error-code mapping and no raw server error display.
- [ ] **Step 4: Add routes/navigation/dashboard selection**, ten-second polling, and light responsive CSS.
- [ ] **Step 5: Synchronize en/zh-CN keys and placeholders**, run JSON parsing and the locale audit script.
- [ ] **Step 6: Run `npm run typecheck && npm run test && npm run build-prod`** and commit `feat: add cluster management UI`.

### Task 6: Integration and acceptance evidence

**Files:**
- Create: `scripts/cluster-integration.py`, `docs/cluster-acceptance.md`, `docker-compose.cluster-test.yml`
- Create: `scripts/testdata/cluster-master.yaml`, `cluster-member.yaml` with non-secret test tokens

**Interfaces:**
- The harness builds one binary, starts isolated master/member work directories, configures DNS ports above 1024, executes API assertions, captures desktop/mobile UI evidence, and always terminates children.

- [ ] **Step 1: Add harness assertions** for node status, unauthenticated 401, two-node sums, 24-hour common window, rule add/delete, DNS persistence, outage degradation, and recovery alignment.
- [ ] **Step 2: Run the harness** and record exact PASS/FAIL evidence without credentials.
- [ ] **Step 3: Run `go vet ./... && go build ./...`** and the complete frontend verification.
- [ ] **Step 4: Compare a disabled-cluster instance against stock routes** and confirm no listener or background cluster activity.
- [ ] **Step 5: Commit `test: add cluster acceptance harness`** with the evidence table.

### Task 7: Release packaging and deployment

**Files:**
- Create: `scripts/cluster/install-cluster.sh`, `scripts/cluster/upgrade-cluster.sh`, `scripts/cluster/adguardhome-cluster.service`, `scripts/cluster/build-release.sh`
- Modify: `docs/cluster-acceptance.md`

**Interfaces:**
- `build-release.sh` produces `dist/AdGuardHome-cluster-linux-amd64.tar.gz` and SHA-256 sums.
- Upgrade requires an explicit installation path, backs up configuration/data, verifies the binary, installs a systemd unit, and prints rollback instructions.

- [ ] **Step 1: Test scripts with `bash -n`** and build with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`.
- [ ] **Step 2: Inspect the archive** for binary, license, examples, service, and scripts.
- [ ] **Step 3: Probe both supplied servers using existing SSH aliases only**, without changing authentication or deploying when no authenticated alias exists.
- [ ] **Step 4: If authenticated access exists**, back up, deploy master/member configuration, restart, and repeat the acceptance checks; otherwise record the exact external blocker and leave production untouched.
- [ ] **Step 5: Push the reviewed feature branch**, without merging or deleting branches, and commit `build: add cluster release packaging`.
