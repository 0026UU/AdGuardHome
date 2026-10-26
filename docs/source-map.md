# AdGuard Home source map

Baseline: `71edcc505341752a34aafc5792958dd6e1eb9d9c` (`upstream/master`, 2026-10-23).

## Build and entry points

- `main.go` (`!next`) embeds `build/static` and calls `internal/home.Main`.
- `main_next.go` (`next`) starts the separate `internal/next` implementation.  The
  cluster work targets the default, non-`next` binary.
- `internal/home/home.go`: process initialization, configuration parsing,
  module construction, HTTP registrar construction, DNS startup, and Web startup.
- `go.mod`: Go 1.27.2 at the baseline.
- `client/package.json`: React 16, Redux, Webpack; production build is
  `npm run build-prod`.
- `client_v2/`: SolidJS client for the separate next-generation stack.  It is
  not the default UI embedded by the non-`next` binary.

The task prompt's `internal/control/` directory and Vue frontend do not exist at
this baseline.  Control handlers are distributed across their owning modules.

## HTTP API registration

- `internal/aghhttp/registrar.go`: `Registrar` and `DefaultRegistrar`; routes
  are registered on a `http.ServeMux` through `webMw.wrap`.
- `internal/home/control.go`: core status, TLS, version, profile, language, and
  authentication routes.  `webMw.ensure` enforces methods, JSON content types,
  and the global write lock.
- `internal/home/authhttp.go`: login/logout and session authentication.
- `internal/home/web.go`: static UI, install routes, control registration,
  authentication middleware, HTTP/HTTPS servers, and shutdown.
- `internal/stats/http.go`: `/control/stats`, `/control/stats/config`,
  `/control/stats/config/update`, reset, and deprecated stats routes.
- `internal/querylog/http.go`: query log search, configuration, clear, and
  deprecated information/configuration routes.
- `internal/filtering/http.go`: filtering, safe browsing, parental, safe search,
  blocked services, and rewrite routes.
- `internal/home/clientshttp.go`: persistent/runtime clients, search, add,
  update, and delete.
- `internal/dnsforward/http.go`: DNS configuration, protection, access lists,
  cache clearing, and upstream tests.
- `internal/dhcpd/http_unix.go` and `http_windows.go`: DHCP routes and platform
  fallbacks.

## Configuration and persistence

- `internal/home/config.go`: the legacy YAML configuration model and defaults.
- `internal/configmgr/`: newer typed configuration and migration-facing models.
- `internal/configmigrate/`: schema migrations for `AdGuardHome.yaml`.
- `internal/home/config.go` and the default config modifier in `internal/home`:
  atomic configuration writes after module changes.
- Cluster metadata deliberately lives in a separate `cluster.yaml`; the upstream
  schema and migration chain are untouched.

## Data semantics needed by aggregation

- `internal/stats/http.go` defines the stats response.  Upstream response time
  is represented by `top_upstreams_responses`; latency by
  `top_upstreams_avg_time`.  `recent` is milliseconds and must not exceed the
  node's configured retention interval.
- `internal/querylog/json.go` returns `{data, oldest}`.  Entries contain an
  RFC3339Nano `time`; `data` is newest first.
- `internal/home/clientshttp.go` returns `{clients, auto_clients,
  supported_tags}` from `GET /clients`; `POST /clients/search` returns an array
  of single-key objects, not the old `{found: [...]}` wrapper.

## Default frontend

- `client/src/index.tsx`: UI bootstrap.
- `client/src/components/App/index.tsx`: `HashRouter` and top-level routes.
- `client/src/helpers/constants.ts`: route constants and shared UI constants.
- `client/src/components/Header/Menu.tsx`: navigation and settings dropdown.
- `client/src/configureStore.ts` and `client/src/reducers/index.ts`: Redux store.
- `client/src/api/Api.ts` and `client/src/api/fetch.ts`: Control API client.
- `client/src/actions/stats.ts` and `client/src/reducers/stats.ts`: dashboard
  statistics flow.
- `client/src/components/Dashboard/`: dashboard cards and charts.
- `client/src/i18n.ts` and `client/src/__locales/`: existing i18next setup and
  language resources.  New user-visible strings must at least be synchronized
  between `en.json` and `zh-cn.json`; other languages fall back to English.

## Reference projects reviewed

### bakito/adguardhome-sync

Reviewed locally at `references/adguardhome-sync`, especially
`internal/sync/sync.go`, `action.go`, `action-general.go`, and
`internal/client/client-methods.go`.

Its effective synchronization flow is:

1. Read origin status and the complete supported configuration snapshot.
2. Read the corresponding replica state.
3. Compare typed values.
4. Apply only differing operations, continuing or stopping according to policy.
5. Refresh filters after additions or updates.

Covered configuration groups and their principal APIs:

| Group | Read | Write |
| --- | --- | --- |
| Profile/protection | `status`, `profile` | `profile/update`, `protection` |
| Parental/safe browsing | `parental/status`, `safebrowsing/status` | corresponding `enable`/`disable` |
| Safe search | `safesearch/status` | `safesearch/settings`, `enable`, `disable` |
| DNS server | `dns_info` | `dns_config` |
| Query log | `querylog/config` | `querylog/config/update` |
| Statistics | `stats/config` | `stats/config/update` |
| Rewrites | `rewrite/list`, `rewrite/settings` | `rewrite/add`, `delete`, `update`, `settings/update` |
| Filters and user rules | `filtering/status` | `filtering/add_url`, `remove_url`, `set_url`, `set_rules`, `refresh`, `config` |
| Blocked services | `blocked_services/get` | `blocked_services/update` (legacy `set` remains supported) |
| Clients | `clients` | `clients/add`, `update`, `delete` |
| Access lists | `access/list` | `access/set` |
| DHCP | `dhcp/status` | `dhcp/set_config`, static lease add/update/delete |
| TLS | `tls/status` | `tls/configure` |

Useful ideas retained: startup full alignment, deterministic operation ordering,
field-level diffing, complete payloads, per-node failure isolation, and an
origin-wins default.  No source code is copied.

### linuxserver/docker-adguardhome-sync

Reviewed `README.md` and container layout.  Useful deployment ideas are explicit
configuration paths, timezone configuration, persistent configuration volumes,
non-root/read-only operation, and secrets through environment-file indirection.
The cluster implementation remains in AGH rather than adding this container.

### Existing agh-proxy semantics

The source proxy was not included with the task attachment.  The detailed merge
and write semantics in the task document are therefore the authoritative
functional comparison checklist; no unobserved proxy implementation details are
claimed.
