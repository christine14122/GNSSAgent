# GNSSAgent PowerShell Go Selection Design

## Goal

Align the Windows PowerShell build scripts with the Makefile toolchain-selection policy while keeping PowerShell builds single-attempt:

- Ordinary tests and builds prefer any system Go version.
- If no system Go exists, ordinary tests and builds use bundled Go 1.26.4 for Windows amd64.
- HF accepts system Go only when it reports exactly `go version go1.23.12 windows/amd64`; otherwise it uses bundled Go 1.23.12 for Windows amd64.
- Once a toolchain is selected, a test or build failure is reported immediately. PowerShell never retries the command with another toolchain.

## Files and Scope

Modify only:

- `build/scripts/powershell/build.ps1`
- `build/scripts/powershell/build-hf.ps1`
- `tests/build.test.ps1` and, if needed, one focused PowerShell behavior test

Keep `build.bat`, Makefiles, target names, output paths, cross-compilation settings, linker metadata, and environment restoration unchanged.

Download these official archives into ignored `build/compiler` storage without committing them:

| Archive | SHA256 |
| --- | --- |
| `go1.26.4.windows-amd64.zip` | `3ca8fb4630b07c419cbdd51f754e31363cfcfb83b3a5354d9e895c90be2cc345` |
| `go1.23.12.windows-amd64.zip` | `07c35866cdd864b81bb6f1cfbf25ac7f87ddc3a976ede1bf5112acbb12dfe6dc` |

The source URLs are `https://go.dev/dl/<archive-name>` and the checksums come from the official Go download catalog.

## Ordinary Build Flow

`build.ps1` resolves `go` with `Get-Command` before inspecting the bundled archive.

1. If system Go exists, run `go version`, print the selected executable and version, and use that executable for `go test ./...` plus CCU, MultibandRadio, and MultibandHandheld builds.
2. Any system Go version is accepted. `GOTOOLCHAIN=local` prevents implicit toolchain downloads.
3. If system Go is absent, require the Go 1.26.4 Windows ZIP, validate its SHA256, extract it when the expected executable is absent, and verify the exact bundled version before use.
4. If tests or any build fail after selection, throw immediately. Do not switch from system Go to bundled Go, and do not rerun the failed command.

## HF Build Flow

`build-hf.ps1` checks system Go before inspecting the bundled archive.

1. If system Go reports exactly `go version go1.23.12 windows/amd64`, select it.
2. If system Go is absent, cannot report its version, or reports any other version/platform, select bundled Go 1.23.12.
3. Before bundled use, require the ZIP, validate SHA256, extract it when needed, and verify its exact version.
4. Run the HF build once. A build failure is reported immediately with no retry or toolchain switch.

Rejecting a wrong system version before the build is selection, not a compile-failure fallback.

## Archive Handling

- Validate each ZIP with `Get-FileHash -Algorithm SHA256` before extraction, including when an extracted directory already exists.
- Use `Expand-Archive -Force` only when the expected bundled `go.exe` is missing.
- Do not recursively delete any compiler directory.
- Verify the bundled executable's exact Windows version after extraction.
- A missing archive, checksum mismatch, extraction failure, or wrong bundled version stops before tests or compilation.

## Testing

PowerShell tests will cover these observable behaviors:

- An arbitrary fake system Go is selected by `build.ps1` even when the bundled archive is missing.
- A failed system-Go test or build stops immediately and does not inspect or invoke bundled Go.
- Exact fake system Go 1.23.12 is selected by `build-hf.ps1` without requiring the bundled archive.
- A failed exact-version HF system build stops immediately and does not retry.
- Contract checks assert the two archive names, official hashes, exact HF version requirement, and `GOTOOLCHAIN=local`.

Final verification will also use the downloaded official archives to exercise bundled selection and produce all four target binaries. Existing build and Makefile regression tests remain required.

## Non-Goals

- No shared PowerShell module or new public parameters.
- No WSL dependency for PowerShell builds.
- No compile retry, automatic online download from within build scripts, or support for additional bundled Go versions.
- No changes to deployment, runtime behavior, or output naming.
