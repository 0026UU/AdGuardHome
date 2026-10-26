#!/usr/bin/env bash

# Install a fresh cluster-capable AdGuard Home from an unpacked release.
set -Eeuo pipefail

readonly INSTALL_DIR='/opt/AdGuardHome'
readonly SERVICE_NAME='AdGuardHome'
readonly UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
PACKAGE_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
readonly PACKAGE_ROOT
readonly PACKAGE_BINARY="${PACKAGE_ROOT}/AdGuardHome"

fail() {
    printf 'install: %s\n' "$*" >&2
    exit 1
}

[[ "${EUID}" -eq 0 ]] || fail 'run as root (for example, with sudo)'
[[ "$(uname -s)" == 'Linux' ]] || fail 'this installer only supports Linux'
[[ -f "${PACKAGE_BINARY}" ]] || fail "missing release binary: ${PACKAGE_BINARY}"
[[ ! -e "${INSTALL_DIR}/AdGuardHome" && ! -e "${UNIT_PATH}" ]] ||
    fail 'an existing installation was detected; use upgrade.sh instead'

for command_name in install systemctl; do
    command -v "${command_name}" >/dev/null 2>&1 || fail "missing command: ${command_name}"
done

# Check that the binary is executable on this host before changing the service.
"${PACKAGE_BINARY}" --version >/dev/null || fail 'the release binary cannot run on this host'

install -d -m 0755 "${INSTALL_DIR}"
install -m 0755 "${PACKAGE_BINARY}" "${INSTALL_DIR}/AdGuardHome"
if [[ -f "${PACKAGE_ROOT}/LICENSE.txt" ]]; then
    install -m 0644 "${PACKAGE_ROOT}/LICENSE.txt" "${INSTALL_DIR}/LICENSE.txt"
fi
install -m 0644 "${SCRIPT_DIR}/adguardhome-cluster.service" "${UNIT_PATH}"

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"
if ! systemctl start "${SERVICE_NAME}"; then
    systemctl status "${SERVICE_NAME}" --no-pager >&2 || true
    fail 'the service was installed but did not start; no existing files were replaced'
fi

printf 'Installed %s in %s\n' "$("${INSTALL_DIR}/AdGuardHome" --version)" "${INSTALL_DIR}"
printf 'Clustering remains disabled until cluster.yaml is created and enabled.\n'
