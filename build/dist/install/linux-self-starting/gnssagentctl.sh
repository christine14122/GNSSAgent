#!/bin/sh
# GNSSAgent公共进程控制；用于SysV x86-64，启动目录就是配置包相对路径基准。
set -eu

BASE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
BIN_FILE=$(readlink -f "$BASE_DIR/GNSSAgent-CCU")
PID_FILE="$BASE_DIR/gnssagent.pid"
cd "$BASE_DIR"

case "${1:-}" in start|stop|restart|status) ACTION=$1;;
    *) echo "Usage: $0 {start|stop|restart|status}" >&2; exit 2;;
esac
command -v flock >/dev/null 2>&1 || { echo 'GNSSAgent requires flock (util-linux)' >&2; exit 1; }
exec 9>"$PID_FILE.lock"
flock -n 9 || { echo 'Another GNSSAgent control operation is active' >&2; exit 4; }

process_info() {
    # 删除包含空格或括号的comm字段后，state为第1列，starttime为第20列。
    awk '{sub(/^.*\) /, ""); print $1, $20}' "/proc/$1/stat" 2>/dev/null
}

inspect_process() {
    if [ ! -e "$PID_FILE" ] && [ ! -L "$PID_FILE" ]; then return 3; fi
    [ -f "$PID_FILE" ] || return 4
    read -r PID TOKEN EXTRA < "$PID_FILE" || return 4
    case "$PID:$TOKEN" in *[!0-9:]*|:*|*:|*:*:*) return 4;; esac
    [ -z "${EXTRA:-}" ] && [ "$PID" -gt 1 ] || return 4
    [ -d "/proc/$PID" ] || return 3
    INFO=$(process_info "$PID") || return 4
    [ "${INFO#* }" = "$TOKEN" ] || return 4
    [ "${INFO%% *}" != Z ] || return 3
    EXE=$(readlink "/proc/$PID/exe" 2>/dev/null) || return 4
    [ "${EXE% (deleted)}" = "$BIN_FILE" ] || return 4
    kill -0 "$PID" 2>/dev/null || return 4
    return 0
}

start_service() {
    if inspect_process; then
        echo "GNSSAgent is already running (pid: $PID)"
        return 0
    else
        RESULT=$?
        [ "$RESULT" -eq 3 ] || { echo 'Cannot verify gnssagent.pid; inspect it manually' >&2; return 4; }
    fi
    [ -x "$BIN_FILE" ] || { echo "GNSSAgent executable missing or not executable: $BIN_FILE" >&2; return 1; }
    if [ -f "$PID_FILE" ]; then rm -- "$PID_FILE"; fi
    # 不创建无限增长的控制台日志；关闭锁描述符及标准输入，脱离终端。
    nohup "$BIN_FILE" --log-file "$BASE_DIR/../Logs/GNSSAgent/gnssagent.log" </dev/null >/dev/null 2>&1 9>&- &
    PID=$!
    INFO=$(process_info "$PID") || { echo 'GNSSAgent exited during startup; inspect ../Logs/GNSSAgent' >&2; return 1; }
    TOKEN=${INFO#* }
    case "$TOKEN" in ''|*[!0-9]*) echo 'Unable to obtain GNSSAgent start time' >&2; return 1;; esac
    printf '%s %s\n' "$PID" "$TOKEN" > "$PID_FILE"
    sleep 1
    if inspect_process; then
        echo "GNSSAgent started (pid: $PID); GNSS availability is reported separately"
        return 0
    else
        RESULT=$?
        if [ "$RESULT" -eq 3 ]; then rm -- "$PID_FILE"; fi
        echo 'GNSSAgent did not remain running; inspect ../Logs/GNSSAgent or run ./GNSSAgent-CCU' >&2
        return 1
    fi
}

stop_service() {
    if inspect_process; then
        kill -TERM "$PID" || return 1
    else
        RESULT=$?
        if [ "$RESULT" -eq 3 ]; then
            if [ -f "$PID_FILE" ]; then rm -- "$PID_FILE"; fi
            echo 'GNSSAgent is already stopped'
            return 0
        fi
        echo 'Cannot verify gnssagent.pid; refusing to signal another process' >&2
        return 4
    fi
    COUNT=0
    while inspect_process; do
        if [ "$COUNT" -ge 15 ]; then
            echo 'GNSSAgent did not stop within 15 seconds; PID retained, no forced kill' >&2
            return 1
        fi
        sleep 1
        COUNT=$((COUNT + 1))
    done
    # 再核对退出结果；身份变化时不删除诊断文件。
    if inspect_process; then return 1; else RESULT=$?; fi
    [ "$RESULT" -eq 3 ] || { echo 'GNSSAgent process identity changed while stopping; PID retained' >&2; return 4; }
    rm -- "$PID_FILE"
    echo 'GNSSAgent stopped'
}

case "$ACTION" in
    start) start_service;;
    stop) stop_service;;
    restart) stop_service && start_service;;
    status)
        if inspect_process; then echo "GNSSAgent is running (pid: $PID)"; else
            RESULT=$?
            if [ "$RESULT" -eq 3 ]; then echo 'GNSSAgent is not running'; else echo 'GNSSAgent status unknown; inspect gnssagent.pid' >&2; fi
            exit "$RESULT"
        fi
        ;;
esac
