param(
    [string]$OutputDirectory = "build\dist\bin"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
$compilerArchive = Join-Path $projectRoot "build\compiler\go1.23.12.windows-amd64.zip"
$compilerArchiveSha256 = "07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc"
$compilerRoot = Join-Path $projectRoot "build\compiler\.go1.23.12-windows-amd64"
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

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousGOARM = $env:GOARM
$previousCGO = $env:CGO_ENABLED
$previousGOTOOLCHAIN = $env:GOTOOLCHAIN

Push-Location $projectRoot
try {
    $env:GOTOOLCHAIN = "local"
    $goExe = $null
    $version = ""
    $systemGo = Get-Command "go" -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -ne $systemGo) {
        $systemVersion = (& $systemGo.Source version) -join "`n"
        if ($LASTEXITCODE -eq 0 -and $systemVersion.Trim() -eq "go version go1.23.12 windows/amd64") {
            $goExe = $systemGo.Source
            $version = $systemVersion.Trim()
            Write-Host "Using system Go for HF: $goExe ($version)"
        }
    }
    if ($null -eq $goExe) {
        if (-not (Test-Path -LiteralPath $compilerArchive -PathType Leaf)) {
            throw "Go 1.23.12 archive is missing: $compilerArchive"
        }
        $actualHash = Get-FileSha256 -Path $compilerArchive
        if ($actualHash -ne $compilerArchiveSha256) {
            throw "Go 1.23.12 archive checksum mismatch: expected $compilerArchiveSha256, got $actualHash"
        }
        if (-not (Test-Path -LiteralPath $bundledGoExe -PathType Leaf)) {
            New-Item -ItemType Directory -Force -Path $compilerRoot | Out-Null
            Expand-Archive -LiteralPath $compilerArchive -DestinationPath $compilerRoot -Force
        }
        if (-not (Test-Path -LiteralPath $bundledGoExe -PathType Leaf)) {
            throw "Go 1.23.12 executable is missing from the extracted toolchain: $bundledGoExe"
        }
        $goExe = $bundledGoExe
        $version = (& $goExe version) -join "`n"
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to run the Go 1.23.12 toolchain: $goExe"
        }
        if ($version.Trim() -ne "go version go1.23.12 windows/amd64") {
            throw "Expected Go 1.23.12 windows/amd64, got: $version"
        }
        $version = $version.Trim()
        Write-Host "Using bundled Go for HF: $goExe ($version)"
    }

    $env:GOOS = "linux"
    $env:GOARCH = "arm"
    $env:GOARM = "7"
    $env:CGO_ENABLED = "0"
    $destination = Join-Path $outputRoot "GNSSAgent-HF"
    & $goExe build -trimpath -ldflags "-s -w -X gnssagent/internal/buildinfo.Target=hf" `
        -o $destination ./cmd/gnssagent
    if ($LASTEXITCODE -ne 0) {
        throw "GNSSAgent-HF cross-compilation failed"
    }
    Write-Host "Built $destination with $version"
} finally {
    Pop-Location
    Restore-ProcessEnvironment "GOOS" $previousGOOS
    Restore-ProcessEnvironment "GOARCH" $previousGOARCH
    Restore-ProcessEnvironment "GOARM" $previousGOARM
    Restore-ProcessEnvironment "CGO_ENABLED" $previousCGO
    Restore-ProcessEnvironment "GOTOOLCHAIN" $previousGOTOOLCHAIN
}
