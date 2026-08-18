# GNSSAgent PowerShell Go Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the PowerShell builds prefer eligible system Go installations, use verified bundled Windows toolchains only when selection requires them, and never retry a failed test or build with another toolchain.

**Architecture:** Keep selection local to the existing `build.ps1` and `build-hf.ps1` scripts. Each script resolves its Go executable before running tests/builds, then uses that executable for one attempt; a focused integration test supplies fake `go.cmd` executables through `PATH` to verify selection and no-retry behavior without depending on installed archives.

**Tech Stack:** Windows PowerShell 5.1, batch-compatible fake Go commands, Go Windows ZIP archives, Pester-free assertion scripts, existing Go and Make regression suites.

---

### Task 1: Add failing PowerShell selection and no-retry tests

**Files:**
- Create: `tests/powershell_build_behavior.test.ps1`
- Modify: `tests/build.test.ps1`

- [ ] **Step 1: Create a temporary-project behavior test with a fake system Go**

Create `tests/powershell_build_behavior.test.ps1`. The test must:

1. Copy `build.ps1` and `build-hf.ps1` into a temporary `project/build/scripts/powershell` tree so their normal project-root calculation remains real.
2. Generate a `go.cmd` in a temporary directory and prepend that directory to `PATH`.
3. Log `test` and `build` calls to `FAKE_GO_LOG`; return a selectable version from `FAKE_GO_VERSION`; fail the selected command when `FAKE_GO_FAIL` matches it.
4. Restore `PATH`, `FAKE_GO_LOG`, `FAKE_GO_VERSION`, and `FAKE_GO_FAIL` in `finally` without recursively deleting the temporary directory.

Use these helpers and assertions:

```powershell
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
```

Exercise these cases in separate fixture copies/log resets:

```powershell
# Ordinary: arbitrary system Go succeeds without any compiler archive.
$env:FAKE_GO_VERSION = "go9.9.9"
$env:FAKE_GO_FAIL = ""
& (Join-Path $fixtureDirectory "build.ps1") -OutputDirectory (Join-Path $temporaryRoot "ordinary-success")
$calls = @(Get-Content -LiteralPath $fakeLog)
Assert-Equal $calls.Count 4 "Ordinary system Go did not run test plus three builds exactly once"
Assert-Equal (@($calls | Where-Object { $_ -eq "test" }).Count) 1 "Ordinary tests were not run once"
Assert-Equal (@($calls | Where-Object { $_ -eq "build" }).Count) 3 "Ordinary builds were not run once each"

# Ordinary: first build failure stops and does not retry with a bundled toolchain.
Clear-Content -LiteralPath $fakeLog
$env:FAKE_GO_FAIL = "build"
Invoke-ExpectedFailure {
    & (Join-Path $fixtureDirectory "build.ps1") -OutputDirectory (Join-Path $temporaryRoot "ordinary-failure")
} "GNSSAgent-CCU cross-compilation failed"
$calls = @(Get-Content -LiteralPath $fakeLog)
Assert-Equal $calls.Count 2 "Ordinary failure retried or continued after the first failed build"
Assert-Equal $calls[0] "test" "Ordinary failure did not test first"
Assert-Equal $calls[1] "build" "Ordinary failure did not stop on the first build"

# HF: exact system Go succeeds without an archive.
Clear-Content -LiteralPath $fakeLog
$env:FAKE_GO_VERSION = "go1.23.12"
$env:FAKE_GO_FAIL = ""
& (Join-Path $fixtureDirectory "build-hf.ps1") -OutputDirectory (Join-Path $temporaryRoot "hf-success")
$calls = @(Get-Content -LiteralPath $fakeLog)
Assert-Equal $calls.Count 1 "HF exact system Go did not build exactly once"
Assert-Equal $calls[0] "build" "HF exact system Go did not run build"

# HF: exact system Go failure stops without retry.
Clear-Content -LiteralPath $fakeLog
$env:FAKE_GO_FAIL = "build"
Invoke-ExpectedFailure {
    & (Join-Path $fixtureDirectory "build-hf.ps1") -OutputDirectory (Join-Path $temporaryRoot "hf-failure")
} "GNSSAgent-HF cross-compilation failed"
$calls = @(Get-Content -LiteralPath $fakeLog)
Assert-Equal $calls.Count 1 "HF retried after an exact system Go build failed"

# HF: wrong system version selects bundled Go before compilation.
Clear-Content -LiteralPath $fakeLog
$env:FAKE_GO_VERSION = "go9.9.9"
$env:FAKE_GO_FAIL = ""
$fixtureCompiler = Join-Path $temporaryRoot "project\build\compiler"
New-Item -ItemType Directory -Force -Path $fixtureCompiler | Out-Null
Set-Content -LiteralPath (Join-Path $fixtureCompiler "go1.23.12.windows-amd64.zip") `
    -Value "invalid archive" -Encoding ASCII
function Get-FileHash { throw "Get-FileHash is unavailable" }
Invoke-ExpectedFailure {
    & (Join-Path $fixtureDirectory "build-hf.ps1") -OutputDirectory (Join-Path $temporaryRoot "hf-wrong-version")
} "Go 1.23.12 archive checksum mismatch"
Assert-Equal (Get-Item -LiteralPath $fakeLog).Length 0 "HF built with a wrong system Go version"
```

End with:

```powershell
Write-Host "GNSSAgent PowerShell build behavior passed."
```

- [ ] **Step 2: Update the static build contract for the selected Windows archives**

In `tests/build.test.ps1`, replace the ordinary `go1.25.5` requirement with these required strings for `build.ps1`:

```powershell
'go1.26.4.windows-amd64.zip',
'3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345',
'Get-Command',
'System.Security.Cryptography.SHA256'
```

Require these strings in `build-hf.ps1`:

```powershell
'go1.23.12.windows-amd64.zip',
'07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc',
'go version go1.23.12 windows/amd64',
'Get-Command',
'System.Security.Cryptography.SHA256'
```

Keep all target, architecture, linker, forbidden-token, Makefile, and wrapper checks.
Also forbid `Get-FileHash` in both PowerShell build scripts so the `build.bat` path cannot depend on module auto-loading.

- [ ] **Step 3: Run the new tests and verify RED**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/powershell_build_behavior.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
```

Expected: both fail because the current scripts inspect missing bundled archives before resolving system Go and `build.ps1` still names bundled Go 1.25.5.

### Task 2: Implement single-attempt system/bundled selection

**Files:**
- Modify: `build/scripts/powershell/build.ps1`
- Modify: `build/scripts/powershell/build-hf.ps1`
- Test: `tests/powershell_build_behavior.test.ps1`
- Test: `tests/build.test.ps1`

- [ ] **Step 1: Update ordinary bundled-toolchain constants**

In `build.ps1`, replace the Go 1.25.5 paths with:

```powershell
$compilerArchive = Join-Path $projectRoot "build\compiler\go1.26.4.windows-amd64.zip"
$compilerArchiveSha256 = "3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345"
$compilerRoot = Join-Path $projectRoot "build\compiler\.go1.26.4-windows-amd64"
$bundledGoExe = Join-Path $compilerRoot "go\bin\go.exe"
```

Remove the unconditional archive/extraction block from the top of the script.

Add this module-independent checksum helper to both PowerShell build scripts:

```powershell
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
```

- [ ] **Step 2: Select ordinary system Go before bundled Go**

Inside the existing outer `try`, immediately after setting `GOTOOLCHAIN=local`, resolve and select Go once:

```powershell
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
```

Keep `Build-GNSSAgent` using the selected script-scope `$goExe`. Change the test failure to the version-independent message:

```powershell
throw "GNSSAgent tests failed"
```

Do not add a `catch` that switches toolchains.

- [ ] **Step 3: Select exact HF system Go or bundled Go before one build**

In `build-hf.ps1`, define:

```powershell
$compilerArchive = Join-Path $projectRoot "build\compiler\go1.23.12.windows-amd64.zip"
$compilerArchiveSha256 = "07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc"
$compilerRoot = Join-Path $projectRoot "build\compiler\.go1.23.12-windows-amd64"
$bundledGoExe = Join-Path $compilerRoot "go\bin\go.exe"
```

Remove unconditional archive preparation. Inside the outer `try`, set `GOTOOLCHAIN=local`, then select once:

```powershell
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
```

Keep the existing HF build as one invocation and keep the immediate `GNSSAgent-HF cross-compilation failed` error. Do not add retry logic.

- [ ] **Step 4: Run tests and verify GREEN**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/powershell_build_behavior.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
go test ./...
```

Expected:

- `GNSSAgent PowerShell build behavior passed.`
- `GNSSAgent build contract passed.`
- all Go packages pass.

- [ ] **Step 5: Commit the scripts and tests**

Run:

```powershell
git diff --check
git add -- build/scripts/powershell/build.ps1 build/scripts/powershell/build-hf.ps1 tests/build.test.ps1 tests/powershell_build_behavior.test.ps1
git commit -m "build: select PowerShell Go toolchains"
```

Expected: only the two scripts and two tests enter the implementation commit.

### Task 3: Download official Windows toolchains and run real builds

**Files:**
- Download/ignored: `build/compiler/go1.26.4.windows-amd64.zip`
- Download/ignored: `build/compiler/go1.23.12.windows-amd64.zip`
- Generated/ignored: `build/compiler/.go1.26.4-windows-amd64/`
- Generated/ignored: `build/compiler/.go1.23.12-windows-amd64/`
- Generated/ignored: `build/dist/bin/GNSSAgent-*`

- [ ] **Step 1: Download Go 1.26.4 for Windows atomically and verify before placement**

Download to the explicit temporary path `build/compiler/go1.26.4.windows-amd64.zip.download`, verify SHA256 `3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345`, then move it to `build/compiler/go1.26.4.windows-amd64.zip`. If verification fails, remove only that explicit `.download` file and stop.

```powershell
$archive = Join-Path $PWD "build\compiler\go1.26.4.windows-amd64.zip"
$download = "$archive.download"
$expected = "3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345"
if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
    Invoke-WebRequest -Uri "https://go.dev/dl/go1.26.4.windows-amd64.zip" -OutFile $download
    $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        Remove-Item -LiteralPath $download
        throw "Go 1.26.4 Windows archive checksum mismatch: $actual"
    }
    Move-Item -LiteralPath $download -Destination $archive
}
$actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "Existing Go 1.26.4 Windows archive checksum mismatch: $actual" }
```

- [ ] **Step 2: Download Go 1.23.12 for Windows atomically and verify before placement**

Download to the explicit temporary path `build/compiler/go1.23.12.windows-amd64.zip.download`, verify SHA256 `07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc`, then move it to `build/compiler/go1.23.12.windows-amd64.zip`. If verification fails, remove only that explicit `.download` file and stop.

```powershell
$archive = Join-Path $PWD "build\compiler\go1.23.12.windows-amd64.zip"
$download = "$archive.download"
$expected = "07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc"
if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
    Invoke-WebRequest -Uri "https://go.dev/dl/go1.23.12.windows-amd64.zip" -OutFile $download
    $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        Remove-Item -LiteralPath $download
        throw "Go 1.23.12 Windows archive checksum mismatch: $actual"
    }
    Move-Item -LiteralPath $download -Destination $archive
}
$actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "Existing Go 1.23.12 Windows archive checksum mismatch: $actual" }
```

- [ ] **Step 3: Verify system Go selection with the real ordinary build**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File build/scripts/powershell/build.ps1
```

Expected: if system Go exists, the script prints `Using system Go`, runs tests once, and builds CCU, MultibandRadio, and MultibandHandheld once each.

- [ ] **Step 4: Verify the bundled ordinary path**

Start a child PowerShell process whose `PATH` excludes the directory containing the current system `go.exe`, assert `Get-Command go` is absent in that child, then invoke `build.ps1` with a temporary output directory.

```powershell
$systemGoDirectories = @(Get-Command go -CommandType Application -All | ForEach-Object {
    (Split-Path -Parent $_.Source).TrimEnd('\')
})
$filteredPath = (($env:PATH -split ';') | Where-Object {
    $candidate = $_.TrimEnd('\')
    $_ -and -not @($systemGoDirectories | Where-Object {
        [string]::Equals($_, $candidate, [StringComparison]::OrdinalIgnoreCase)
    }).Count
}) -join ';'
$command = @'
$ErrorActionPreference = "Stop"
if (Get-Command go -CommandType Application -ErrorAction SilentlyContinue) {
    throw "System Go remains on PATH"
}
& "build\scripts\powershell\build.ps1" -OutputDirectory "build\dist\bundled-powershell"
'@
$previousPath = $env:PATH
$env:PATH = $filteredPath
try {
    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -Command $command
    if ($LASTEXITCODE -ne 0) { throw "Bundled ordinary build failed" }
} finally {
    $env:PATH = $previousPath
}
```

Expected: the archive checksum is accepted, bundled Go reports exactly `go version go1.26.4 windows/amd64`, tests pass, and all three ordinary binaries build.

- [ ] **Step 5: Verify the bundled HF path**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File build/scripts/powershell/build-hf.ps1
```

Expected: unless the installed system Go is exactly 1.23.12 Windows amd64, the script validates/extracts bundled Go 1.23.12 and builds HF once.

- [ ] **Step 6: Run the complete regression set and inspect repository state**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/powershell_build_behavior.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
wsl.exe sh -lc 'cd /mnt/d/GNSSAgent/.worktrees/gnss-agent-v1 && sh tests/make.test.sh'
go test ./...
git diff --check
git status --short
```

Expected: all tests pass, downloaded/extracted toolchains and outputs remain ignored, and the only untracked worktree path is the pre-existing `.codegraph/` directory.
