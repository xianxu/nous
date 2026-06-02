#!/usr/bin/env bash
# vm-hook (nous#36) — make the headless tart VM GPG-unattended for brain
# testing. Runs on every boot via ariadne#59's vm-hooks.d convention
# (invoked as `bash <hook> <repo>`). Delegates to the idempotent
# scripts/brain-vm-setup.sh, resolved relative to this hook's location
# (<repo>/.tart/vm-hooks.d/ → <repo>/scripts/).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$here/../../scripts/brain-vm-setup.sh" "$@"
