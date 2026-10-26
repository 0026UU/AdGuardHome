# AdGuard Home 原生集群版验收记录

验收时间：2026-10-26。代码基线：`71edcc505341752a34aafc5792958dd6e1eb9d9c`；本次验收构建版本：`v0.0.0-cluster-e2e`。

本表严格区分“已实测通过”“部分覆盖”和“未满足”。原始双进程报告保存在 [cluster-acceptance.json](cluster-acceptance.json)。报告中的 16 项检查均为 PASS，且 `processes_stopped` 为 `true`；这不等于任务书 10 项整体验收全部通过。

## 1. 任务书逐项验收

| # | 验收项 | 结果 | 实测证据与限制 |
| --- | --- | --- | --- |
| 1 | 主控 UI 显示双节点在线、版本和 RTT | **FAIL（证据不完整）** | 真实 API 已确认两个节点在线、版本和心跳有效，前端组件包含 RTT 列；本次没有留存主控 UI 桌面截图，也没有在浏览器中逐项核对 RTT。 |
| 2 | 成员离线、降级、恢复和自动对齐 | **FAIL（时限未达标）** | 停止成员后，连续健康检查失败触发离线，聚合保留主节点并返回 `node_offline`；恢复后自动对齐、清空 2 项 pending。离线实测 **89.6 秒**，超过任务书 30～60 秒上限；实现采用每 30 秒检查、连续 3 次失败，实际最坏窗口约为 90 秒。 |
| 3 | 聚合统计与双机直连精确对账 | **PASS** | 分别产生主节点 3 次、成员 2 次 DNS 查询，聚合 `num_dns_queries` 与两者之和完全相等，健康响应 `num_nodes_ok=2`。 |
| 4 | 任意节点规则新增和删除在 30 秒内同步 | **FAIL（覆盖不完整）** | 真实双实例已验证主节点新增自定义规则并同步成员；没有在真实实例上完成规则删除以及从成员直接写入的新增/删除全流程。相关写广播有单元测试，但不替代此项真实验收。 |
| 5 | DNS 配置同步并在双双重启后保留 | **PASS** | 修改 DNS 缓存配置后成员同步成功；双进程重启后，集群成员关系、自定义规则和 DNS 配置仍然存在。 |
| 6 | 7 天与 24 小时统计周期使用公共窗口 | **PASS** | 主节点设置为 7 天、成员为 24 小时，聚合配置返回 24 小时；聚合序列长度与成员窗口一致，避免按 7 天请求成员导致 400。 |
| 7 | 桌面与手机浏览器浅色布局 | **FAIL（未执行浏览器验收）** | 响应式 CSS、TypeScript、96 项前端测试和生产构建通过，但没有桌面/手机视口截图，不能据此判定整项通过。 |
| 8 | 无 `cluster.yaml` 时保持官方单机行为 | **FAIL（证据不完整）** | 缺少配置文件返回禁用默认值、禁用中间件直接调用原处理器有单元测试；真实进程也先以无集群配置完成安装。尚未逐页执行官方前端完整回归，因此不把整项记为通过。 |
| 9 | Go 静态检查、构建和前端生产构建 | **PASS** | `go vet ./...`、`go build ./...`、`npm run build-prod` 均通过。生产前端为 React 16 + Webpack，使用本基线实际命令而非任务书中假设的 Vue/Vite 命令。 |
| 10 | 独立集群节点 API 无 Token 返回 401 | **PASS** | 独立节点监听器在路由前统一校验 Bearer Token；真实请求已确认无 Token 返回 401、有效 Token 返回正确节点身份。浏览器管理接口 `/control/cluster/ui/*` 复用 AGH 会话鉴权，未登录同样返回 401。 |

## 2. 最终验证命令

在 Windows amd64、Go 1.27.2、`CGO_ENABLED=0` 环境执行：

```bash
go vet ./...
go build ./...
go test -count=1 ./...
go test -count=1 -skip '^TestConfigFilePath$' ./...

cd client
npm test
npm run typecheck
npm run lint
npm run build-prod
```

结果：

- `go vet ./...` 和 `go build ./...`：PASS。
- `go test -count=1 ./...`：**FAIL**。唯一失败是既有 `internal/home.TestConfigFilePath` 在创建符号链接时收到 Windows `A required privilege is not held by the client`。
- 跳过该符号链接用例后，其余全包测试：PASS。
- 前端：6 个测试文件、96 项测试全部 PASS；类型检查、lint 和生产构建 PASS。
- 本机没有 gcc/clang 且 CGO 关闭，未执行竞态测试，不能声称 `go test -race` 通过。

## 3. 国际化审计

使用 locale-guard 审计脚本扫描 `client/src` 的 212 个 TypeScript/JavaScript 文件：

- 使用中的静态翻译键：444。
- 英文和简体中文 Missing：均为 0；Parity missing：均为 0。
- 两种语言各报告 439 个 Unused，主要来自脚本无法识别的动态键和既有语言资源，不作为失败项。
- `cluster.*` 键：英文 123、简体中文 123，集合一致，占位符差异为 0。
- 两个 JSON 文件均通过 `python -m json.tool`。
- 新增集群界面没有复数翻译键，因此没有新增 `_one`/`_other` 复数样例需要实测。

## 4. 环境说明

验收日志包含默认过滤器访问外部 DNS 或下载列表超时的错误，这是测试环境的外网问题；上述报告只证明列出的检查通过，不表示日志完全无错误。真实服务器替换、线上 DNS 中断窗口、公网 HTTPS 入口和完整回滚尚未验证。
