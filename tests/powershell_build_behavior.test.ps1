$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$sourceDirectory = Join-Path $projectRoot "build\scripts\powershell"
$temporaryRoot = Join-Path ([IO.Path]::GetTempPath()) ("gnssagent-powershell-build-" + [guid]::NewGuid().ToString("N"))
$fixtureDirectory = Join-Path $temporaryRoot "project\build\scripts\powershell"
$fakeBin = Join-Path $temporaryRoot "fake-bin"
$fakeLog = Join-Path $temporaryRoot "fake-go.log"

New-Item -ItemType Directory -Force -Path $fixtureDirectory, $fakeBin | Out-Null
Copy-Item -LiteralPath (Join-Path $sourceDirectory "build.ps1") -Destination $fixtureDirectory
Copy-Item -LiteralPath (Join-Path $sourceDirectory "build-hf.ps1") -Destination $fixtureDirectory

@'
@echo off
if "%1"=="version" (
  echo go version %FAKE_GO_VERSION% windows/amd64
  exit /b 0
)
echo %1>>"%FAKE_GO_LOG%"
if "%FAKE_GO_FAIL%"=="%1" exit /b 1
exit /b 0
'@ | Set-Content -LiteralPath (Join-Path $fakeBin "go.cmd") -Encoding ASCII

function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -ne $Expected) {
        throw "$Message. Expected '$Expected', got '$Actual'"
    }
}

function Invoke-ExpectedFailure {
    param([scriptblock]$Action, [string]$ExpectedMessage)
    try {
        & $Action
    } catch {
        if ($_.Exception.Message -notlike "*$ExpectedMessage*") {
            throw "Expected failure containing '$ExpectedMessage', got: $($_.Exception.Message)"
        }
        return
    }
    throw "Expected failure containing '$ExpectedMessage', but the command succeeded"
}

function Restore-ProcessEnvironment {
    param([string]$Name, [AllowNull()][string]$Value)
    [Environment]::SetEnvironmentVariable($Name, $Value, [EnvironmentVariableTarget]::Process)
}

$previousPath = $env:PATH
$previousLog = $env:FAKE_GO_LOG
$previousVersion = $env:FAKE_GO_VERSION
$previousFailure = $env:FAKE_GO_FAIL

try {
    $env:PATH = "$fakeBin;$previousPath"
    $env:FAKE_GO_LOG = $fakeLog

    $env:FAKE_GO_VERSION = "go9.9.9"
    $env:FAKE_GO_FAIL = ""
    & (Join-Path $fixtureDirectory "build.ps1") -OutputDirectory (Join-Path $temporaryRoot "ordinary-success")
    $calls = @(Get-Content -LiteralPath $fakeLog)
    Assert-Equal $calls.Count 4 "Ordinary system Go did not run test plus three builds exactly once"
    Assert-Equal (@($calls | Where-Object { $_ -eq "test" }).Count) 1 "Ordinary tests were not run once"
    Assert-Equal (@($calls | Where-Object { $_ -eq "build" }).Count) 3 "Ordinary builds were not run once each"

    Clear-Content -LiteralPath $fakeLog
    $env:FAKE_GO_FAIL = "build"
    Invoke-ExpectedFailure {
        & (Join-Path $fixtureDirectory "build.ps1") -OutputDirectory (Join-Path $temporaryRoot "ordinary-failure")
    } "GNSSAgent-CCU cross-compilation failed"
    $calls = @(Get-Content -LiteralPath $fakeLog)
    Assert-Equal $calls.Count 2 "Ordinary failure retried or continued after the first failed build"
    Assert-Equal $calls[0] "test" "Ordinary failure did not test first"
    Assert-Equal $calls[1] "build" "Ordinary failure did not stop on the first build"

    Clear-Content -LiteralPath $fakeLog
    $env:FAKE_GO_VERSION = "go1.23.12"
    $env:FAKE_GO_FAIL = ""
    & (Join-Path $fixtureDirectory "build-hf.ps1") -OutputDirectory (Join-Path $temporaryRoot "hf-success")
    $calls = @(Get-Content -LiteralPath $fakeLog)
    Assert-Equal $calls.Count 1 "HF exact system Go did not build exactly once"
    Assert-Equal $calls[0] "build" "HF exact system Go did not run build"

    Clear-Content -LiteralPath $fakeLog
    $env:FAKE_GO_FAIL = "build"
    Invoke-ExpectedFailure {
        & (Join-Path $fixtureDirectory "build-hf.ps1") -OutputDirectory (Join-Path $temporaryRoot "hf-failure")
    } "GNSSAgent-HF cross-compilation failed"
    $calls = @(Get-Content -LiteralPath $fakeLog)
    Assert-Equal $calls.Count 1 "HF retried after an exact system Go build failed"

    Clear-Content -LiteralPath $fakeLog
    $env:FAKE_GO_VERSION = "go9.9.9"
    $env:FAKE_GO_FAIL = ""
    Invoke-ExpectedFailure {
        & (Join-Path $fixtureDirectory "build-hf.ps1") -OutputDirectory (Join-Path $temporaryRoot "hf-wrong-version")
    } "Go 1.23.12 archive is missing"
    Assert-Equal (Get-Item -LiteralPath $fakeLog).Length 0 "HF built with a wrong system Go version"
} finally {
    Restore-ProcessEnvironment "PATH" $previousPath
    Restore-ProcessEnvironment "FAKE_GO_LOG" $previousLog
    Restore-ProcessEnvironment "FAKE_GO_VERSION" $previousVersion
    Restore-ProcessEnvironment "FAKE_GO_FAIL" $previousFailure
}

Write-Host "GNSSAgent PowerShell build behavior passed."
