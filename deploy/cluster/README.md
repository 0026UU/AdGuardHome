# AdGuard Home 原生集群版部署

本目录用于部署本仓库构建的 Linux amd64 二进制。它不使用官方更新通道，也不会下载或覆盖上游脚本。所有示例地址、名称和 Token 都是占位符，与任何真实服务器无关。

## 1. 发布包内容

压缩包解压后包含：

- `AdGuardHome`：包含最新 React 生产前端的 Linux amd64 可执行文件。
- `LICENSE.txt`：上游 GPL-3.0 许可证。
- `deploy/cluster/`：安装、升级、systemd 单元和双节点示例。
- `docs/`：集群设计与实测验收记录。

先核对 SHA256，再解压：

```bash
sha256sum -c AdGuardHome-cluster-v0.0.0-linux-amd64.tar.gz.sha256
tar -xzf AdGuardHome-cluster-v0.0.0-linux-amd64.tar.gz
cd AdGuardHome
```

## 2. 首次安装

安装目录固定为 `/opt/AdGuardHome`，服务名为 `AdGuardHome`。安装器发现已有二进制或同名 systemd 单元时会退出，已有实例必须使用升级脚本。

```bash
sudo bash deploy/cluster/install.sh
sudo systemctl status AdGuardHome --no-pager
```

首次启动后先通过官方安装页面完成 AGH 初始化。没有 `cluster.yaml` 时保持单机模式。

## 3. 升级已有实例

升级器会停止服务，备份二进制、`AdGuardHome.yaml`、`cluster.yaml` 和同名 systemd 单元，再安装新版本并启动。备份位于 `/opt/AdGuardHome/backups/<UTC 时间>/`。如果启动失败，会尝试恢复这些备份。

```bash
sudo bash deploy/cluster/upgrade.sh
sudo systemctl status AdGuardHome --no-pager
journalctl -u AdGuardHome -n 100 --no-pager
```

**该本地备份不包含统计数据库和完整数据目录，也不能保证逆转未来版本的数据迁移。生产替换前必须另外备份整个 `/opt/AdGuardHome`，并先在与线上同版本的测试实例验证回滚。** 本仓库只在 Windows 上做了脚本语法与静态检查，安装和升级脚本尚未在 Linux 服务器执行。

## 4. 配置双节点

1. 为每个节点生成不同的随机 Token，例如 `openssl rand -hex 32`。不要复用 Web 登录密码。
2. 将 `master.cluster.yaml` 和 `member.cluster.yaml` 分别复制到对应节点的 `/opt/AdGuardHome/cluster.yaml`，权限设为 `0600`。
3. 替换节点名称、地址和两个 Token。主节点示例的 `members` 初始为空；成员示例包含主节点反向引用，使成员上的写操作可以广播回主节点。
4. 两台节点都完成配置后，将 `cluster.enabled` 改为 `true`，重启服务。
5. 登录主节点的原有 AGH Web 界面，在“节点”页面注册成员：填写成员的 `api_base` 和成员自己的 Token。不要再次手工把同一成员加入主节点配置。
6. 检查两个节点的版本、心跳、RTT、待同步数量和审计记录，再执行一次手动对齐。

```bash
sudo install -m 0600 deploy/cluster/master.cluster.yaml /opt/AdGuardHome/cluster.yaml
# 在主节点编辑 cluster.yaml 后：
sudo systemctl restart AdGuardHome
```

成员节点使用 `member.cluster.yaml`，操作相同。

### 传输安全

内置节点监听器本身使用 HTTP。示例把它绑定到 `127.0.0.1:8091`，要求在同机 HTTPS 反向代理或可信私有隧道之后提供 `https://…` 地址。不要把明文 `8091` 直接暴露到公网，也不要在不受信的跨公网链路上使用 `http://`。防火墙只需开放实际使用的 Web、DNS 和经过 TLS 终止的集群入口。

## 5. 运维与回滚

```bash
systemctl status AdGuardHome --no-pager
journalctl -u AdGuardHome -f
```

停用集群时，先备份并移除 `cluster.yaml`（或设置 `cluster.enabled: false`），再重启；原有 AGH 配置和数据保留。

升级失败需要手工回滚时，停止服务，将最新时间戳备份目录中的 `AdGuardHome` 和对应配置文件复制回 `/opt/AdGuardHome`，再恢复其中的 `AdGuardHome.service`（如果存在），执行 `systemctl daemon-reload` 并启动。若数据格式已经迁移，必须恢复生产前的完整外部备份，而不能只使用上述文件级回滚。

## 6. 已知验收限制

真实双进程验收的功能报告见 `docs/cluster-acceptance.json`，逐项任务验收表见 `docs/cluster-acceptance.md`。其中离线判定实测为 89.6 秒，不满足任务书的 30～60 秒时限；生产上线仍需可达的 SSH 配置、线上备份和维护窗口。外部过滤器下载在验收环境中出现网络超时，不应把日志描述为完全无错误。
