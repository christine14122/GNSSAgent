#!/bin/sh
# Managed by GNSSAgent install.sh.
# chkconfig: 2345 91 00
# description: GNSSAgent-CCU - GNSS data service
# processname: GNSSAgent-CCU

GNSSAGENT_HOME='@GNSSAGENT_HOME@'
LOCK_FILE=/var/lock/subsys/gnssagent
start() {
    /bin/sh "$GNSSAGENT_HOME/gnssagentctl.sh" start || return $?
    touch "$LOCK_FILE"
}
stop() {
    /bin/sh "$GNSSAGENT_HOME/gnssagentctl.sh" stop || return $?
    rm -f -- "$LOCK_FILE"
}
case "${1:-}" in
    start) start;;
    stop) stop;;
    restart) stop && start;;
    status) /bin/sh "$GNSSAGENT_HOME/gnssagentctl.sh" status;;
    *) echo "Usage: $0 {start|stop|status|restart}" >&2; exit 2;;
esac
