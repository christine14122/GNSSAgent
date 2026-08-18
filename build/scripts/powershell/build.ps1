param(
    [string]$OutputDirectory = "build\dist\bin"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
$compilerArchive = Join-Path $projectRoot "build\compiler\go1.25.5.windows-amd64.zip"
$compilerRoot = Join-Path $projectRoot "build\compiler\.go1.25.5"
$goExe = Join-Path $compilerRoot "go\bin\go.exe"

if (-not (Test-Path -LiteralPath $compilerArchive -PathType Leaf)) {
    throw "Go 1.25.5 archive is missing: $compilerArchive"
}
if (-not (Test-Path -LiteralPath $compilerRoot)) {
    New-Item -ItemType Directory -Path $compilerRoot | Out-Null
    Expand-Archive -LiteralPath $compilerArchive -DestinationPath $compilerRoot
}
if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) {
    throw "Go 1.25.5 executable is missing from the extracted toolchain: $goExe"
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
    $version = (& $goExe version) -join "`n"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to run the Go 1.25.5 toolchain: $goExe"
    }
    if ($version.Trim() -ne "go version go1.25.5 windows/amd64") {
        throw "Expected Go 1.25.5 windows/amd64, got: $version"
    }

	$env:GOOS = "windows"
	$env:GOARCH = "amd64"
	Restore-ProcessEnvironment "GOARM" $null
	Restore-ProcessEnvironment "GOARM64" $null
	$env:CGO_ENABLED = "0"
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "GNSSAgent tests failed under Go 1.25.5"
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
