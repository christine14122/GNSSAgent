#!/bin/sh
# Run on Linux with TCP/UDP port 29501 free; pass a Linux amd64 CCU binary.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
BIN=${1:-"$ROOT/release/GNSSAgent-CCU"}
WORK=$(mktemp -d "${TMPDIR:-/tmp}/gnssagent-self-starting.XXXXXX")
cp "$ROOT"/build/dist/install/linux-self-starting/*.sh "$WORK/"
cp "$BIN" "$WORK/GNSSAgent-CCU"
chmod +x "$WORK/GNSSAgent-CCU"
trap 'sh "$WORK/stop.sh"' 0
sh "$WORK/start.sh"
FIRST=$(cat "$WORK/gnssagent.pid")
sh "$WORK/start.sh"
[ "$(cat "$WORK/gnssagent.pid")" = "$FIRST" ]
sh "$WORK/status.sh"
# Empty/missing PID data: stop/status remain conservative; start adopts the live instance.
: > "$WORK/gnssagent.pid"
if sh "$WORK/stop.sh"; then exit 1; else [ "$?" -eq 4 ]; fi
if sh "$WORK/status.sh"; then exit 1; else [ "$?" -eq 4 ]; fi
kill -0 "${FIRST%% *}"
sh "$WORK/start.sh"
[ "$(cat "$WORK/gnssagent.pid")" = "$FIRST" ]
rm -- "$WORK/gnssagent.pid"
if sh "$WORK/stop.sh"; then exit 1; else [ "$?" -eq 4 ]; fi
sh "$WORK/start.sh"
[ "$(cat "$WORK/gnssagent.pid")" = "$FIRST" ]
# Both a previous-boot record and a legacy mismatched token recover the same live process.
printf '%s 1 00000000-0000-0000-0000-000000000000\n' "${FIRST%% *}" > "$WORK/gnssagent.pid"
sh "$WORK/start.sh"
[ "$(cat "$WORK/gnssagent.pid")" = "$FIRST" ]
printf '%s 1\n' "${FIRST%% *}" > "$WORK/gnssagent.pid"
if sh "$WORK/stop.sh"; then exit 1; else [ "$?" -eq 4 ]; fi
sh "$WORK/start.sh"
[ "$(cat "$WORK/gnssagent.pid")" = "$FIRST" ]
sh "$WORK/restart.sh"
[ "$(cat "$WORK/gnssagent.pid")" != "$FIRST" ]
sh "$WORK/stop.sh"
if sh "$WORK/status.sh"; then exit 1; else [ "$?" -eq 3 ]; fi
# An empty deployment PID file with no running process is recoverable.
: > "$WORK/gnssagent.pid"
sh "$WORK/start.sh"
[ -s "$WORK/gnssagent.pid" ]
sh "$WORK/status.sh"
sh "$WORK/stop.sh"
# Invalid PID data must be rejected without signalling a process.
printf '1 1\n' > "$WORK/gnssagent.pid"
if sh "$WORK/stop.sh"; then exit 1; else [ "$?" -eq 4 ]; fi
rm -- "$WORK/gnssagent.pid"
echo "PASS: lifecycle, empty/missing PID recovery, boot/legacy recovery, duplicate start and invalid PID protection ($WORK)"
