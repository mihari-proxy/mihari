#!/bin/bash
# Deliberately unavailable as a workstation test convenience command.
set -eu
if [ "$(id -u)" != 0 ] || [ "${CI-}" != true ] || [ "${MIHARI_ISOLATED_SECURITY_CI-}" != 1 ]; then
  echo 'isolated ephemeral security CI required' >&2
  exit 2
fi
case "${MIHARI_SECURITY_PYTHON-}" in /*) ;; *) exit 2;; esac
exec "$MIHARI_SECURITY_PYTHON" "$(dirname "$0")/unix_security_host.py" run "$@"
