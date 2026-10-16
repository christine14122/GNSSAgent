#!/bin/sh
# Run on Linux with TCP/UDP port 29501 free; pass a Linux amd64 CCU binary.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
BIN=${1:-"$ROOT/release/GNSSAgent-CCU"}
WORK=$(mktemp -d /tmp/gnssagent-self-starting.XXXXXX)
cp "$ROOT"/build/dist/install/linux-self-starting/*.sh "$WORK/"
cp "$BIN" "$WORK/GNSSAgent-CCU"
chmod +x "$WORK/GNSSAgent-CCU"
trap 'sh "$WORK/stop.sh"' 0
sh "$WORK/start.sh"
FIRST=$(cat "$WORK/gnssagent.pid")
sh "$WORK/start.sh"
[ "$(cat "$WORK/gnssagent.pid")" = "$FIRST" ]
sh "$WORK/status.sh"
sh "$WORK/restart.sh"
[ "$(cat "$WORK/gnssagent.pid")" != "$FIRST" ]
sh "$WORK/stop.sh"
if sh "$WORK/status.sh"; then exit 1; else [ "$?" -eq 3 ]; fi
# Invalid PID data must be rejected without signalling a process.
printf '1 1\n' > "$WORK/gnssagent.pid"
if sh "$WORK/stop.sh"; then exit 1; else [ "$?" -eq 4 ]; fi
rm -- "$WORK/gnssagent.pid"
echo "PASS: lifecycle, duplicate start, status and invalid PID protection ($WORK)"
