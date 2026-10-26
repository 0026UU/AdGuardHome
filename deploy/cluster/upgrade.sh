#!/usr/bin/env bash

# Upgrade an existing installation while preserving a local rollback copy.
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
    printf 'upgrade: %s\n' "$*" >&2
    exit 1
}

[[ "${EUID}" -eq 0 ]] || fail 'run as root (for example, with sudo)'
[[ "$(uname -s)" == 'Linux' ]] || fail 'this upgrader only supports Linux'
[[ -f "${PACKAGE_BINARY}" ]] || fail "missing release binary: ${PACKAGE_BINARY}"
[[ -f "${INSTALL_DIR}/AdGuardHome" ]] || fail "no existing installation in ${INSTALL_DIR}"

for command_name in cp date install systemctl; do
    command -v "${command_name}" >/dev/null 2>&1 || fail "missing command: ${command_name}"
done

"${PACKAGE_BINARY}" --version >/dev/null || fail 'the release binary cannot run on this host'

BACKUP_DIR="${INSTALL_DIR}/backups/$(date -u +%Y%m%dT%H%M%SZ)"
readonly BACKUP_DIR
install -d -m 0700 "${BACKUP_DIR}"

# Statistics databases and other data are deliberately not copied.  Back up the
# complete installation separately before a production migration.
for file_name in AdGuardHome AdGuardHome.yaml cluster.yaml; do
    if [[ -f "${INSTALL_DIR}/${file_name}" ]]; then
        cp -a -- "${INSTALL_DIR}/${file_name}" "${BACKUP_DIR}/${file_name}"
    fi
done
if [[ -f "${UNIT_PATH}" ]]; then
    cp -a -- "${UNIT_PATH}" "${BACKUP_DIR}/AdGuardHome.service"
fi
printf 'Rollback files: %s\n' "${BACKUP_DIR}"

restore_backup() {
    printf 'Restoring the previous binary and configuration...\n' >&2
    cp -a -- "${BACKUP_DIR}/AdGuardHome" "${INSTALL_DIR}/AdGuardHome"
    for file_name in AdGuardHome.yaml cluster.yaml; do
        if [[ -f "${BACKUP_DIR}/${file_name}" ]]; then
            cp -a -- "${BACKUP_DIR}/${file_name}" "${INSTALL_DIR}/${file_name}"
        fi
    done
    if [[ -f "${BACKUP_DIR}/AdGuardHome.service" ]]; then
        cp -a -- "${BACKUP_DIR}/AdGuardHome.service" "${UNIT_PATH}"
    fi
    systemctl daemon-reload
}

if systemctl is-active --quiet "${SERVICE_NAME}"; then
    systemctl stop "${SERVICE_NAME}"
fi

install -m 0755 "${PACKAGE_BINARY}" "${INSTALL_DIR}/AdGuardHome"
install -m 0644 "${SCRIPT_DIR}/adguardhome-cluster.service" "${UNIT_PATH}"
systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"

if ! systemctl start "${SERVICE_NAME}"; then
    systemctl status "${SERVICE_NAME}" --no-pager >&2 || true
    restore_backup
    if systemctl start "${SERVICE_NAME}"; then
        fail 'the upgrade failed; the previous binary and configuration were restored'
    fi
    fail 'the upgrade and automatic rollback both failed; restore the complete external backup'
fi

printf 'Upgraded to %s\n' "$("${INSTALL_DIR}/AdGuardHome" --version)"
printf 'Previous binary and configuration: %s\n' "${BACKUP_DIR}"
