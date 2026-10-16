#!/bin/sh
# Disable auto-start, retaining the program and logs.
set -eu
if [ "$#" -gt 1 ] || [ "${1:-sysv}" != sysv ]; then
    echo "Usage: $0 [sysv]" >&2; exit 2
fi
INSTALL_PATH=/home/TEDS/GNSSAgent
INIT_DST=/etc/init.d/gnssagent
[ "$(id -u)" -eq 0 ] || { echo 'Please run as root' >&2; exit 1; }
if [ -e "$INIT_DST" ] || [ -L "$INIT_DST" ]; then
    [ -f "$INIT_DST" ] && [ ! -L "$INIT_DST" ] && grep -qFx '# Managed by GNSSAgent install.sh.' "$INIT_DST" ||
        { echo "Existing service entry is not managed by GNSSAgent: $INIT_DST" >&2; exit 1; }
else
    echo 'GNSSAgent auto-start is not installed'
    exit 0
fi
[ -f "$INSTALL_PATH/gnssagentctl.sh" ] || { echo 'GNSSAgent control script missing' >&2; exit 1; }
command -v chkconfig >/dev/null 2>&1 || { echo 'SysV environment requires chkconfig' >&2; exit 1; }
/bin/sh "$INSTALL_PATH/gnssagentctl.sh" stop
chkconfig gnssagent off
chkconfig --del gnssagent
rm -f -- /var/lock/subsys/gnssagent
rm -f -- "$INIT_DST"
echo 'GNSSAgent auto-start removed; program and logs retained'
