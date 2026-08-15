$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$defaultsPath = Join-Path $projectRoot "deploy\default\gnssagent"
$initPath = Join-Path $projectRoot "deploy\init.d\gnssagent"
$smokePath = Join-Path $projectRoot "tests\device\smoke.sh"
$rotatingPath = Join-Path $projectRoot "internal\observe\rotating_file.go"

foreach ($path in @($defaultsPath, $initPath, $smokePath, $rotatingPath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required deployment file is missing: $path"
    }
}

$defaults = Get-Content -LiteralPath $defaultsPath -Raw
$init = Get-Content -LiteralPath $initPath -Raw
$smoke = Get-Content -LiteralPath $smokePath -Raw
$rotating = Get-Content -LiteralPath $rotatingPath -Raw

$requiredDefaults = @(
    'UDP_LISTEN=127.0.0.1:29501',
    'TCP_LISTEN=0.0.0.0:29501',
    'MAX_CONNECTIONS=5',
    'MAX_REMOTE_CONNECTIONS=4',
    'LOG_LEVEL=info',
    'LOG_FILE=/lib/firmware/gnssagent/log/gnssagent.log',
    'LOG_MAX_BYTES=8388608',
    'CONSOLE_LOG=/var/volatile/gnssagent-console.log'
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
