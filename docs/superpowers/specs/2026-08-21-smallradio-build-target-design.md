# GNSSAgent SmallRadio Build Target Design

> **状态：历史设计。** 已生效的 SmallRadio 构建要求已合入 [`GNSSAgent-Requirements.md`](./GNSSAgent-Requirements.md) 第 4 节；本文仅用于追溯。

## Goal

Add SmallRadio as a maintained GNSSAgent build target in the Linux Make and Windows PowerShell build paths, producing `build/dist/bin/GNSSAgent-SmallRadio` for the connected device.

## Verified Device Profile

The device at `192.168.5.1` was inspected over SSH without modifying it. Its executable ABI is:

- SoC: Ingenic X2000 / XBurst II
- Kernel: Linux 4.4.94
- Distribution: Buildroot 2020.02.1
- ELF class: 32-bit
- Endianness: little-endian
- ISA: MIPS32r2
- ABI: o32
- Floating point: hard float, 64-bit FPU

The corresponding Go target is:

```text
GOOS=linux GOARCH=mipsle GOMIPS=hardfloat CGO_ENABLED=0
```

Both the available Go 1.25.5 system toolchain and bundled Go 1.26.4 toolchain list `linux/mipsle` as a supported target.

## Build Identity and Output

- Make target: `small-radio`
- Build metadata: `gnssagent/internal/buildinfo.Target=small-radio`
- Output filename: `GNSSAgent-SmallRadio`
- Output directory: `build/dist/bin`

The build identity changes only diagnostic/build metadata. Configuration defaults are target-independent, so no runtime configuration code changes are required.

## Make Changes

The primary `build/scripts/make/makefile` will:

- declare `small-radio` phony;
- build it with the ordinary system-Go-first policy;
- retry once with bundled Go 1.26.4 if the system Go command fails;
- include it in `build` and therefore in `all`;
- remove `GNSSAgent-SmallRadio` from `clean`.

Add `build/scripts/make/makefile_SmallRadio` with the same wrapper contract as the existing device-specific Makefiles:

- `all` delegates to `small-radio` in the primary makefile;
- `clean` removes only `GNSSAgent-SmallRadio`;
- `install` remains an empty phony target.

## PowerShell Changes

`build/scripts/powershell/build.ps1` will extend `Build-GNSSAgent` with an optional `GOMIPS` parameter and restore the prior process environment after each command and after the script finishes.

The ordinary PowerShell build will add:

```powershell
Build-GNSSAgent -Name "GNSSAgent-SmallRadio" -GOARCH "mipsle" -GOMIPS "hardfloat" -Target "small-radio"
```

The existing PowerShell policy remains unchanged:

- any available system Go is selected first;
- bundled Windows Go 1.26.4 is selected only when no system Go exists;
- once selected, tests and builds run once;
- a failed test or build is reported immediately without switching toolchains.

`build.bat` requires no change because it already invokes `build.ps1`.

## Tests

Update the existing test suites to cover:

- exact presence and casing of `makefile_SmallRadio`;
- Make dry-run parameters `GOARCH=mipsle GOMIPS=hardfloat`;
- output name and build metadata;
- wrapper `all`, `clean`, and empty `install` behavior;
- SmallRadio inclusion in primary `all` and `clean`;
- PowerShell `GOMIPS` selection and environment restoration;
- four ordinary PowerShell build calls instead of three;
- existing targets, Go selection, fallback, HF single-attempt behavior, concurrency, and path validation.

## Verification

After automated tests pass:

1. Build `GNSSAgent-SmallRadio` through the primary Make target.
2. Build it through `makefile_SmallRadio`.
3. Build it through PowerShell `build.ps1`.
4. Use `file` and `readelf` to confirm a static 32-bit little-endian MIPS executable with a hard-float-compatible MIPS ABI.
5. Copy the verified artifact to a unique file under `/tmp` on `192.168.5.1`.
6. Run that temporary file with `--help` to verify the kernel can execute it.
7. Remove only that explicit temporary device file.

No persistent installation, service modification, configuration change, or device reboot is in scope.

## Acceptance Criteria

- `make -f build/scripts/make/makefile all` produces all five device binaries.
- `make -f build/scripts/make/makefile_SmallRadio all` produces only the SmallRadio binary.
- PowerShell `build.ps1` produces the three existing ordinary binaries plus SmallRadio.
- `GNSSAgent-SmallRadio` runs successfully on the connected SmallRadio device.
- All existing Make, PowerShell, architecture, and Go tests remain green.
