# GNSSAgent System Go Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prefer any available system Go for tests and non-HF builds, retry once with bundled Go 1.26.4 on failure, and keep HF on a single exact Go 1.23.12 attempt.

**Architecture:** The primary Makefile detects system Go once at parse time and uses two private recipe macros. The ordinary macro tries system Go and conditionally prepares/retries with the existing bundled Go 1.26.4; the HF macro selects exact system Go 1.23.12 or bundled Go 1.23.12 before one build attempt. Existing archive integrity, locking, stamps, wrappers, outputs, and cleanup remain unchanged.

**Tech Stack:** GNU Make, POSIX shell, Go toolchains, shell behavior tests, PowerShell contract tests, WSL.

---

### Task 1: Add failing selection and fallback behavior tests

**Files:**
- Modify: `tests/make.test.sh`

- [ ] **Step 1: Extend the fake bundled Go to log and optionally fail test/build commands**

In `make_archive`, replace the generated fake Go `test` and `build` cases with:

```sh
        'test) [ -z "${FAKE_EVENT_LOG:-}" ] || printf bundled-test\\n >> "$FAKE_EVENT_LOG"; sleep "${FAKE_TEST_SLEEP:-0}"; [ "${FAKE_TEST_FAIL:-0}" = 0 ] || exit 1 ;;' \
        'build) [ -z "${FAKE_EVENT_LOG:-}" ] || printf bundled-build\\n >> "$FAKE_EVENT_LOG"; [ "${FAKE_BUILD_FAIL:-0}" = 0 ] || exit 1 ;;' \
```

Update the existing barrier assertion from `^build$` to `^bundled-build$`.

- [ ] **Step 2: Add a controllable fake system Go helper**

Add after `make_archive`:

```sh
make_system_go() {
    directory=$1
    version=$2
    mkdir -p "$directory"
    printf '%s\n' '#!/bin/sh' \
        'case "$1" in' \
        "version) printf '%s\\n' 'go version $version linux/amd64' ;;" \
        'test) printf system-test\\n >> "${FAKE_SYSTEM_LOG:?}"; [ "${FAKE_SYSTEM_FAIL:-0}" = 0 ] ;;' \
        'build) printf system-build\\n >> "${FAKE_SYSTEM_LOG:?}"; [ "${FAKE_SYSTEM_FAIL:-0}" = 0 ] ;;' \
        '*) exit 1 ;;' \
        'esac' > "$directory/go"
    chmod +x "$directory/go"
}
```

- [ ] **Step 3: Add failing ordinary-target cases**

After archive hashes are calculated, add tests using command-line `SYSTEM_GO` overrides:

```sh
system_bin="$tmp_dir/system-bin"
system_log="$tmp_dir/system.log"
bundled_log="$tmp_dir/bundled.log"
make_system_go "$system_bin" go9.9.9

: > "$system_log"
FAKE_SYSTEM_LOG="$system_log" FAKE_SYSTEM_FAIL=0 \
    make -f "$make_dir/Makefile" SYSTEM_GO="$system_bin/go" \
    GO126_ARCHIVE="$tmp_dir/not-needed.tar.gz" test
[ "$(grep -c '^system-test$' "$system_log")" = 1 ] || fail 'system Go success did not run exactly once'

: > "$system_log"
: > "$bundled_log"
FAKE_SYSTEM_LOG="$system_log" FAKE_SYSTEM_FAIL=1 FAKE_EVENT_LOG="$bundled_log" \
    run_make "$tmp_dir/fallback126" "$tmp_dir/fallback123" SYSTEM_GO="$system_bin/go" ccu
[ "$(grep -c '^system-build$' "$system_log")" = 1 ] || fail 'system Go failure was not attempted once'
[ "$(grep -c '^bundled-build$' "$bundled_log")" = 1 ] || fail 'bundled Go fallback was not attempted once'

: > "$system_log"
: > "$bundled_log"
if FAKE_SYSTEM_LOG="$system_log" FAKE_SYSTEM_FAIL=1 FAKE_EVENT_LOG="$bundled_log" FAKE_BUILD_FAIL=1 \
    run_make "$tmp_dir/fail126" "$tmp_dir/fail123" SYSTEM_GO="$system_bin/go" ccu; then
    fail 'two failed ordinary Go attempts were accepted'
fi
[ "$(grep -c '^system-build$' "$system_log")" = 1 ] || fail 'failed system Go ran more than once'
[ "$(grep -c '^bundled-build$' "$bundled_log")" = 1 ] || fail 'failed bundled Go ran more than once'

: > "$bundled_log"
FAKE_EVENT_LOG="$bundled_log" run_make "$tmp_dir/no-system126" "$tmp_dir/no-system123" SYSTEM_GO= ccu
[ "$(grep -c '^bundled-build$' "$bundled_log")" = 1 ] || fail 'missing system Go did not use bundled Go'
```

Add an equivalent system-test-fails/bundled-test-succeeds case for `test`, asserting one `system-test` and one `bundled-test` event.

- [ ] **Step 4: Add failing HF cases**

```sh
hf_exact_bin="$tmp_dir/hf-exact-bin"
hf_wrong_bin="$tmp_dir/hf-wrong-bin"
make_system_go "$hf_exact_bin" go1.23.12
make_system_go "$hf_wrong_bin" go1.26.4

: > "$system_log"
FAKE_SYSTEM_LOG="$system_log" FAKE_SYSTEM_FAIL=0 \
    make -f "$make_dir/Makefile" SYSTEM_GO="$hf_exact_bin/go" \
    GO123_ARCHIVE="$tmp_dir/not-needed-hf.tar.gz" hf
[ "$(grep -c '^system-build$' "$system_log")" = 1 ] || fail 'HF exact system Go was not used once'

: > "$system_log"
: > "$bundled_log"
if FAKE_SYSTEM_LOG="$system_log" FAKE_SYSTEM_FAIL=1 FAKE_EVENT_LOG="$bundled_log" \
    run_make "$tmp_dir/hf-no-retry126" "$tmp_dir/hf-no-retry123" SYSTEM_GO="$hf_exact_bin/go" hf; then
    fail 'HF accepted a failed exact system Go build'
fi
[ ! -s "$bundled_log" ] || fail 'HF retried after exact system Go failed'

: > "$system_log"
: > "$bundled_log"
FAKE_SYSTEM_LOG="$system_log" FAKE_EVENT_LOG="$bundled_log" \
    run_make "$tmp_dir/hf-wrong126" "$tmp_dir/hf-wrong123" SYSTEM_GO="$hf_wrong_bin/go" hf
[ ! -s "$system_log" ] || fail 'HF built with a non-1.23.12 system Go'
[ "$(grep -c '^bundled-build$' "$bundled_log")" = 1 ] || fail 'HF wrong system version did not select bundled Go'
```

Also cover `SYSTEM_GO=` for HF, expecting bundled Go exactly once.

- [ ] **Step 5: Run the behavior test and verify RED**

Run:

```powershell
wsl.exe sh -lc 'cd /mnt/d/GNSSAgent/.worktrees/gnss-agent-v1 && sh tests/make.test.sh'
```

Expected: FAIL because current `test`, ordinary builds, and HF unconditionally prepare/use bundled toolchains and ignore `SYSTEM_GO`.

### Task 2: Implement system Go selection and one-time fallback

**Files:**
- Modify: `build/scripts/make/Makefile`

- [ ] **Step 1: Add system Go detection and a non-special recursive Make alias**

Add after `LINK_FLAGS`:

```make
SYSTEM_GO ?= $(shell command -v go 2>/dev/null)
MAKE_RUNNER := $(MAKE)
```

`MAKE_RUNNER` deliberately avoids GNU Make treating dry-run recipe lines as recursive `$(MAKE)` lines; `make -n` must print selection/fallback logic without executing it.

- [ ] **Step 2: Add the ordinary command macro**

Add before `.DEFAULT_GOAL`:

```make
define RUN_WITH_GO126_FALLBACK
	@system_go="$(SYSTEM_GO)"; \
	if [ -n "$$system_go" ]; then \
		version="$$($$system_go version 2>&1)" || version="unavailable"; \
		echo "Using system Go: $$system_go ($$version)"; \
		if cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local $(1) "$$system_go" $(2); then \
			exit 0; \
		fi; \
		echo "System Go command failed; retrying with bundled Go 1.26.4" >&2; \
	else \
		echo "System Go not found; using bundled Go 1.26.4"; \
	fi; \
	"$(MAKE_RUNNER)" --no-print-directory -f "$(MAKE_DIR)/Makefile" go126 || exit $$?; \
	if cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" $(1) "$(GO126)" $(2); then \
		exit 0; \
	else \
		status=$$?; \
		echo "Bundled Go 1.26.4 command failed" >&2; \
		exit $$status; \
	fi
endef
```

Quote `$$system_go` in the version command as `"$$system_go"` in the actual implementation so paths cannot be split.

- [ ] **Step 3: Add the HF selection macro**

```make
define RUN_HF_ONCE
	@system_go="$(SYSTEM_GO)"; \
	version=""; \
	if [ -n "$$system_go" ]; then version="$$($$system_go version 2>&1)" || version="unavailable"; fi; \
	if [ "$$version" = "go version go1.23.12 linux/amd64" ]; then \
		echo "Using system Go for HF: $$system_go ($$version)"; \
		cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
			"$$system_go" build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=hf" \
			-o "$(DIST_DIR)/GNSSAgent-HF" $(MAIN_PACKAGE); \
	else \
		if [ -n "$$system_go" ]; then \
			echo "System Go is not go1.23.12 linux/amd64 ($$version); using bundled Go 1.23.12"; \
		else \
			echo "System Go not found; using bundled Go 1.23.12"; \
		fi; \
		"$(MAKE_RUNNER)" --no-print-directory -f "$(MAKE_DIR)/Makefile" go123 || exit $$?; \
		cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO123_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
			"$(GO123)" build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=hf" \
			-o "$(DIST_DIR)/GNSSAgent-HF" $(MAIN_PACKAGE); \
	fi
endef
```

Quote the system Go executable in its version command in the actual implementation.

- [ ] **Step 4: Replace unconditional toolchain prerequisites and recipes**

Use these target shapes:

```make
test:

	$(call RUN_WITH_GO126_FALLBACK,,test ./...)

ccu: | $(DIST_DIR)

	$(call RUN_WITH_GO126_FALLBACK,CGO_ENABLED=0 GOOS=linux GOARCH=amd64,build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=ccu" -o "$(DIST_DIR)/GNSSAgent-CCU" $(MAIN_PACKAGE))

multiband-radio: | $(DIST_DIR)

	$(call RUN_WITH_GO126_FALLBACK,CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0,build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=multiband-radio" -o "$(DIST_DIR)/GNSSAgent-MultibandRadio" $(MAIN_PACKAGE))

multiband-handheld: | $(DIST_DIR)

	$(call RUN_WITH_GO126_FALLBACK,CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7,build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=multiband-handheld" -o "$(DIST_DIR)/GNSSAgent-MultibandHandheld" $(MAIN_PACKAGE))

hf: | $(DIST_DIR)

	$(RUN_HF_ONCE)
```

Keep `go126`, `go123`, `all`, `build`, output directory, checksums, locks, stamps, version recovery, wrappers, and `clean` otherwise unchanged.

- [ ] **Step 5: Run tests and verify GREEN**

Run:

```powershell
wsl.exe sh -lc 'cd /mnt/d/GNSSAgent/.worktrees/gnss-agent-v1 && sh tests/make.test.sh'
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File tests/build.test.ps1
```

Expected: both exit 0 with `GNSSAgent Make behavior passed.` and `GNSSAgent build contract passed.`

- [ ] **Step 6: Commit the implementation**

Run:

```powershell
git diff --check
git add -- build/scripts/make/Makefile tests/make.test.sh
git commit -m "build: prefer system Go with fallback"
```

Expected: only the primary Makefile and behavior test are committed.

### Task 3: Run real toolchain and artifact verification

**Files:**
- Generated/ignored: `build/compiler/.go*/`
- Generated/ignored: `build/dist/bin/GNSSAgent-*`

- [ ] **Step 1: Verify a real system-Go success does not require the bundled Go 1.26.4 archive**

In WSL, temporarily override `GO126_ARCHIVE` to a nonexistent path and run `test` with the detected system Go:

```sh
make -f build/scripts/make/Makefile GO126_ARCHIVE=/nonexistent/go1.26.4.tar.gz test
```

Expected: if WSL has a working system Go, tests pass without touching the missing archive. If no system Go exists, record that this real-path check is not applicable; the fake behavior test remains authoritative.

- [ ] **Step 2: Run real default and individual builds**

Run:

```sh
make -f build/scripts/make/Makefile
make -f build/scripts/make/Makefile_CCU
make -f build/scripts/make/Makefile_MultibandRadio
make -f build/scripts/make/Makefile_MultibandHandheld
make -f build/scripts/make/Makefile_HF
```

Expected: ordinary targets either use a working system Go or fall back once; HF uses exact system Go 1.23.12 only if present, otherwise bundled Go 1.23.12. All commands exit 0.

- [ ] **Step 3: Verify output architectures and repository state**

Run:

```sh
file build/dist/bin/GNSSAgent-CCU \
     build/dist/bin/GNSSAgent-MultibandRadio \
     build/dist/bin/GNSSAgent-MultibandHandheld \
     build/dist/bin/GNSSAgent-HF
```

Run in PowerShell:

```powershell
git diff --check
git status --short
```

Expected: CCU is x86-64, Radio is aarch64, Handheld/HF are ARM EABI5; no tracked changes remain beyond the committed implementation and the known untracked `.codegraph/`.
