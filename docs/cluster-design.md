# AdGuard Home cluster design

Status: approved for continuous implementation by the task owner on 2026-10-26.

Baseline: upstream `master` commit
`71edcc505341752a34aafc5792958dd6e1eb9d9c`.  GPL-3.0, license notices, and
copyright headers remain intact.

## 1. Goals and compatibility

The default AGH binary gains an optional flat cluster.  Every node remains a
fully functional AGH instance.  Without `cluster.yaml`, or with
`cluster.enabled: false`, the request path is the original handler with no
aggregation, broadcast, extra listener, or background health loop.

The implementation targets the actual baseline: Go 1.27.2 and the default
React/Webpack UI.  It does not modify the frozen filtering or DNS forwarding
engines.  Existing API response fields remain intact; aggregate responses only
add metadata such as `agh_node`, `node_list`, `num_nodes_ok`, and
`degraded_nodes`.

## 2. Alternatives and decision

1. **Selected: additive package plus HTTP middleware and a node listener.**
   Existing handlers stay unchanged.  The cluster layer buffers a local request,
   invokes the original mux, and merges or broadcasts only registered routes.
2. Per-route registrar wrappers were rejected because they distribute cluster
   concerns through every owning module and make upstream merges harder.
3. An external aggregation proxy was rejected because it cannot provide native
   health, retry, alignment, authentication, or UI state.

## 3. Architecture

```text
Browser (normal AGH session)
        |
        v
AGH auth middleware
        |
        v
Cluster middleware -- disabled --> original mux
        |
        +-- aggregate GET ----+--> local original handler
        |                     +--> remote /control/cluster/read (concurrent)
        |
        +-- mutating request ----> local original handler first
        |                            |
        |                            +--> authorized remote /control/cluster/apply
        |                            +--> retry queue and audit on failure
        |
        +-- /control/cluster/ui/* management endpoints

Node-to-node listener (cluster.listen, HTTPS recommended)
        |
        +-- Bearer token validation (constant-time comparison)
        +-- /control/cluster/ping
        +-- /control/cluster/register
        +-- /control/cluster/read
        +-- /control/cluster/apply
        +-- /control/cluster/align
        +-- /control/cluster/audit
```

The node listener receives an internal dispatch function pointing at the
original mux.  Consequently a remotely applied write executes the full original
validation and persistence logic but does not recursively broadcast.  It does
not need or store the node's web administrator password.

Browser management routes use `/control/cluster/ui/*` on the main Web listener
and the existing AGH session middleware.  The public management names required
by the UI (`status`, `nodes`, `register`, `unregister`, `align`, and `audit`) are
also available there.  The independent node API itself always requires a Bearer
token; an unauthenticated request receives 401.  This separation avoids exposing
cluster tokens to browser JavaScript.

## 4. Configuration

`cluster.yaml` is loaded from the AGH working directory.  Writes use a temporary
file, file sync, and atomic rename with restrictive permissions.  Tokens are
never returned by management APIs or written to audit entries.

```yaml
cluster:
  enabled: true
  mode: master                 # master | member
  name: "阿里云-主"
  listen: "127.0.0.1:8091"
  api_base: "http://127.0.0.1:8091" # this node's cluster API
  token: "replace-with-random-token"
  members:
    - name: "京东云-备"
      api_base: "http://member:8091" # member cluster API
      token: "replace-with-member-token"
      timeout: 5s
      timezone: "Asia/Shanghai"
  sync:
    enabled: true
    interval: 30s
    on_change: broadcast
    conflict: master_wins      # master_wins | reject
  stats:
    window: min                # min | align_to_master
  degradation:
    skip_offline: true
```

Validation rejects unknown modes, invalid listen addresses or URLs, empty tokens
when enabled, duplicate node names, non-positive timeouts, and unsupported
policies.  `listen` is bound explicitly; production deployments should use HTTPS
through the configured AGH TLS environment or a trusted private tunnel.  Plain
HTTP is supported for local integration tests and isolated lab networks.

## 5. Node lifecycle and health

- The local node is always included in status.  Each configured member has name,
  address, online state, last heartbeat, RTT, version, pending-sync state, and a
  sanitized last-error code.
- A heartbeat requests `GET /control/cluster/ping` every 30 seconds.  Three
  consecutive failures mark a node offline.  A successful heartbeat restores it
  immediately and schedules one full alignment.
- Reads use a three-second per-request timeout and skip known-offline nodes when
  degradation is enabled.  Writes use the member timeout (default five seconds).
- Registration adds/updates the member, persists configuration, performs a
  health check, and requests full alignment.  Removal is explicit and audited.
- Shutdown stops tickers, retry workers, and the node HTTP server.

## 6. Read aggregation

All configured healthy nodes, including the local node, are queried
concurrently.  A failed member is added to `degraded_nodes`; it does not fail the
whole request.  If every node fails, the request returns 502 with a stable error
code rather than an empty successful response.

### Rule table

| Endpoint | Merge behavior |
| --- | --- |
| `GET /control/stats` | Sum `num_*`; weighted `avg_processing_time` by DNS query count; right-align and add the shortest common length of time series; sum and rerank top maps; upstream latency uses response-count weights; preserve `time_units`; add `num_nodes_ok` and `degraded_nodes`. |
| `GET /control/stats/config` | Default response interval is the minimum healthy-node interval and other fields come from the local node.  Requests to each node are clamped to that node's accepted interval to prevent 400 responses.  In `align_to_master` mode, alignment propagates the master interval before aggregate reads. |
| `GET /control/querylog` | Request an amplified limit from each node, preserve filters and `older_than`, merge by RFC3339Nano time descending, truncate to the requested limit, annotate `agh_node`, and return the oldest merged cursor. |
| `GET /control/querylog_info`, `/querylog/config` | Local/coordinator response. |
| `GET /control/clients` | Merge persistent clients by name, union IDs/tags/upstreams, attach `node_list`, merge runtime clients by IP/name, and union supported tags. |
| `POST /control/clients/search` | Fan out the unchanged JSON body; accept both an array and legacy `{found: []}`; merge by client name or response key and attach node metadata. |
| `GET /control/clients/find` | Merge compatible array/object responses and attach source metadata. |
| Filtering, access, rewrite, blocked services, safe search, safe browsing, parental, DNS, TLS, DHCP, language, version, and check-host reads | Local/coordinator response; configuration is kept consistent by synchronization.  No invented cross-node merging of singleton settings. |

Single-node selection uses the `agh_node` query parameter.  The middleware strips
that parameter before local dispatch and asks only the selected remote node.
Unknown or offline selected nodes return a stable 400/503 response instead of
silently changing the dataset.

## 7. Stats window invariant

For `window: min`, the aggregate config interval is the minimum positive healthy
member interval.  Each aggregate stats request is rewritten per target to
`min(requestedRecent, targetInterval)` and at least one hour.  This directly
prevents the seven-day-master/24-hour-member failure mode.  Arrays are aligned at
their newest (right-hand) samples before summing.

For `window: align_to_master`, the master stats configuration is included in full
alignment and the same per-node clamp remains as a defensive fallback during
propagation or partial failure.

## 8. Write broadcast and retry

Only registered mutating Control API routes are intercepted.  The body is read
once with a bounded size, restored for the original handler, and captured using a
response recorder.  A local status outside 200–299 is returned unchanged and is
never broadcast.  A successful local write is broadcast asynchronously to every
other node except the originating node.

The apply payload contains the original method, path, query, complete body, and
content type.  It never contains web credentials.  Remote application invokes
the original handler directly and verifies a 2xx result.  Duplicate or repeated
operations may be rejected by upstream validation; alignment is the idempotent
recovery mechanism.

Failures create retry entries with a stable operation ID, target, payload,
attempt count, next-attempt time, and sanitized error code.  The worker retries
every 30 seconds, at most ten times.  Pending nodes are visible in node status.
Exhaustion is audited and remains visible; it is not reported as success.

Conflict policy:

- `master_wins`: full alignment compares canonical JSON values and overwrites
  differences from the master.
- `reject`: alignment reports differences without overwriting them.
- All detected overwrites and failures are audited.  Audit entries contain time,
  operation, source, target, result, and a sanitized message; secrets and raw
  request bodies are excluded.

Because the transport applies writes locally first, a partial cluster failure is
represented explicitly as degraded/pending state.  The design does not claim
atomic distributed transactions.

## 9. Full alignment

Alignment runs after registration, recovery, and a manual request.  A table maps
each readable configuration endpoint to its write endpoint and method.  For each
entry:

1. Read the complete master and target payloads through local dispatch and the
   authorized node read API.
2. Canonically compare JSON while ignoring known node-local fields.
3. Produce `{field, old, new}` diff metadata, redacting sensitive values.
4. Apply the complete master payload using the mapped write route unless the
   policy is `reject`.
5. Return counts for succeeded, overwritten, failed, and skipped items.

Initial mappings cover profile/protection, DNS, query log, stats, rewrites,
filters/user rules, blocked services, clients, access lists, safe search, safe
browsing, parental, DHCP configuration/static leases, and TLS.  Rewrite/client
collections use their native add/update/delete operations when a single PUT is
not available.  DHCP interface names and enabled state can remain node-local;
DNS traffic and DHCP service are never proxied.

## 10. Frontend

The default React UI adds:

- `#/cluster/nodes`: node cards/table, health, RTT, version, heartbeat, pending
  state, registration and deletion.
- `#/cluster/settings`: synchronization, conflict, stats-window, degradation,
  and manual-alignment controls.
- Header navigation entries for Nodes and Cluster Settings.
- Dashboard `NodeSwitcher`: aggregate or individual node; selection is stored in
  Redux and localStorage.
- `NodeStatusBadge`, `DegradedBanner`, and `ClusterSettingsForm` components.
- Ten-second status polling while cluster pages/dashboard are visible.
- Responsive vertical cards on narrow screens and the existing light theme.

All new visible strings use i18next keys.  English and Simplified Chinese are
complete; other existing languages use the established English fallback.  Error
toasts map stable backend error codes to localized messages.  Raw `error.message`
is logged for developers but not used as the primary UI error.

## 11. Testing and acceptance mapping

- Config unit tests: missing file, disabled mode, valid config, invalid tokens,
  URLs, modes, durations, and atomic save.
- Health tests: three-failure offline transition, recovery, RTT/version updates.
- Merge tests: exact totals, weighted averages, series right-alignment, top-10
  ordering, upstream weighting, common stats window, query-log sort/limit/source,
  client/search compatibility.
- Sync tests: local failure does not broadcast, complete payload preservation,
  source exclusion, retries, retry exhaustion, and audit redaction.
- API tests: missing/wrong Bearer token returns 401; malformed internal paths are
  rejected; single-node mode remains unchanged.
- Frontend: typecheck, unit tests, production build, and i18n key consistency.
- Integration: two local AGH instances with distinct work directories and ports;
  node health, outage/degradation/recovery, stats reconciliation, rule and DNS
  synchronization, restart persistence, mobile/desktop UI, and token checks.
- Release: `go test` for cluster packages, `go vet ./...`, `go build ./...`,
  frontend `npm ci`, tests/typecheck/build, Linux amd64 binary, install/upgrade
  scripts, and systemd unit.

Real-server replacement is performed only after local verification, with a
backup and rollback path.  Server SSH credentials and host reachability are
external prerequisites, not embedded in the repository.
