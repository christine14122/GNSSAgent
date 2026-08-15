#!/bin/sh

set -eu

DEFAULTS=${DEFAULTS:-/etc/default/gnssagent}
if [ ! -r "$DEFAULTS" ]; then
    echo "deployment defaults are not readable: $DEFAULTS" >&2
    exit 1
fi
. "$DEFAULTS"

read_live_pid() {
    [ -r "$PIDFILE" ] || return 1
    pid=$(sed -n '1p' "$PIDFILE" 2>/dev/null)
    case "$pid" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ "$pid" -gt 1 ] 2>/dev/null || return 1
    kill -0 "$pid" 2>/dev/null
}

if ! read_live_pid; then
    echo "GNSSAgent process is not running" >&2
    exit 1
fi
echo "GNSSAgent process is running (pid $pid)"

if command -v ss >/dev/null 2>&1; then
    udp_listeners=$(ss -lun)
    tcp_listeners=$(ss -lnt)
elif command -v netstat >/dev/null 2>&1; then
    udp_listeners=$(netstat -lun)
    tcp_listeners=$(netstat -lnt)
else
    echo "neither ss nor netstat is available" >&2
    exit 1
fi

printf '%s\n' "$udp_listeners" | grep -F "${UDP_LISTEN%:*}:${UDP_LISTEN##*:}" >/dev/null
printf '%s\n' "$tcp_listeners" | grep -E ":${TCP_LISTEN##*:}([[:space:]]|$)" >/dev/null
echo "UDP and TCP listeners are present"

for tool in nc od dd wc grep; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "smoke test prerequisite is missing: $tool" >&2
        exit 1
    fi
done
nc_help=$(nc -h 2>&1 || true)
case "$nc_help" in
    *-u*-w*|*-w*-u*) ;;
    *)
        echo "installed nc does not advertise compatible UDP (-u) and timeout (-w) options" >&2
        exit 1
        ;;
esac

udp_host=${UDP_LISTEN%:*}
udp_port=${UDP_LISTEN##*:}
tcp_port=${TCP_LISTEN##*:}

send_fixture() {
    while IFS= read -r line || [ -n "$line" ]; do
        if ! printf '%s\r\n' "$line" | nc -u -w 1 "$udp_host" "$udp_port" >/dev/null 2>&1; then
            echo "failed to send a fixture line as one UDP datagram" >&2
            return 1
        fi
    done <<'EOF'
$GNGGA,123519.00,3112.0000,N,12130.0000,E,1,08,0.9,12.3,M,0.0,M,,*7E
$GNGGA,123520.00,3112.0000,N,12130.0000,E,1,08,0.9,12.3,M,0.0,M,,*74
EOF
}

send_fixture

response_file=/tmp/gnssagent-smoke-response.$$
status_file=/tmp/gnssagent-smoke-status.$$
cleanup() {
    rm -f "$response_file"
    rm -f "$status_file"
}
trap cleanup EXIT HUP INT TERM

(
    printf '\107\116\123\123\001\001\000\001\001'
    sleep 4
) | nc -w 5 127.0.0.1 "$tcp_port" >"$response_file" &
subscriber_pid=$!
sleep 1
send_fixture
wait "$subscriber_pid" || true

response_size=$(wc -c <"$response_file" | tr -d ' ')
if [ "$response_size" -lt 75 ]; then
    echo "subscription response is only $response_size bytes; expected ACK plus a 66-byte SIMPLE frame" >&2
    exit 1
fi
dd if="$response_file" of="$status_file" bs=1 skip=9 count=66 2>/dev/null
status_size=$(wc -c <"$status_file" | tr -d ' ')
if [ "$status_size" -ne 66 ]; then
    echo "SIMPLE frame size is $status_size, expected 66" >&2
    exit 1
fi
status_header=$(od -An -tx1 -N8 "$status_file" | tr -d ' \n')
if [ "$status_header" != "474e53530104003a" ]; then
    echo "unexpected SIMPLE frame header: $status_header" >&2
    exit 1
fi
echo "SIMPLE subscription returned a valid 66-byte frame"

if [ ! -f "$LOG_FILE" ]; then
    echo "persistent service log is missing: $LOG_FILE" >&2
    exit 1
fi
echo "Socket buffer and drop-observation evidence:"
grep -E 'requested_rcvbuf|actual_rcvbuf|drop_source|kernel_drops' "$LOG_FILE" | tail -n 10 || true

limit=$((LOG_MAX_BYTES + 4096))
active_size=$(wc -c <"$LOG_FILE" | tr -d ' ')
echo "active log bytes: $active_size (limit $limit)"
if [ "$active_size" -gt "$limit" ]; then
    echo "active log exceeds bounded size" >&2
    exit 1
fi
if [ -f "$LOG_FILE.1" ]; then
    backup_size=$(wc -c <"$LOG_FILE.1" | tr -d ' ')
    echo "backup log bytes: $backup_size (limit $limit)"
    if [ "$backup_size" -gt "$limit" ]; then
        echo "backup log exceeds bounded size" >&2
        exit 1
    fi
else
    echo "backup log is not present yet"
fi
if [ -e "$LOG_FILE.2" ]; then
    echo "unexpected second log backup exists: $LOG_FILE.2" >&2
    exit 1
fi

echo "GNSSAgent UDP smoke checks passed"
