#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
init_script=$project_root/deploy/init.d/gnssagent
test_dir=$(mktemp -d /tmp/gnssagent-supervisor.XXXXXX)
defaults=$test_dir/defaults
fake_agent=$test_dir/fake-agent
attempts=$test_dir/attempts
pidfile=$test_dir/supervisor.pid
child_pidfile=$pidfile.child
console_log=$test_dir/console.log
console_backup=$console_log.1
structured_log=$test_dir/structured.log
unrelated_pid=''

cleanup() {
    DEFAULTS=$defaults /bin/sh "$init_script" stop >/dev/null 2>&1 || true
    if [ -n "$unrelated_pid" ] && kill -0 "$unrelated_pid" 2>/dev/null; then
        kill -TERM "$unrelated_pid" 2>/dev/null || true
        wait "$unrelated_pid" 2>/dev/null || true
    fi
    rm -f "$child_pidfile"
    rm -f "$pidfile"
    rm -f "$attempts"
    rm -f "$console_backup"
    rm -f "$console_log"
    rm -f "$structured_log"
    rm -f "$fake_agent"
    rm -f "$defaults"
    rmdir "$test_dir" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

cat >"$fake_agent" <<EOF
#!/bin/sh
count=0
[ -r "$attempts" ] && count=\$(sed -n '1p' "$attempts")
count=\$((count + 1))
printf '%s\n' "\$count" >"$attempts"
printf 'fake attempt %s\n' "\$count" >&2
if [ "\$count" -lt 3 ]; then
    exit 7
fi
trap 'exit 0' HUP INT TERM
while :; do sleep 1; done
EOF
chmod +x "$fake_agent"

cat >"$defaults" <<EOF
GNSSAGENT_BIN=$fake_agent
PIDFILE=$pidfile
UDP_LISTEN=127.0.0.1:29501
TCP_LISTEN=127.0.0.1:29502
MAX_CONNECTIONS=5
MAX_REMOTE_CONNECTIONS=4
LOG_LEVEL=info
LOG_FILE=$structured_log
LOG_MAX_BYTES=8388608
CONSOLE_LOG=$console_log
EOF

DEFAULTS=$defaults /bin/sh "$init_script" start
sleep 4
DEFAULTS=$defaults /bin/sh "$init_script" status >/dev/null

attempt_count=$(sed -n '1p' "$attempts")
if [ "$attempt_count" -lt 3 ]; then
    echo "supervisor did not restart the failing child: attempts=$attempt_count" >&2
    exit 1
fi
for expected in 1 2 3; do
    grep -F "fake attempt $expected" "$console_log" >/dev/null
done

DEFAULTS=$defaults /bin/sh "$init_script" stop
DEFAULTS=$defaults /bin/sh "$init_script" start
sleep 1
grep -F "fake attempt 1" "$console_backup" >/dev/null
grep -F "fake attempt 4" "$console_log" >/dev/null
DEFAULTS=$defaults /bin/sh "$init_script" stop

sleep 30 &
unrelated_pid=$!
printf '%s\n' "$unrelated_pid" >"$pidfile"
DEFAULTS=$defaults /bin/sh "$init_script" stop
if ! kill -0 "$unrelated_pid" 2>/dev/null; then
    echo "stale PID handling signaled an unrelated process" >&2
    exit 1
fi

echo "GNSSAgent init supervisor behavior passed."
