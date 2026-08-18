# GNSSAgent Linux Makefiles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add reproducible Linux-hosted Make builds for all four GNSSAgent targets while preserving the existing Windows PowerShell build path.

**Architecture:** One primary Makefile owns exact Linux toolchain validation, tests, cross-compilation, output, and cleanup. Four thin Makefiles delegate one target each to the primary file. Existing Windows scripts move into a sibling directory and keep their current behavior.

**Tech Stack:** GNU Make, POSIX shell, Go 1.26.4 and Go 1.23.12 Linux amd64 toolchains, PowerShell contract tests, WSL/Linux verification.

---

## File map

- Modify: `.gitignore` — ignore local Linux Go archives alongside existing Windows archives and extracted caches.
- Modify: `tests/build.test.ps1` — enforce the new script layout and five-Makefile contract.
- Move/modify: `build/scripts/build.ps1` → `build/scripts/powershell/build.ps1` — preserve Windows main builds and fix project-root discovery after the move.
- Move/modify: `build/scripts/build-hf.ps1` → `build/scripts/powershell/build-hf.ps1` — preserve Windows HF build and fix project-root discovery after the move.
- Move: `build/scripts/build.bat` → `build/scripts/powershell/build.bat` — preserve the batch entry point; its same-directory script references remain valid.
- Create: `build/scripts/make/Makefile` — central Linux toolchain, test, build, and clean logic.
- Create: `build/scripts/make/Makefile_CCU` — CCU-only entry point.
- Create: `build/scripts/make/Makefile_HF` — HF-only entry point.
- Create: `build/scripts/make/Makefile_MultibandRadio` — MultibandRadio-only entry point.
- Create: `build/scripts/make/Makefile_MultibandHandheld` — MultibandHandheld-only entry point.
- Download locally: `build/compiler/go1.23.12.linux-amd64.tar.gz` — official HF Linux host toolchain; ignored by Git.

### Task 1: Write the failing build-layout contract

**Files:**
- Modify: `tests/build.test.ps1`

- [ ] **Step 1: Replace the build contract test with the new layout and Makefile assertions**

Use this complete content:

```powershell
$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$powerShellDirectory = Join-Path $projectRoot "build\scripts\powershell"
$makeDirectory = Join-Path $projectRoot "build\scripts\make"

$mainScript = Join-Path $powerShellDirectory "build.ps1"
$hfScript = Join-Path $powerShellDirectory "build-hf.ps1"
$batchScript = Join-Path $powerShellDirectory "build.bat"
$makefiles = [ordered]@{
    Makefile = Join-Path $makeDirectory "Makefile"
    Makefile_CCU = Join-Path $makeDirectory "Makefile_CCU"
    Makefile_HF = Join-Path $makeDirectory "Makefile_HF"
    Makefile_MultibandRadio = Join-Path $makeDirectory "Makefile_MultibandRadio"
    Makefile_MultibandHandheld = Join-Path $makeDirectory "Makefile_MultibandHandheld"
}

foreach ($path in @($mainScript, $hfScript, $batchScript) + @($makefiles.Values)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required build file is missing: $path"
    }
}

$main = Get-Content -LiteralPath $mainScript -Raw
$hf = Get-Content -LiteralPath $hfScript -Raw

foreach ($text in @(
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
)) {
    if (-not $main.Contains($text)) {
        throw "build.ps1 is missing required contract text: $text"
    }
}

foreach ($text in @(
    'go1.23.12',
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

$make = Get-Content -LiteralPath $makefiles.Makefile -Raw
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
    'GOARM64=v8.0',
    'GOARCH=arm',
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

$wrapperTargets = [ordered]@{
    Makefile_CCU = 'ccu'
    Makefile_HF = 'hf'
    Makefile_MultibandRadio = 'multiband-radio'
    Makefile_MultibandHandheld = 'multiband-handheld'
}
foreach ($name in $wrapperTargets.Keys) {
    $content = Get-Content -LiteralPath $makefiles[$name] -Raw
    if (-not $content.Contains('$(MAKE_DIR)/Makefile') -or
        -not $content.Contains($wrapperTargets[$name])) {
        throw "$name does not delegate to $($wrapperTargets[$name])"
    }
}

foreach ($script in @($main, $hf, $make)) {
    foreach ($forbidden in @('CCU-Audio', '--serial', '--baud')) {
        if ($script.Contains($forbidden)) {
            throw "Build file contains forbidden text: $forbidden"
        }
    }
}

Write-Host "GNSSAgent build contract passed."
```

- [ ] **Step 2: Run the contract test and verify the expected failure**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
```

Expected: FAIL with `Required build file is missing` for `build\scripts\powershell\build.ps1`. This proves the test observes the requested layout before implementation.

### Task 2: Move Windows scripts and implement the five Makefiles

**Files:**
- Modify: `.gitignore`
- Modify: `tests/build.test.ps1`
- Move/modify: `build/scripts/build.ps1` → `build/scripts/powershell/build.ps1`
- Move/modify: `build/scripts/build-hf.ps1` → `build/scripts/powershell/build-hf.ps1`
- Move: `build/scripts/build.bat` → `build/scripts/powershell/build.bat`
- Create: `build/scripts/make/Makefile`
- Create: `build/scripts/make/Makefile_CCU`
- Create: `build/scripts/make/Makefile_HF`
- Create: `build/scripts/make/Makefile_MultibandRadio`
- Create: `build/scripts/make/Makefile_MultibandHandheld`

- [ ] **Step 1: Move the Windows build files and update project-root discovery**

Move the three existing files into `build/scripts/powershell/`. In both PowerShell scripts, replace:

```powershell
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
```

with:

```powershell
$projectRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
```

Do not otherwise change the Windows Go 1.25.5/1.23.12 logic. `build.bat` keeps its current contents because `%~dp0build.ps1` and `%~dp0build-hf.ps1` still refer to siblings.

- [ ] **Step 2: Add the main Linux Makefile**

Create `build/scripts/make/Makefile` with exactly this content (recipe indentation is a tab):

```make
SHELL := /bin/sh

MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKE_DIR := $(dir $(MAKEFILE_PATH))
PROJECT_ROOT := $(abspath $(MAKE_DIR)/../../..)
COMPILER_DIR := $(PROJECT_ROOT)/build/compiler
DIST_DIR := $(PROJECT_ROOT)/build/dist/bin
MAIN_PACKAGE := ./cmd/gnssagent
GO_BUILD_FLAGS := -trimpath -buildvcs=false
GO_LINK_FLAGS := -s -w

GO126_ARCHIVE := $(COMPILER_DIR)/go1.26.4.linux-amd64.tar.gz
GO126_SHA256 := 1153d3d50e0ac764b447adfe05c2bcf08e889d42a02e0fe0259bd47f6733ad7f
GO126_ROOT := $(COMPILER_DIR)/.go1.26.4-linux-amd64
GO126_BIN := $(GO126_ROOT)/bin/go
GO126_STDLIB := $(GO126_ROOT)/src/errors/errors.go

GO123_ARCHIVE := $(COMPILER_DIR)/go1.23.12.linux-amd64.tar.gz
GO123_SHA256 := d3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90
GO123_ROOT := $(COMPILER_DIR)/.go1.23.12-linux-amd64
GO123_BIN := $(GO123_ROOT)/bin/go
GO123_STDLIB := $(GO123_ROOT)/src/errors/errors.go

CCU_OUT := $(DIST_DIR)/GNSSAgent-CCU
MULTIBAND_RADIO_OUT := $(DIST_DIR)/GNSSAgent-MultibandRadio
MULTIBAND_HANDHELD_OUT := $(DIST_DIR)/GNSSAgent-MultibandHandheld
HF_OUT := $(DIST_DIR)/GNSSAgent-HF

.DEFAULT_GOAL := all

.PHONY: all test build ccu multiband-radio multiband-handheld hf go126-toolchain go123-toolchain clean

all: test build

test: go126-toolchain
	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" "$(GO126_BIN)" test ./...

build: ccu multiband-radio multiband-handheld hf

go126-toolchain:
	@test -f "$(GO126_ARCHIVE)" || (echo "Go archive not found: $(GO126_ARCHIVE)" >&2; exit 1)
	@printf '%s  %s\n' "$(GO126_SHA256)" "$(GO126_ARCHIVE)" | sha256sum -c -
	@if [ ! -x "$(GO126_BIN)" ] || [ ! -f "$(GO126_STDLIB)" ]; then \
		echo "Extracting Go 1.26.4 to $(GO126_ROOT)"; \
		mkdir -p "$(GO126_ROOT)"; \
		tar -xzf "$(GO126_ARCHIVE)" --strip-components=1 -C "$(GO126_ROOT)"; \
	fi
	@version="$$($(GO126_BIN) version)"; \
		test "$$version" = "go version go1.26.4 linux/amd64" || \
		(echo "Expected go version go1.26.4 linux/amd64, got: $$version" >&2; exit 1)

go123-toolchain:
	@test -f "$(GO123_ARCHIVE)" || (echo "Go archive not found: $(GO123_ARCHIVE)" >&2; exit 1)
	@printf '%s  %s\n' "$(GO123_SHA256)" "$(GO123_ARCHIVE)" | sha256sum -c -
	@if [ ! -x "$(GO123_BIN)" ] || [ ! -f "$(GO123_STDLIB)" ]; then \
		echo "Extracting Go 1.23.12 to $(GO123_ROOT)"; \
		mkdir -p "$(GO123_ROOT)"; \
		tar -xzf "$(GO123_ARCHIVE)" --strip-components=1 -C "$(GO123_ROOT)"; \
	fi
	@version="$$($(GO123_BIN) version)"; \
		test "$$version" = "go version go1.23.12 linux/amd64" || \
		(echo "Expected go version go1.23.12 linux/amd64, got: $$version" >&2; exit 1)

$(DIST_DIR):
	mkdir -p "$(DIST_DIR)"

ccu: go126-toolchain $(DIST_DIR)
	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		"$(GO126_BIN)" build $(GO_BUILD_FLAGS) -ldflags="$(GO_LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=ccu" \
		-o "$(CCU_OUT)" $(MAIN_PACKAGE)

multiband-radio: go126-toolchain $(DIST_DIR)
	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0 \
		"$(GO126_BIN)" build $(GO_BUILD_FLAGS) -ldflags="$(GO_LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=multiband-radio" \
		-o "$(MULTIBAND_RADIO_OUT)" $(MAIN_PACKAGE)

multiband-handheld: go126-toolchain $(DIST_DIR)
	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
		"$(GO126_BIN)" build $(GO_BUILD_FLAGS) -ldflags="$(GO_LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=multiband-handheld" \
		-o "$(MULTIBAND_HANDHELD_OUT)" $(MAIN_PACKAGE)

hf: go123-toolchain $(DIST_DIR)
	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO123_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
		"$(GO123_BIN)" build $(GO_BUILD_FLAGS) -ldflags="$(GO_LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=hf" \
		-o "$(HF_OUT)" $(MAIN_PACKAGE)

clean:
	rm -f "$(CCU_OUT)"
	rm -f "$(MULTIBAND_RADIO_OUT)"
	rm -f "$(MULTIBAND_HANDHELD_OUT)"
	rm -f "$(HF_OUT)"
```

- [ ] **Step 3: Add the four thin target Makefiles**

Create `build/scripts/make/Makefile_CCU`:

```make
MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKE_DIR := $(dir $(MAKEFILE_PATH))
.DEFAULT_GOAL := ccu
.PHONY: ccu

ccu:
	$(MAKE) --no-print-directory -f "$(MAKE_DIR)/Makefile" ccu
```

Create `build/scripts/make/Makefile_HF`:

```make
MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKE_DIR := $(dir $(MAKEFILE_PATH))
.DEFAULT_GOAL := hf
.PHONY: hf

hf:
	$(MAKE) --no-print-directory -f "$(MAKE_DIR)/Makefile" hf
```

Create `build/scripts/make/Makefile_MultibandRadio`:

```make
MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKE_DIR := $(dir $(MAKEFILE_PATH))
.DEFAULT_GOAL := multiband-radio
.PHONY: multiband-radio

multiband-radio:
	$(MAKE) --no-print-directory -f "$(MAKE_DIR)/Makefile" multiband-radio
```

Create `build/scripts/make/Makefile_MultibandHandheld`:

```make
MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKE_DIR := $(dir $(MAKEFILE_PATH))
.DEFAULT_GOAL := multiband-handheld
.PHONY: multiband-handheld

multiband-handheld:
	$(MAKE) --no-print-directory -f "$(MAKE_DIR)/Makefile" multiband-handheld
```

- [ ] **Step 4: Ignore Linux toolchain archives**

Add this line directly after `/build/compiler/*.zip` in `.gitignore`:

```gitignore
/build/compiler/*.tar.gz
```

- [ ] **Step 5: Run the contract test and verify it passes**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
```

Expected: exit 0 and `GNSSAgent build contract passed.`

- [ ] **Step 6: Inspect the focused diff and commit the build entry points**

Run:

```powershell
git diff --check
git status --short
git diff -- .gitignore tests/build.test.ps1 build/scripts
git add -- .gitignore tests/build.test.ps1 build/scripts
git commit -m "build: add Linux Makefile targets"
```

Expected: only the intended ignore rule, contract test, moved Windows scripts, and five Makefiles are committed. Compiler archives, extracted toolchains, output binaries, and `.codegraph/` are not committed.

### Task 3: Download and validate the Go 1.23.12 Linux toolchain

**Files:**
- Download locally: `build/compiler/go1.23.12.linux-amd64.tar.gz`

- [ ] **Step 1: Download the official archive to a temporary explicit path**

Run from PowerShell:

```powershell
$archive = Join-Path (Get-Location) "build\compiler\go1.23.12.linux-amd64.tar.gz"
$download = "$archive.download"
New-Item -ItemType Directory -Force (Split-Path -Parent $archive) | Out-Null
curl.exe -fL "https://go.dev/dl/go1.23.12.linux-amd64.tar.gz" -o $download
if ($LASTEXITCODE -ne 0) { throw "Go 1.23.12 download failed" }
```

Expected: the `.download` file is approximately 70 MB and `curl.exe` exits 0.

- [ ] **Step 2: Verify SHA-256 and promote the archive**

Run:

```powershell
$expected = "d3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90"
$actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "Go 1.23.12 hash mismatch: $actual" }
Move-Item -LiteralPath $download -Destination $archive
Get-Item -LiteralPath $archive | Select-Object FullName, Length
```

Expected: hash equals `d3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90`; the final archive exists at `build/compiler/go1.23.12.linux-amd64.tar.gz`.

- [ ] **Step 3: Verify the user-provided Go 1.26.4 archive**

Run:

```powershell
$go126 = "build\compiler\go1.26.4.linux-amd64.tar.gz"
$expected126 = "1153d3d50e0ac764b447adfe05c2bcf08e889d42a02e0fe0259bd47f6733ad7f"
$actual126 = (Get-FileHash -LiteralPath $go126 -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual126 -ne $expected126) { throw "Go 1.26.4 hash mismatch: $actual126" }
```

Expected: exit 0. No compiler archive is added to Git.

### Task 4: Verify Linux parsing, toolchains, builds, and outputs

**Files:**
- Generated, ignored: `build/compiler/.go1.26.4-linux-amd64/`
- Generated, ignored: `build/compiler/.go1.23.12-linux-amd64/`
- Generated, ignored: `build/dist/bin/GNSSAgent-*`

- [ ] **Step 1: Verify GNU Make can parse all five entry points from a different working directory**

Run in Linux/WSL from the repository root path mounted in Linux:

```sh
make -n -f build/scripts/make/Makefile test
make -n -f build/scripts/make/Makefile_CCU
make -n -f build/scripts/make/Makefile_HF
make -n -f build/scripts/make/Makefile_MultibandRadio
make -n -f build/scripts/make/Makefile_MultibandHandheld
```

Expected: all commands exit 0 and print the correct archive, architecture, output, and target commands without executing them.

- [ ] **Step 2: Build each single-target entry point**

Run:

```sh
make -f build/scripts/make/Makefile_CCU
make -f build/scripts/make/Makefile_MultibandRadio
make -f build/scripts/make/Makefile_MultibandHandheld
make -f build/scripts/make/Makefile_HF
```

Expected: archives pass `sha256sum`, Go 1.26.4 and 1.23.12 extract into separate caches, and all four commands exit 0.

- [ ] **Step 3: Run the default all-target build including tests**

Run:

```sh
make -f build/scripts/make/Makefile
```

Expected: `go test ./...` passes under Go 1.26.4 and all four target builds exit 0.

- [ ] **Step 4: Verify output architectures**

Run:

```sh
file build/dist/bin/GNSSAgent-CCU
file build/dist/bin/GNSSAgent-MultibandRadio
file build/dist/bin/GNSSAgent-MultibandHandheld
file build/dist/bin/GNSSAgent-HF
```

Expected:

- `GNSSAgent-CCU`: ELF 64-bit x86-64.
- `GNSSAgent-MultibandRadio`: ELF 64-bit ARM aarch64.
- `GNSSAgent-MultibandHandheld`: ELF 32-bit ARM.
- `GNSSAgent-HF`: ELF 32-bit ARM.

- [ ] **Step 5: Run fresh final verification and inspect repository state**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
git diff --check
git status --short
```

Run in Linux/WSL:

```sh
make -f build/scripts/make/Makefile
```

Expected: contract test and Linux all-target build both exit 0. Git shows no unexpected tracked changes; `.codegraph/`, compiler archives/caches, and build outputs remain untracked or ignored as designed.
