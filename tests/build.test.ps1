$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$mainScript = Join-Path $projectRoot "build\scripts\build.ps1"
$hfScript = Join-Path $projectRoot "build\scripts\build-hf.ps1"

foreach ($path in @($mainScript, $hfScript)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required build script is missing: $path"
    }
}

$main = Get-Content -LiteralPath $mainScript -Raw
$hf = Get-Content -LiteralPath $hfScript -Raw

$required = @(
    'go1.25.5',
    'GNSSAgent-CCU',
    'GNSSAgent-MultibandRadio',
    'GNSSAgent-MultibandHandheld',
    '-GOARM64 "v8.0"',
    '-GOARM "7"',
    'CGO_ENABLED',
    '-trimpath',
    '-s -w',
    'gnssagent/internal/buildinfo.Target'
)
foreach ($text in $required) {
    if (-not $main.Contains($text)) {
        throw "build.ps1 is missing required contract text: $text"
    }
}

foreach ($text in @('go1.23.12', 'GNSSAgent-HF', 'CGO_ENABLED', '-trimpath', '-s -w', 'gnssagent/internal/buildinfo.Target')) {
    if (-not $hf.Contains($text)) {
        throw "build-hf.ps1 is missing required contract text: $text"
    }
}

foreach ($script in @($main, $hf)) {
    foreach ($forbidden in @('CCU-Audio', '--serial', '--baud')) {
        if ($script.Contains($forbidden)) {
            throw "Build script contains forbidden text: $forbidden"
        }
    }
}

Write-Host "GNSSAgent build contract passed."
