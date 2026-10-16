#!/bin/sh
# Register GNSSAgent-CCU on SysV x86-64 (chkconfig).
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
INSTALL_PATH=/home/TEDS/GNSSAgent
INIT_DST=/etc/init.d/gnssagent
START_NOW=0
for ARG in "$@"; do
    case "$ARG" in
        sysv) ;;
        --start) START_NOW=1;;
        *) echo "Usage: $0 [sysv] [--start]" >&2; exit 2;;
    esac
done
[ "$(id -u)" -eq 0 ] || { echo 'Please run as root' >&2; exit 1; }
case "$(uname -m)" in x86_64|amd64) ;;
    *) echo 'Only x86-64 is supported' >&2; exit 1;;
esac
for FILE in GNSSAgent-CCU gnssagentctl.sh start.sh stop.sh restart.sh status.sh; do
    [ -f "$INSTALL_PATH/$FILE" ] || { echo "Missing deployment file: $INSTALL_PATH/$FILE" >&2; exit 1; }
done
[ -f "$SCRIPT_DIR/gnssagent.init.d" ] || { echo 'Missing gnssagent.init.d template' >&2; exit 1; }
for TOOL in chkconfig flock readlink awk nohup sed grep chmod mv; do
    command -v "$TOOL" >/dev/null 2>&1 || { echo "Required command missing: $TOOL" >&2; exit 1; }
done
[ -d /var/lock/subsys ] || { echo 'Missing SysV lock directory /var/lock/subsys' >&2; exit 1; }
if [ -e "$INIT_DST" ] || [ -L "$INIT_DST" ]; then
    [ -f "$INIT_DST" ] && [ ! -L "$INIT_DST" ] && grep -qFx '# Managed by GNSSAgent install.sh.' "$INIT_DST" ||
        { echo "Existing service entry is not managed by GNSSAgent: $INIT_DST" >&2; exit 1; }
fi
for FILE in GNSSAgent-CCU gnssagentctl.sh start.sh stop.sh restart.sh status.sh; do
    chmod +x "$INSTALL_PATH/$FILE"
done
INIT_TMP="/etc/init.d/.gnssagent-install.$$"
trap 'rm -f -- "$INIT_TMP"' 0
trap 'exit 1' 1 2 15
sed "s|@GNSSAGENT_HOME@|$INSTALL_PATH|g" "$SCRIPT_DIR/gnssagent.init.d" > "$INIT_TMP"
chmod +x "$INIT_TMP"
mv -f -- "$INIT_TMP" "$INIT_DST"
chkconfig --add gnssagent
chkconfig gnssagent on
echo "GNSSAgent auto-start installed: $INIT_DST"
echo "Logs: $INSTALL_PATH/../Logs/GNSSAgent"
if [ "$START_NOW" -eq 0 ]; then
    printf 'Start now? (y/n) '
    if read -r ANSWER; then
        case "$ANSWER" in y|Y|yes|YES) START_NOW=1;; esac
    fi
fi
if [ "$START_NOW" -eq 1 ]; then
    "$INIT_DST" start
    "$INIT_DST" status
fi
