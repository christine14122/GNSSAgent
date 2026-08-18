$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$powerShellDirectory = Join-Path $projectRoot "build\scripts\powershell"
$makeDirectory = Join-Path $projectRoot "build\scripts\make"
$mainScript = Join-Path $powerShellDirectory "build.ps1"
$hfScript = Join-Path $powerShellDirectory "build-hf.ps1"
$primaryMakefile = Join-Path $makeDirectory "Makefile"

foreach ($path in @(
    $mainScript,
    $hfScript,
    (Join-Path $powerShellDirectory "build.bat"),
    $primaryMakefile,
    (Join-Path $makeDirectory "Makefile_CCU"),
    (Join-Path $makeDirectory "Makefile_HF"),
    (Join-Path $makeDirectory "Makefile_MultibandRadio"),
    (Join-Path $makeDirectory "Makefile_MultibandHandheld")
)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required build script is missing: $path"
    }
}

$main = Get-Content -LiteralPath $mainScript -Raw
$hf = Get-Content -LiteralPath $hfScript -Raw
$make = Get-Content -LiteralPath $primaryMakefile -Raw

$required = @(
    'go1.26.4.windows-amd64.zip',
    '3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345',
    'Get-Command',
    'System.Security.Cryptography.SHA256',
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

foreach ($text in @(
    'go1.23.12.windows-amd64.zip',
    '07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc',
    'go version go1.23.12 windows/amd64',
    'Get-Command',
    'System.Security.Cryptography.SHA256',
    'GNSSAgent-HF',
    'CGO_ENABLED',
    '-trimpath',
    '-s -w',
    'gnssagent/internal/buildinfo.Target'
)) {
    if (-not $hf.Contains($text)) {
        throw "build-hf.ps1 is missing required contract text: $text"
    }
}

foreach ($text in @(
    'go1.26.4.linux-amd64.tar.gz',
    '1153d3d50e0ac764b447adfe05c2bcf08e889d42a02e0fe0259bd47f6733ad7f',
    'go1.23.12.linux-amd64.tar.gz',
    'd3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90',
    'GNSSAgent-CCU',
    'GNSSAgent-MultibandRadio',
    'GNSSAgent-MultibandHandheld',
    'GNSSAgent-HF',
    'GOOS=linux',
    'GOARCH=amd64',
    'GOARCH=arm64',
    'GOARCH=arm',
    'GOARM64=v8.0',
    'GOARM=7',
    'CGO_ENABLED=0',
    'GOTOOLCHAIN=local',
    '-trimpath',
    '-buildvcs=false',
    'gnssagent/internal/buildinfo.Target'
)) {
    if (-not $make.Contains($text)) {
        throw "Makefile is missing required contract text: $text"
    }
}

foreach ($wrapper in @(
    @{ Path = "Makefile_CCU"; Target = "ccu" },
    @{ Path = "Makefile_HF"; Target = "hf" },
    @{ Path = "Makefile_MultibandRadio"; Target = "multiband-radio" },
    @{ Path = "Makefile_MultibandHandheld"; Target = "multiband-handheld" }
)) {
    $content = Get-Content -LiteralPath (Join-Path $makeDirectory $wrapper.Path) -Raw
    if (-not $content.Contains('$(MAKE_DIR)/Makefile') -or -not $content.Contains($wrapper.Target)) {
        throw "$($wrapper.Path) does not delegate to $($wrapper.Target) through the primary Makefile"
    }
}

foreach ($script in @($main, $hf, $make)) {
    foreach ($forbidden in @('CCU-Audio', '--serial', '--baud')) {
        if ($script.Contains($forbidden)) {
            throw "Build script contains forbidden text: $forbidden"
        }
    }
}

foreach ($script in @($main, $hf)) {
    if ($script.Contains('Get-FileHash')) {
        throw "PowerShell build script depends on unavailable Get-FileHash module resolution"
    }
}

Write-Host "GNSSAgent build contract passed."
