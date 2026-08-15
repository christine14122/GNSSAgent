param(
    [string]$OutputDirectory = "build\dist\bin"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$compilerArchive = Join-Path $projectRoot "build\compiler\go1.23.12.windows-amd64.zip"
$compilerRoot = Join-Path $projectRoot "build\compiler\.go1.23.12"
$goExe = Join-Path $compilerRoot "go\bin\go.exe"

if (-not (Test-Path -LiteralPath $compilerArchive -PathType Leaf)) {
    throw "Go 1.23.12 archive is missing: $compilerArchive"
}
if (-not (Test-Path -LiteralPath $compilerRoot)) {
    New-Item -ItemType Directory -Path $compilerRoot | Out-Null
    Expand-Archive -LiteralPath $compilerArchive -DestinationPath $compilerRoot
}
if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) {
    throw "Go 1.23.12 executable is missing from the extracted toolchain: $goExe"
}

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

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousGOARM = $env:GOARM
$previousCGO = $env:CGO_ENABLED
$previousGOTOOLCHAIN = $env:GOTOOLCHAIN

Push-Location $projectRoot
try {
    $env:GOTOOLCHAIN = "local"
    $version = (& $goExe version) -join "`n"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to run the Go 1.23.12 toolchain: $goExe"
    }
    if ($version.Trim() -ne "go version go1.23.12 windows/amd64") {
        throw "Expected Go 1.23.12 windows/amd64, got: $version"
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
