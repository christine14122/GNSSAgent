$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot

foreach ($relative in @('internal\serial', 'internal\control')) {
    if (Test-Path -LiteralPath (Join-Path $projectRoot $relative)) {
        throw "Forbidden production directory exists: $relative"
    }
}

$productionFiles = Get-ChildItem -LiteralPath (Join-Path $projectRoot 'internal'), (Join-Path $projectRoot 'cmd') -Recurse -File -Filter '*.go' |
    Where-Object {
        $_.Name -notlike '*_test.go' -and
        $_.FullName -notlike "*\internal\protocol\*"
    }
$productionText = ($productionFiles | ForEach-Object { Get-Content -LiteralPath $_.FullName -Raw }) -join "`n"

$forbiddenProduction = @(
    'SerialDevice',
    '--baud',
    '/dev/tty',
    'termios',
    'TIOCEXCL',
    'TIOCGICOUNT',
    'CFGSYS',
    'CFGSAVE',
    'ParseSwitchRequest',
    'EncodeSwitchACK',
    'Settimeofday',
    'clock_settime',
    'date -s'
)
foreach ($text in $forbiddenProduction) {
    if ($productionText.IndexOf($text, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
        throw "Production code contains forbidden architecture text: $text"
    }
}

$config = Get-Content -LiteralPath (Join-Path $projectRoot 'internal\config\config.go') -Raw
foreach ($text in @('127.0.0.1:29501', 'netip.ParseAddrPort', '.Is4()', '.IsLoopback()', '.Port() == 0')) {
    if (-not $config.Contains($text)) {
        throw "IPv4 loopback UDP validation is missing: $text"
    }
}
$udpSocket = Get-Content -LiteralPath (Join-Path $projectRoot 'internal\udpinput\socket_linux.go') -Raw
if (-not $udpSocket.Contains('net.ListenPacket("udp4", address)')) {
    throw 'Linux UDP listener is not explicitly udp4'
}

$server = (Get-Content -LiteralPath (Join-Path $projectRoot 'internal\server\session.go') -Raw) +
    (Get-Content -LiteralPath (Join-Path $projectRoot 'internal\server\server.go') -Raw)
foreach ($text in @('ParseSwitchRequest', 'EncodeSwitchACK', 'control handler')) {
    if ($server.IndexOf($text, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
        throw "TCP server contains forbidden control path: $text"
    }
}

$deploymentFiles = @(
    Get-ChildItem -LiteralPath (Join-Path $projectRoot 'deploy') -Recurse -File
    Get-ChildItem -LiteralPath (Join-Path $projectRoot 'build') -Recurse -File
    Get-Item -LiteralPath (Join-Path $projectRoot 'tests\device\smoke.sh')
)
$deploymentText = ($deploymentFiles | ForEach-Object { Get-Content -LiteralPath $_.FullName -Raw }) -join "`n"
foreach ($text in @('/dev/tty', 'ttyUL', '--baud', 'termios', 'TIOC', 'CFGSYS', 'CFGSAVE', 'GPIO', '/proc/tty', 'link budget')) {
    if ($deploymentText.IndexOf($text, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
        throw "Deployment/build path contains forbidden text: $text"
    }
}
if ($deploymentText.IndexOf('gnss_survey', [StringComparison]::OrdinalIgnoreCase) -ge 0 -or
    $productionText.IndexOf('gnss_survey', [StringComparison]::OrdinalIgnoreCase) -ge 0) {
    throw 'Host acceptance survey tool is referenced by target runtime paths'
}

Write-Host "GNSSAgent UDP-only architecture contract passed."
