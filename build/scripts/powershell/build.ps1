param(
    [string]$OutputDirectory = "build\dist\bin"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
$compilerArchive = Join-Path $projectRoot "build\compiler\go1.26.4.windows-amd64.zip"
$compilerArchiveSha256 = "3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345"
$compilerRoot = Join-Path $projectRoot "build\compiler\.go1.26.4-windows-amd64"
$bundledGoExe = Join-Path $compilerRoot "go\bin\go.exe"

if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    $outputRoot = $OutputDirectory
} else {
    $outputRoot = Join-Path $projectRoot $OutputDirectory
}
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null

function Restore-ProcessEnvironment {
    param([string]$Name, [AllowNull()][string]$Value)
    [Environment]::SetEnvironmentVariable($Name, $Value, [EnvironmentVariableTarget]::Process)
}

function Get-FileSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stream = [System.IO.File]::OpenRead($Path)
    try {
        $sha256 = [System.Security.Cryptography.SHA256]::Create()
        try {
            return ([System.BitConverter]::ToString($sha256.ComputeHash($stream))).Replace("-", "").ToLowerInvariant()
        } finally {
            $sha256.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

function Build-GNSSAgent {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [string]$GOOS = "linux",
        [Parameter(Mandatory = $true)][string]$GOARCH,
        [string]$GOARM = "",
        [string]$GOARM64 = "",
        [Parameter(Mandatory = $true)][string]$Target
    )

    $previousGOOS = $env:GOOS
    $previousGOARCH = $env:GOARCH
    $previousGOARM = $env:GOARM
    $previousGOARM64 = $env:GOARM64
    $previousCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = $GOOS
        $env:GOARCH = $GOARCH
        $env:GOARM = $GOARM
        $env:GOARM64 = $GOARM64
        $env:CGO_ENABLED = "0"
        $destination = Join-Path $outputRoot $Name
        & $goExe build -trimpath -ldflags "-s -w -X gnssagent/internal/buildinfo.Target=$Target" `
            -o $destination ./cmd/gnssagent
        if ($LASTEXITCODE -ne 0) {
            throw "$Name cross-compilation failed"
        }
        Write-Host "Built $destination"
    } finally {
        Restore-ProcessEnvironment "GOOS" $previousGOOS
        Restore-ProcessEnvironment "GOARCH" $previousGOARCH
        Restore-ProcessEnvironment "GOARM" $previousGOARM
        Restore-ProcessEnvironment "GOARM64" $previousGOARM64
        Restore-ProcessEnvironment "CGO_ENABLED" $previousCGO
    }
}

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousGOARM = $env:GOARM
$previousGOARM64 = $env:GOARM64
$previousCGO = $env:CGO_ENABLED
$previousGOTOOLCHAIN = $env:GOTOOLCHAIN
Push-Location $projectRoot
try {
    $env:GOTOOLCHAIN = "local"
    $systemGo = Get-Command "go" -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -ne $systemGo) {
        $goExe = $systemGo.Source
        $version = (& $goExe version) -join "`n"
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to run the system Go toolchain: $goExe"
        }
        Write-Host "Using system Go: $goExe ($($version.Trim()))"
    } else {
        if (-not (Test-Path -LiteralPath $compilerArchive -PathType Leaf)) {
            throw "Go 1.26.4 archive is missing: $compilerArchive"
        }
        $actualHash = Get-FileSha256 -Path $compilerArchive
        if ($actualHash -ne $compilerArchiveSha256) {
            throw "Go 1.26.4 archive checksum mismatch: expected $compilerArchiveSha256, got $actualHash"
        }
        if (-not (Test-Path -LiteralPath $bundledGoExe -PathType Leaf)) {
            New-Item -ItemType Directory -Force -Path $compilerRoot | Out-Null
            Expand-Archive -LiteralPath $compilerArchive -DestinationPath $compilerRoot -Force
        }
        if (-not (Test-Path -LiteralPath $bundledGoExe -PathType Leaf)) {
            throw "Go 1.26.4 executable is missing from the extracted toolchain: $bundledGoExe"
        }
        $goExe = $bundledGoExe
        $version = (& $goExe version) -join "`n"
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to run the Go 1.26.4 toolchain: $goExe"
        }
        if ($version.Trim() -ne "go version go1.26.4 windows/amd64") {
            throw "Expected Go 1.26.4 windows/amd64, got: $version"
        }
        Write-Host "Using bundled Go: $goExe ($($version.Trim()))"
    }

	$env:GOOS = "windows"
	$env:GOARCH = "amd64"
	Restore-ProcessEnvironment "GOARM" $null
	Restore-ProcessEnvironment "GOARM64" $null
	$env:CGO_ENABLED = "0"
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "GNSSAgent tests failed"
    }

    Build-GNSSAgent -Name "GNSSAgent-CCU" -GOARCH "amd64" -Target "ccu"
    Build-GNSSAgent -Name "GNSSAgent-MultibandRadio" -GOARCH "arm64" -GOARM64 "v8.0" -Target "multiband-radio"
    Build-GNSSAgent -Name "GNSSAgent-MultibandHandheld" -GOARCH "arm" -GOARM "7" -Target "multiband-handheld"
} finally {
    Pop-Location
    Restore-ProcessEnvironment "GOOS" $previousGOOS
    Restore-ProcessEnvironment "GOARCH" $previousGOARCH
    Restore-ProcessEnvironment "GOARM" $previousGOARM
    Restore-ProcessEnvironment "GOARM64" $previousGOARM64
    Restore-ProcessEnvironment "GOTOOLCHAIN" $previousGOTOOLCHAIN
    Restore-ProcessEnvironment "CGO_ENABLED" $previousCGO
}
