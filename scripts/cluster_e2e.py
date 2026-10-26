#!/usr/bin/env python3
"""Run an isolated two-node AdGuard Home cluster acceptance test."""

import argparse
import datetime
import http.cookiejar
import json
import secrets
import socket
import struct
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path


MASTER = "master"
MEMBER = "member"
DAY_MS = 24 * 60 * 60 * 1000


def free_port(used, sock_type=socket.SOCK_STREAM):
    while True:
        with socket.socket(socket.AF_INET, sock_type) as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        if port not in used:
            used.add(port)
            return port


def free_dns_port(used):
    while True:
        port = free_port(used)
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                sock.bind(("127.0.0.1", port))
            return port
        except OSError:
            pass


def decode_body(body):
    if not body:
        return None
    try:
        return json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return body.decode("utf-8", errors="replace")


class API:
    def __init__(self, base):
        self.base = base
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

    def raw(self, method, path, body=None, token=None):
        headers = {"Accept": "application/json"}
        data = None
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if token is not None:
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, decode_body(response.read())
        except urllib.error.HTTPError as error:
            return error.code, decode_body(error.read())

    def call(self, method, path, body=None, expected=200, token=None):
        status, data = self.raw(method, path, body, token)
        expected_statuses = (expected,) if isinstance(expected, int) else tuple(expected)
        if status not in expected_statuses:
            raise RuntimeError(f"{method} {path}: expected {expected_statuses}, got {status}: {data}")
        return data

    def login(self, username, password):
        self.call("POST", "/control/login", {"name": username, "password": password})


class Node:
    def __init__(self, repo, binary, root, name, web_port, dns_port, cluster_port):
        self.repo = repo
        self.binary = binary
        self.root = root
        self.name = name
        self.web_port = web_port
        self.dns_port = dns_port
        self.cluster_port = cluster_port
        self.web_base = f"http://127.0.0.1:{web_port}"
        self.cluster_base = f"http://127.0.0.1:{cluster_port}"
        self.process = None
        self.log_handle = None

    def start(self):
        self.root.mkdir(parents=True, exist_ok=True)
        self.log_handle = (self.root / "server.log").open("ab")
        arguments = [
            str(self.binary),
            "--config",
            str(self.root / "AdGuardHome.yaml"),
            "--work-dir",
            str(self.root),
            "--web-addr",
            f"127.0.0.1:{self.web_port}",
            "--no-check-update",
            "--no-permcheck",
        ]
        self.process = subprocess.Popen(
            arguments,
            cwd=self.repo,
            stdout=self.log_handle,
            stderr=subprocess.STDOUT,
        )

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=10)
        if self.log_handle is not None:
            self.log_handle.close()
            self.log_handle = None

    def write_cluster_config(self, token, member_token=None, member_base=None):
        lines = [
            "cluster:",
            "  enabled: true",
            f"  mode: {self.name}",
            f"  name: {self.name}",
            f"  listen: 127.0.0.1:{self.cluster_port}",
            f"  api_base: {self.cluster_base}",
            f"  token: {token}",
            "  sync:",
            "    enabled: true",
            "    interval: 2s",
            "    on_change: broadcast",
            "    conflict: master_wins",
            "  stats:",
            "    window: min",
            "  degradation:",
            "    skip_offline: true",
        ]
        if member_token is not None:
            lines.extend(
                [
                    "  members:",
                    f"    - name: {MEMBER}",
                    f"      api_base: {member_base}",
                    f"      token: {member_token}",
                    "      timeout: 2s",
                    "      timezone: UTC",
                ]
            )
        else:
            lines.append("  members: []")
        (self.root / "cluster.yaml").write_text("\n".join(lines) + "\n", encoding="utf-8")


def wait_for(description, predicate, timeout=30):
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        try:
            result = predicate()
            if result:
                return result
        except (OSError, RuntimeError) as error:
            last_error = error
        time.sleep(0.25)
    detail = f": {last_error}" if last_error else ""
    raise TimeoutError(f"timed out waiting for {description}{detail}")


def install(node, username, password):
    node.start()
    api = API(node.web_base)
    wait_for(
        f"{node.name} installation API",
        lambda: api.call("GET", "/control/install/get_addresses"),
    )
    api.call(
        "POST",
        "/control/install/configure",
        {
            "username": username,
            "password": password,
            "language": "en",
            "web": {"ip": "127.0.0.1", "port": node.web_port},
            "dns": {"ip": "127.0.0.1", "port": node.dns_port},
        },
    )
    return api


def dns_exchange(port, label):
    transaction_id = secrets.randbelow(65536)
    domain = f"{label}.cluster-e2e.test"
    question = b"".join(bytes([len(part)]) + part.encode("ascii") for part in domain.split(".")) + b"\0"
    packet = struct.pack("!HHHHHH", transaction_id, 0x0100, 1, 0, 0, 0) + question + struct.pack("!HH", 1, 1)
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.settimeout(5)
        sock.sendto(packet, ("127.0.0.1", port))
        response, _ = sock.recvfrom(4096)
    if len(response) < 12 or struct.unpack("!H", response[:2])[0] != transaction_id:
        raise RuntimeError("invalid DNS response")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--work-dir", type=Path, required=True)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[1]
    binary = args.binary.resolve()
    root = args.work_dir.resolve()
    root.mkdir(parents=True, exist_ok=False)

    used_ports = set()
    master = Node(
        repo,
        binary,
        root / MASTER,
        MASTER,
        free_port(used_ports),
        free_dns_port(used_ports),
        free_port(used_ports),
    )
    member = Node(
        repo,
        binary,
        root / MEMBER,
        MEMBER,
        free_port(used_ports),
        free_dns_port(used_ports),
        free_port(used_ports),
    )
    nodes = [master, member]
    username = "cluster-e2e-admin"
    password = secrets.token_urlsafe(24)
    master_token = secrets.token_urlsafe(32)
    member_token = secrets.token_urlsafe(32)
    rule = "||cluster-e2e.test^"
    offline_rule = "||offline-change.cluster-e2e.test^"
    checks = []
    report = {
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "checks": checks,
    }

    def mark(name, **details):
        entry = {"name": name, "result": "PASS"}
        if details:
            entry["details"] = details
        checks.append(entry)
        print("PASS:", name, flush=True)

    try:
        master_api = install(master, username, password)
        member_api = install(member, username, password)
        mark("isolated installation of two real AGH instances")

        master.stop()
        member.stop()
        master.write_cluster_config(master_token)
        member.write_cluster_config(member_token)
        member.start()
        master.start()
        wait_for("member web restart", lambda: member_api.call("GET", "/login.html"))
        wait_for("master web restart", lambda: master_api.call("GET", "/login.html"))

        unauthenticated = API(master.web_base)
        status, _ = unauthenticated.raw("GET", "/control/cluster/ui/status")
        if status != 401:
            raise RuntimeError(f"unauthenticated cluster status returned {status}, expected 401")
        mark("cluster management rejects unauthenticated requests")

        master_api.login(username, password)
        member_api.login(username, password)
        node_api = API(member.cluster_base)
        status, _ = node_api.raw("GET", "/control/cluster/ping")
        if status != 401:
            raise RuntimeError(f"unauthenticated node ping returned {status}, expected 401")
        pong = node_api.call("GET", "/control/cluster/ping", token=member_token)
        if not pong.get("pong") or pong.get("name") != MEMBER:
            raise RuntimeError("member node ping returned an invalid identity")
        mark("real session login and independent Bearer Token authentication")

        master_api.call(
            "POST",
            "/control/cluster/ui/register",
            {
                "name": MEMBER,
                "api_base": member.cluster_base,
                "token": member_token,
                "timeout": "2s",
                "timezone": "UTC",
            },
        )

        def healthy_status():
            current = master_api.call("GET", "/control/cluster/ui/status")
            states = {node["name"]: node for node in current["nodes"]}
            if len(states) != 2 or not all(node["online"] for node in states.values()):
                return None
            if any(not node["version"] for node in states.values()):
                return None
            if any(node["last_heartbeat"].startswith("0001-") for node in states.values()):
                return None
            return current

        status = wait_for("two healthy nodes with versions and heartbeats", healthy_status, 40)
        versions = {node["name"]: node["version"] for node in status["nodes"]}
        mark("two nodes online with versions and heartbeats", versions=versions)

        config = status["config"]
        config["sync"]["interval"] = "1s"
        master_api.call(
            "PUT",
            "/control/cluster/ui/settings",
            {
                "sync": config["sync"],
                "stats": config["stats"],
                "degradation": config["degradation"],
            },
        )
        mark("cluster settings update through authenticated management API")

        for node_name, interval in ((MASTER, 7 * DAY_MS), (MEMBER, DAY_MS)):
            stats_config = master_api.call("GET", f"/control/stats/config?agh_node={node_name}")
            stats_config["enabled"] = True
            stats_config["interval"] = interval
            master_api.call("PUT", f"/control/stats/config/update?agh_node={node_name}", stats_config)
        aggregate_config = master_api.call("GET", "/control/stats/config")
        if aggregate_config.get("interval") != DAY_MS:
            raise RuntimeError("aggregate statistics did not select the 24-hour common window")
        mark("master 7-day and member 24-hour retention use the common window")

        master_api.call("POST", "/control/filtering/set_rules", {"rules": [rule]})
        wait_for(
            "custom filtering rule broadcast",
            lambda: rule in master_api.call("GET", f"/control/filtering/status?agh_node={MEMBER}").get("user_rules", []),
            20,
        )
        dns_config = master_api.call("GET", "/control/dns_info")
        cache_size = int(dns_config["cache_size"]) + 4096
        master_api.call("POST", "/control/dns_config", {"cache_size": cache_size})
        wait_for(
            "DNS configuration broadcast",
            lambda: master_api.call("GET", f"/control/dns_info?agh_node={MEMBER}").get("cache_size") == cache_size,
            20,
        )
        mark("custom filtering rules and DNS configuration synchronize to the member")

        def selected_stats(node_name):
            return master_api.call("GET", f"/control/stats?agh_node={node_name}")

        baseline = {name: selected_stats(name)["num_dns_queries"] for name in (MASTER, MEMBER)}
        for index in range(3):
            dns_exchange(master.dns_port, f"master-{index}")
        for index in range(2):
            dns_exchange(member.dns_port, f"member-{index}")

        def queries_recorded():
            current = {name: selected_stats(name)["num_dns_queries"] for name in (MASTER, MEMBER)}
            return current if current[MASTER] == baseline[MASTER] + 3 and current[MEMBER] == baseline[MEMBER] + 2 else None

        direct = wait_for("five DNS queries in per-node statistics", queries_recorded, 30)
        aggregate = master_api.call("GET", "/control/stats")
        if aggregate["num_dns_queries"] != direct[MASTER] + direct[MEMBER]:
            raise RuntimeError("aggregate DNS query count is not the exact per-node sum")
        if aggregate.get("num_nodes_ok") != 2 or aggregate.get("degraded_nodes"):
            raise RuntimeError("healthy aggregate response has incorrect cluster metadata")
        if len(aggregate.get("dns_queries", [])) != len(selected_stats(MEMBER).get("dns_queries", [])):
            raise RuntimeError("aggregate series did not use the member's 24-hour window")
        mark("statistics are exact sums with a common series window", master=direct[MASTER], member=direct[MEMBER])

        def query_logs_merged():
            response = master_api.call("GET", "/control/querylog?limit=100")
            sources = {entry.get("agh_node") for entry in response.get("data", [])}
            return response if {MASTER, MEMBER}.issubset(sources) else None

        wait_for("query log entries from both nodes", query_logs_merged, 20)
        mark("query logs merge with source-node annotations")

        master.stop()
        member.stop()
        member.start()
        master.start()
        wait_for("member persistence restart", lambda: member_api.call("GET", "/login.html"))
        wait_for("master persistence restart", lambda: master_api.call("GET", "/login.html"))
        master_api.login(username, password)
        member_api.login(username, password)
        status = wait_for("registered member after restart", healthy_status, 40)
        if len(status["config"]["members"]) != 1:
            raise RuntimeError("registered member was not persisted")
        if rule not in master_api.call("GET", f"/control/filtering/status?agh_node={MEMBER}").get("user_rules", []):
            raise RuntimeError("custom rule was not persisted")
        if master_api.call("GET", f"/control/dns_info?agh_node={MEMBER}").get("cache_size") != cache_size:
            raise RuntimeError("DNS configuration was not persisted")
        mark("cluster membership and synchronized configuration survive restart")

        member.stop()
        offline_started = time.monotonic()

        def member_offline():
            current = master_api.call("GET", "/control/cluster/ui/status")
            state = next(node for node in current["nodes"] if node["name"] == MEMBER)
            return current if not state["online"] and state["degraded"] else None

        status = wait_for("three failed health checks and offline degradation", member_offline, 115)
        offline_seconds = round(time.monotonic() - offline_started, 1)
        if offline_seconds < 45:
            raise RuntimeError(f"member went offline too early after {offline_seconds} seconds")
        mark("member becomes degraded after repeated health failures", seconds=offline_seconds)

        status, error = master_api.raw("GET", f"/control/stats?agh_node={MEMBER}")
        if status != 503 or error.get("error") != "node_offline":
            raise RuntimeError("selected offline node did not return the structured node_offline error")
        degraded_stats = master_api.call("GET", "/control/stats")
        if degraded_stats.get("num_nodes_ok") != 1 or degraded_stats.get("degraded_nodes") != [MEMBER]:
            raise RuntimeError("offline aggregate did not degrade to the healthy master")
        mark("offline reads return partial aggregate results and structured errors")

        master_api.call("POST", "/control/filtering/set_rules", {"rules": [rule, offline_rule]})
        cache_size += 4096
        master_api.call("POST", "/control/dns_config", {"cache_size": cache_size})
        pending_status = master_api.call("GET", "/control/cluster/ui/status")
        pending = next(node for node in pending_status["nodes"] if node["name"] == MEMBER)["pending_sync"]
        if pending < 2:
            raise RuntimeError("offline configuration writes were not queued")
        mark("offline writes remain pending", pending=pending)

        member.start()
        wait_for("member web recovery", lambda: member_api.call("GET", "/login.html"))
        wait_for("member heartbeat recovery", healthy_status, 45)

        def recovered_configuration():
            filtering = master_api.call("GET", f"/control/filtering/status?agh_node={MEMBER}")
            dns_info = master_api.call("GET", f"/control/dns_info?agh_node={MEMBER}")
            current = master_api.call("GET", "/control/cluster/ui/status")
            state = next(node for node in current["nodes"] if node["name"] == MEMBER)
            recovered = offline_rule in filtering.get("user_rules", []) and dns_info.get("cache_size") == cache_size
            return current if recovered and state["pending_sync"] == 0 and not state["degraded"] else None

        wait_for("automatic alignment and pending-queue recovery", recovered_configuration, 60)
        mark("recovery triggers alignment and clears pending synchronization")

        audit = master_api.call("GET", "/control/cluster/ui/audit")
        audit_text = json.dumps(audit, ensure_ascii=False)
        forbidden = [master_token, member_token, password, rule, offline_rule, "Authorization", '"body"']
        leaked = [index for index, value in enumerate(forbidden) if value in audit_text]
        if leaked:
            raise RuntimeError(f"audit contains forbidden sensitive fields at indexes {leaked}")
        if not audit.get("entries"):
            raise RuntimeError("cluster audit is empty")
        mark("audit records operations without tokens, rule bodies, or request bodies")

        master_api.call("POST", "/control/cluster/ui/unregister", {"name": MEMBER})
        status = master_api.call("GET", "/control/cluster/ui/status")
        if any(node["name"] == MEMBER for node in status["nodes"]):
            raise RuntimeError("member remains registered after unregistration")
        mark("member unregistration through the real management API")

        report["result"] = "PASS"
    except Exception as error:
        report["result"] = "FAIL"
        report["failure"] = str(error)
        raise
    finally:
        for node in reversed(nodes):
            node.stop()
        report["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        report["processes_stopped"] = all(node.process is None or node.process.poll() is not None for node in nodes)
        (root / "acceptance.json").write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        print(f"Acceptance report: {root / 'acceptance.json'}", flush=True)


if __name__ == "__main__":
    main()
