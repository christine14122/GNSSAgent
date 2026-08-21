# GNSSAgent SmallRadio Build Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a tested SmallRadio build target for the connected 32-bit little-endian hard-float MIPS device to the Make and PowerShell build paths.

**Architecture:** Reuse the ordinary system-Go-first build path with `GOOS=linux GOARCH=mipsle GOMIPS=hardfloat CGO_ENABLED=0`, add one thin Make wrapper, and extend the existing PowerShell build helper with one optional `GOMIPS` environment value. Keep runtime code and HF policy unchanged, then prove compatibility by executing the static binary from a temporary device path.

**Tech Stack:** GNU Make, POSIX shell, Windows PowerShell 5.1, Go 1.25.5/1.26.4, SSH/SCP, ELF `file`/`readelf` inspection.

---

### Task 1: Add failing SmallRadio build-contract tests

**Files:**
- Modify: `tests/build.test.ps1`
- Modify: `tests/make.test.sh`
- Modify: `tests/powershell_build_behavior.test.ps1`

- [ ] **Step 1: Extend the PowerShell contract test**

Add `makefile_SmallRadio` to the required paths and exact-case filename list in `tests/build.test.ps1`.

Require these strings from `build.ps1`:

```powershell
'GNSSAgent-SmallRadio',
'-GOARCH "mipsle"',
'-GOMIPS "hardfloat"',
'-Target "small-radio"'
```

Require these strings from the primary Makefile:

```powershell
'GNSSAgent-SmallRadio',
'GOARCH=mipsle',
'GOMIPS=hardfloat',
'gnssagent/internal/buildinfo.Target=small-radio'
```

Add the wrapper mapping:

```powershell
@{ Path = "makefile_SmallRadio"; Target = "small-radio" }
```

- [ ] **Step 2: Extend Make behavior tests**

In `tests/make.test.sh`:

- copy `makefile_SmallRadio` into every fixture;
- add this target assertion:

```sh
assert_target small-radio '.go1.26.4-linux-amd64' \
    'GOOS=linux GOARCH=mipsle GOMIPS=hardfloat' \
    'GNSSAgent-SmallRadio' small-radio
```

- extend the wrapper case loop with `small-radio) wrapper=makefile_SmallRadio`;
- include `makefile_SmallRadio` in install and whitespace-path loops;
- add `GNSSAgent-SmallRadio` to `artifact_names`;
- assert `makefile_SmallRadio clean` removes only `GNSSAgent-SmallRadio`.

- [ ] **Step 3: Extend PowerShell behavior logging**

Change the fake `go.cmd` in `tests/powershell_build_behavior.test.ps1` so a MIPSLE build logs its ABI:

```batch
if "%1"=="build" if "%GOARCH%"=="mipsle" (
  echo smallradio:%GOMIPS%>>"%FAKE_GO_LOG%"
) else (
  echo build>>"%FAKE_GO_LOG%"
)
if not "%1"=="build" echo %1>>"%FAKE_GO_LOG%"
```

Keep the existing failure switch after logging:

```batch
if "%FAKE_GO_FAIL%"=="%1" exit /b 1
```

Update the ordinary success assertions to expect five calls: one test, three existing builds, and exactly one `smallradio:hardfloat` call. Set `GOMIPS` to a sentinel before the build and assert it is restored afterward.

- [ ] **Step 4: Run tests and verify RED**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/powershell_build_behavior.test.ps1
wsl.exe sh -lc 'cd /mnt/d/GNSSAgent/.worktrees/gnss-agent-v1 && sh tests/make.test.sh'
```

Expected: failures report missing `makefile_SmallRadio`, missing `GNSSAgent-SmallRadio`, or an ordinary build-call count of four instead of five.

### Task 2: Implement the Make target and wrapper

**Files:**
- Modify: `build/scripts/make/makefile`
- Create: `build/scripts/make/makefile_SmallRadio`
- Test: `tests/build.test.ps1`
- Test: `tests/make.test.sh`

- [ ] **Step 1: Add SmallRadio to the primary makefile**

Add `small-radio` to `.PHONY`, the `build` prerequisite list, and define:

```make
small-radio: | $(DIST_DIR)

	$(call RUN_WITH_GO126_FALLBACK,CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=hardfloat,build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=small-radio" -o "$(DIST_DIR)/GNSSAgent-SmallRadio" $(MAIN_PACKAGE))
```

Add one explicit cleanup command:

```make
	rm -f "$(DIST_DIR)/GNSSAgent-SmallRadio"
```

- [ ] **Step 2: Add the thin wrapper**

Create `build/scripts/make/makefile_SmallRadio` following the exact current wrapper structure:

```make
ifneq ($(words $(MAKEFILE_LIST)),1)
$(error Makefile paths do not support whitespace)
endif
MAKEFILE_PATH := $(lastword $(MAKEFILE_LIST))
ifeq ($(filter /%,$(MAKEFILE_PATH)),)
ifneq ($(words $(CURDIR)),1)
$(error Makefile paths do not support whitespace)
endif
endif

MAKE_DIR := $(dir $(abspath $(MAKEFILE_PATH)))
PROJECT_ROOT := $(abspath $(MAKE_DIR)/../../..)
DIST_DIR := $(PROJECT_ROOT)/build/dist/bin
.DEFAULT_GOAL := all
.PHONY: all small-radio clean install

all: small-radio

small-radio:

	$(MAKE) --no-print-directory -f "$(MAKE_DIR)/makefile" small-radio

clean:

	rm -f "$(DIST_DIR)/GNSSAgent-SmallRadio"

install:
```

- [ ] **Step 3: Run Make behavior tests**

Run:

```powershell
wsl.exe sh -lc 'cd /mnt/d/GNSSAgent/.worktrees/gnss-agent-v1 && sh tests/make.test.sh'
```

Expected: the Make behavior test passes. The PowerShell contract and behavior tests remain red until Task 3 because `build.ps1` has only three ordinary build calls and no `GOMIPS` parameter.

### Task 3: Implement PowerShell MIPSLE support

**Files:**
- Modify: `build/scripts/powershell/build.ps1`
- Test: `tests/build.test.ps1`
- Test: `tests/powershell_build_behavior.test.ps1`

- [ ] **Step 1: Add `GOMIPS` to the build helper**

Extend `Build-GNSSAgent` parameters:

```powershell
[string]$GOMIPS = "",
```

Capture, set, and restore `$env:GOMIPS` beside `GOARM` and `GOARM64` in the helper:

```powershell
$previousGOMIPS = $env:GOMIPS
$env:GOMIPS = $GOMIPS
Restore-ProcessEnvironment "GOMIPS" $previousGOMIPS
```

- [ ] **Step 2: Isolate outer test/build environment**

Capture `$previousGOMIPS` before the outer `try`. Clear `GOMIPS` before `go test ./...`, and restore it in the outer `finally`, matching the existing `GOARM` and `GOARM64` treatment.

- [ ] **Step 3: Add the SmallRadio build call**

After the existing MultibandHandheld call, add:

```powershell
Build-GNSSAgent -Name "GNSSAgent-SmallRadio" -GOARCH "mipsle" -GOMIPS "hardfloat" -Target "small-radio"
```

- [ ] **Step 4: Run PowerShell and full unit tests**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/powershell_build_behavior.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
go test ./...
```

Expected: `GNSSAgent PowerShell build behavior passed.`, `GNSSAgent build contract passed.`, and all Go packages pass.

- [ ] **Step 5: Commit implementation and tests**

Run:

```powershell
git diff --check
git add -- build/scripts/make/makefile build/scripts/make/makefile_SmallRadio build/scripts/powershell/build.ps1 tests/build.test.ps1 tests/make.test.sh tests/powershell_build_behavior.test.ps1
git commit -m "build: add SmallRadio target"
```

Expected: only the Make, PowerShell, and test files enter this implementation commit.

### Task 4: Build and verify the SmallRadio artifact

**Files:**
- Generated/ignored: `build/dist/bin/GNSSAgent-SmallRadio`
- Temporary device file: `/tmp/GNSSAgent-SmallRadio-codex-verify`

- [ ] **Step 1: Build with the primary Make target**

Run:

```sh
make -f build/scripts/make/makefile small-radio
```

Expected: `build/dist/bin/GNSSAgent-SmallRadio` is produced with system Go or one bundled Go 1.26.4 fallback.

- [ ] **Step 2: Build with the wrapper**

Run:

```sh
make -f build/scripts/make/makefile_SmallRadio all
```

Expected: the wrapper delegates only to `small-radio` and succeeds.

- [ ] **Step 3: Build with PowerShell**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File build/scripts/powershell/build.ps1
```

Expected: tests pass and CCU, MultibandRadio, MultibandHandheld, and SmallRadio are built once with the selected Windows Go toolchain.

- [ ] **Step 4: Inspect the ELF**

Run in WSL:

```sh
file build/dist/bin/GNSSAgent-SmallRadio
readelf -h -A build/dist/bin/GNSSAgent-SmallRadio
```

Expected: a statically linked ELF32 little-endian MIPS executable suitable for MIPS32 hard-float execution.

- [ ] **Step 5: Verify execution on the connected device**

Copy the artifact with legacy SCP mode:

```powershell
scp -O -o BatchMode=yes -o ConnectTimeout=8 build/dist/bin/GNSSAgent-SmallRadio root@192.168.5.1:/tmp/GNSSAgent-SmallRadio-codex-verify
```

Then run a single SSH command that marks only that file executable, captures `--help`, removes the same explicit file regardless of the help exit status, and succeeds only when output contains `Usage of`.

```powershell
ssh -o BatchMode=yes -o ConnectTimeout=8 root@192.168.5.1 'path=/tmp/GNSSAgent-SmallRadio-codex-verify; chmod 700 "$path" || exit 1; output=$("$path" --help 2>&1); status=$?; printf "%s\n" "$output"; rm -f "$path"; case "$output" in *"Usage of"*) exit 0 ;; *) exit "$status" ;; esac'
ssh -o BatchMode=yes -o ConnectTimeout=8 root@192.168.5.1 'test ! -e /tmp/GNSSAgent-SmallRadio-codex-verify'
```

Expected: device output contains GNSSAgent usage text, and `test ! -e /tmp/GNSSAgent-SmallRadio-codex-verify` succeeds afterward.

- [ ] **Step 6: Run complete regression and state checks**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/powershell_build_behavior.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/architecture.test.ps1
go test ./...
wsl.exe sh -lc 'cd /mnt/d/GNSSAgent/.worktrees/gnss-agent-v1 && sh tests/make.test.sh'
git diff --check
git status --short
```

Expected: every test passes, generated outputs remain ignored, and the only untracked worktree path is the pre-existing `.codegraph/` directory.
