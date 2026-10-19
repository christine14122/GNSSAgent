#!/bin/sh
# GNSSAgent公共进程控制；用于SysV x86-64，启动目录就是配置包相对路径基准。
set -eu

BASE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
BINARY_NAME=GNSSAgent-CCU
BIN_FILE=$(readlink -f "$BASE_DIR/$BINARY_NAME")
PID_FILE="$BASE_DIR/gnssagent.pid"
cd "$BASE_DIR"

case "${1:-}" in start|stop|restart|status) ACTION=$1;;
    *) echo "Usage: $0 {start|stop|restart|status}" >&2; exit 2;;
esac
command -v flock >/dev/null 2>&1 || { echo 'GNSSAgent requires flock (util-linux or BusyBox applet)' >&2; exit 1; }
exec 9>"$PID_FILE.lock"
flock -n 9 || { echo 'Another GNSSAgent control operation is active' >&2; exit 4; }

process_info() {
    # 删除包含空格或括号的comm字段后，state为第1列，starttime为第20列。
    awk '{sub(/^.*\) /, ""); print $1, $20}' "/proc/$1/stat" 2>/dev/null
}

ensure_no_agent() {
    # PID 丢失时先查实际程序，避免重复启动或把运行中的服务误报为停止。
    for PROC_EXE in /proc/[0-9]*/exe; do
        PROC_PATH=$(readlink "$PROC_EXE" 2>/dev/null) || continue
        if [ "${PROC_PATH% (deleted)}" = "$BIN_FILE" ]; then
            echo "GNSSAgent process exists without valid PID data: $PROC_EXE; inspect it manually" >&2
            return 4
        fi
    done
    return 0
}

inspect_process() {
    if [ ! -e "$PID_FILE" ] && [ ! -L "$PID_FILE" ]; then
        ensure_no_agent || return 4
        return 3
    fi
    [ ! -L "$PID_FILE" ] || return 4
    [ -f "$PID_FILE" ] || return 4
    if [ ! -s "$PID_FILE" ]; then
        ensure_no_agent || return 4
        return 3
    fi
    read -r PID TOKEN SAVED_BOOT EXTRA < "$PID_FILE" || return 4
    case "$PID:$TOKEN" in *[!0-9:]*|:*|*:|*:*:*) return 4;; esac
    [ -z "${EXTRA:-}" ] && [ "$PID" -gt 1 ] || return 4
    if [ -n "${SAVED_BOOT:-}" ]; then
        case "$SAVED_BOOT" in *[!0-9a-f-]*) return 4;; esac
        [ "${#SAVED_BOOT}" -eq 36 ] || return 4
        read -r CURRENT_BOOT < /proc/sys/kernel/random/boot_id || return 4
        [ "$SAVED_BOOT" = "$CURRENT_BOOT" ] || return 3
    fi
    [ -d "/proc/$PID" ] || return 3
    INFO=$(process_info "$PID") || return 4
    [ "${INFO#* }" = "$TOKEN" ] || return 4
    [ "${INFO%% *}" != Z ] || return 3
    EXE=$(readlink "/proc/$PID/exe" 2>/dev/null) || return 4
    [ "${EXE% (deleted)}" = "$BIN_FILE" ] || return 4
    kill -0 "$PID" 2>/dev/null || return 4
    return 0
}

# 在持锁状态下按可执行文件查找，避免旧PID、丢失PID或启动写盘中断产生重复实例。
find_existing_process() {
    FOUND_PID=
    FOUND_TOKEN=
    for PROC_DIR in /proc/[0-9]*; do
        CANDIDATE=${PROC_DIR##*/}
        if CANDIDATE_EXE=$(readlink "$PROC_DIR/exe" 2>/dev/null); then
            [ "${CANDIDATE_EXE% (deleted)}" = "$BIN_FILE" ] || continue
            CANDIDATE_INFO=$(process_info "$CANDIDATE") || return 4
            [ "${CANDIDATE_INFO%% *}" != Z ] || continue
            [ -z "$FOUND_PID" ] || { echo 'Multiple GNSSAgent processes found; inspect manually' >&2; return 4; }
            FOUND_PID=$CANDIDATE
            FOUND_TOKEN=${CANDIDATE_INFO#* }
        elif [ -d "$PROC_DIR" ]; then
            # 内核线程、僵尸的cmdline为空且exe不可读；其他不可核验进程保守拒绝。
            CANDIDATE_CMD=$(tr -d '\000' < "$PROC_DIR/cmdline" 2>/dev/null) || return 4
            if [ -n "$CANDIDATE_CMD" ]; then
                echo "Cannot inspect process $CANDIDATE; refusing duplicate startup" >&2
                return 4
            fi
        fi
    done
}

write_pid() {
    read -r CURRENT_BOOT < /proc/sys/kernel/random/boot_id || return 4
    [ -n "$CURRENT_BOOT" ] || return 4
    PID_TMP=$(mktemp "$PID_FILE.tmp.XXXXXX") || return 4
    if printf '%s %s %s\n' "$PID" "$TOKEN" "$CURRENT_BOOT" > "$PID_TMP" && mv -f -- "$PID_TMP" "$PID_FILE"; then
        return 0
    fi
    rm -f -- "$PID_TMP"
    return 4
}

# 兼容旧版两字段PID：只有可证明进程身份已改变时才允许恢复。
legacy_pid_is_stale() {
    [ -f "$PID_FILE" ] && [ ! -L "$PID_FILE" ] || return 1
    read -r OLD_PID OLD_TOKEN OLD_BOOT OLD_EXTRA < "$PID_FILE" || return 1
    case "$OLD_PID:$OLD_TOKEN" in *[!0-9:]*|:*|*:|*:*:*) return 1;; esac
    [ "$OLD_PID" -gt 1 ] && [ -z "${OLD_BOOT:-}${OLD_EXTRA:-}" ] || return 1
    OLD_INFO=$(process_info "$OLD_PID") || return 1
    OLD_EXE=$(readlink "/proc/$OLD_PID/exe" 2>/dev/null) || return 1
    [ "${OLD_INFO#* }" != "$OLD_TOKEN" ] || [ "${OLD_EXE% (deleted)}" != "$BIN_FILE" ]
}

# 只允许start恢复空/缺失记录；stop/status继续拒绝把未核验的活进程当成已停止。
pid_record_is_absent_or_empty() {
    [ ! -L "$PID_FILE" ] || return 1
    [ ! -e "$PID_FILE" ] || { [ -f "$PID_FILE" ] && [ ! -s "$PID_FILE" ]; }
}

start_service() {
    if pid_record_is_absent_or_empty; then
        : # start扫描实际进程后恢复；stop/status继续使用严格检查。
    elif inspect_process; then
        echo "GNSSAgent is already running (pid: $PID)"
        return 0
    else
        RESULT=$?
        if [ "$RESULT" -ne 3 ] && ! legacy_pid_is_stale; then
            echo 'Cannot verify gnssagent.pid; inspect it manually' >&2
            return 4
        fi
    fi
    [ ! -L "$PID_FILE" ] || { echo 'Refusing symbolic-link PID file' >&2; return 4; }
    find_existing_process || return 4
    if [ -n "$FOUND_PID" ]; then
        PID=$FOUND_PID
        TOKEN=$FOUND_TOKEN
        write_pid || return 4
        inspect_process || return 4
        echo "GNSSAgent is already running; PID record recovered (pid: $PID)"
        return 0
    fi
    [ -x "$BIN_FILE" ] || { echo "GNSSAgent executable missing or not executable: $BIN_FILE" >&2; return 1; }
    if [ -f "$PID_FILE" ]; then rm -- "$PID_FILE"; fi
    # 不创建无限增长的控制台日志；关闭锁描述符及标准输入，脱离终端。
    mkdir -p "$BASE_DIR/../Logs/GNSSAgent"
    nohup "$BIN_FILE" --log-file "$BASE_DIR/../Logs/GNSSAgent/gnssagent.log" </dev/null >/dev/null 2>&1 9>&- &
    PID=$!
    INFO=$(process_info "$PID") || { echo 'GNSSAgent exited during startup; inspect ../Logs/GNSSAgent' >&2; return 1; }
    TOKEN=${INFO#* }
    case "$TOKEN" in ''|*[!0-9]*) echo 'Unable to obtain GNSSAgent start time' >&2; return 1;; esac
    write_pid || return 4
    sleep 1
    if inspect_process; then
        echo "GNSSAgent started (pid: $PID)"
        return 0
    else
        RESULT=$?
        if [ "$RESULT" -eq 3 ]; then rm -- "$PID_FILE"; fi
        echo "GNSSAgent did not remain running; inspect ../Logs/GNSSAgent or run ./$BINARY_NAME" >&2
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
