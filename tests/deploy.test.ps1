$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$defaultsPath = Join-Path $projectRoot "deploy\default\gnssagent"
$initPath = Join-Path $projectRoot "deploy\init.d\gnssagent"
$smokePath = Join-Path $projectRoot "tests\device\smoke.sh"
$supervisorTestPath = Join-Path $projectRoot "tests\device\init_supervisor.test.sh"
$rotatingPath = Join-Path $projectRoot "internal\observe\rotating_file.go"
$planPath = Join-Path $projectRoot "docs\superpowers\plans\2026-08-03-gnss-agent-udp-implementation-plan.md"

foreach ($path in @($defaultsPath, $initPath, $smokePath, $supervisorTestPath, $rotatingPath, $planPath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required deployment file is missing: $path"
    }
}

$defaults = Get-Content -LiteralPath $defaultsPath -Raw
$init = Get-Content -LiteralPath $initPath -Raw
$smoke = Get-Content -LiteralPath $smokePath -Raw
$rotating = Get-Content -LiteralPath $rotatingPath -Raw
$plan = Get-Content -LiteralPath $planPath -Raw

$requiredDefaults = @(
    'UDP_LISTEN=0.0.0.0:29501',
    'TCP_LISTEN=0.0.0.0:29501',
    'MAX_CONNECTIONS=5',
    'MAX_REMOTE_CONNECTIONS=4',
    'LOG_LEVEL=info',
    'LOG_FILE=/lib/firmware/gnssagent/log/gnssagent.log',
    'LOG_MAX_BYTES=8388608',
    'CONSOLE_LOG=/var/volatile/gnssagent-console.log',
    'TIME_RMS_ENTER=2',
    'TIME_RMS_EXIT=5',
    'TIME_RMS_SPREAD=1',
    'TIME_CONFIRM_CYCLES=10',
    'TIME_EXIT_CYCLES=3',
    'TIME_GST_TIMEOUT=3s',
    'TIME_STEP_TOLERANCE=500ms'
)
foreach ($text in $requiredDefaults) {
    if (-not $defaults.Contains($text)) {
        throw "Deployment defaults are missing: $text"
    }
}

$logFileMatch = [regex]::Match($defaults, '(?m)^LOG_FILE=(.*)$')
if (-not $logFileMatch.Success -or [string]::IsNullOrWhiteSpace($logFileMatch.Groups[1].Value)) {
    throw "LOG_FILE must be non-empty"
}
$logFile = $logFileMatch.Groups[1].Value.Trim()
if ($logFile.StartsWith('/var/log') -or $logFile.StartsWith('/var/volatile')) {
    throw "LOG_FILE must be on persistent storage: $logFile"
}
$maxMatch = [regex]::Match($defaults, '(?m)^LOG_MAX_BYTES=([-0-9]+)$')
if (-not $maxMatch.Success -or [int64]$maxMatch.Groups[1].Value -le 0) {
    throw "LOG_MAX_BYTES must be positive"
}
if ($defaults -match '(?im)backup.*count|LOG_BACKUP') {
    throw "Backup count must not be configurable"
}
if (-not $rotating.Contains('w.path + ".1"') -or $rotating.Contains('w.path + ".2"')) {
    throw "Rotating log must have exactly one fixed .1 backup"
}

foreach ($text in @(
    'mkdir -p "$(dirname "$LOG_FILE")"',
    'mkdir -p "$(dirname "$CONSOLE_LOG")"',
    '--udp-listen "$UDP_LISTEN"',
    '--tcp-listen "$TCP_LISTEN"',
    '--max-connections "$MAX_CONNECTIONS"',
    '--max-remote-connections "$MAX_REMOTE_CONNECTIONS"',
    '--log-level "$LOG_LEVEL"',
    '--log-file "$LOG_FILE"',
    '--log-max-bytes "$LOG_MAX_BYTES"',
    '--time-rms-enter "$TIME_RMS_ENTER"',
    '--time-rms-exit "$TIME_RMS_EXIT"',
    '--time-rms-spread "$TIME_RMS_SPREAD"',
    '--time-confirm-cycles "$TIME_CONFIRM_CYCLES"',
    '--time-exit-cycles "$TIME_EXIT_CYCLES"',
    '--time-gst-timeout "$TIME_GST_TIMEOUT"',
    '--time-step-tolerance "$TIME_STEP_TOLERANCE"',
    '__gnssagent_supervise__',
    'nohup /bin/sh "$SCRIPT_PATH" "$SUPERVISOR_TOKEN"',
    'exec 3>"$CONSOLE_LOG"',
    '/proc/$check_pid/cmdline',
    'kill -TERM "$child_pid"',
    'sleep "$backoff_seconds"'
)) {
    if (-not $init.Contains($text)) {
        throw "Init script is missing: $text"
    }
}
if ($init.Contains('nohup "$@" >"$CONSOLE_LOG"') -or $init.Contains('start-stop-daemon')) {
    throw "Init script must use its supervisor and must not require start-stop-daemon"
}
if (([regex]::Matches($init, 'exec 3>"\$CONSOLE_LOG"')).Count -ne 1) {
    throw "The supervisor must open CONSOLE_LOG exactly once per service start"
}
if (-not $init.Contains('mv "$CONSOLE_LOG" "$CONSOLE_LOG.1"')) {
    throw "The previous supervisor console log must be retained as one fixed backup"
}

foreach ($text in @(
    'between **1430 and 1441 inclusive**',
    'No adjacent summaries may be less than 55 seconds apart',
    'persistent log disabled after terminal error',
    'open configured log file'
)) {
    if (-not $plan.Contains($text)) {
        throw "24-hour acceptance plan is missing: $text"
    }
}

foreach ($text in @('netstat -lun', 'netstat -lnt', 'ss -lun', 'ss -lnt', 'LOG_MAX_BYTES + 4096')) {
    if (-not $smoke.Contains($text)) {
        throw "Smoke script is missing: $text"
    }
}

$forbidden = @('/dev/tty', 'ttyUL', 'baud', 'termios', 'TIOC', 'CFGSYS', 'CFGSAVE', 'GPIO', 'link budget', '/proc/tty', 'overrun', 'parity')
foreach ($content in @($init, $smoke)) {
    foreach ($text in $forbidden) {
        if ($content.IndexOf($text, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
            throw "Deployment script contains forbidden text: $text"
        }
    }
}

Write-Host "GNSSAgent deployment contract passed."
