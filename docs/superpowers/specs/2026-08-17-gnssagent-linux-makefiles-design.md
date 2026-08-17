# GNSSAgent Linux Makefile 设计

## 目标

为 GNSSAgent 增加一套 Linux 主机上的可复现交叉编译入口，同时保留现有 Windows PowerShell 构建。一个主 Makefile 编译全部目标，四个薄封装 Makefile 分别只编译 CCU、MultibandRadio、MultibandHandheld 和 HF。

## 目录布局

```text
build/scripts/
├── powershell/
│   ├── build.ps1
│   ├── build-hf.ps1
│   └── build.bat
└── make/
    ├── Makefile
    ├── Makefile_CCU
    ├── Makefile_HF
    ├── Makefile_MultibandRadio
    └── Makefile_MultibandHandheld
```

现有 PowerShell 脚本移入 `powershell/`。脚本计算项目根目录的逻辑和 `tests/build.test.ps1` 中的路径同步更新；不修改其 Windows 工具链版本和产物行为。历史实施计划保留原文，不追改旧路径。

## Makefile 结构

`build/scripts/make/Makefile` 集中维护工具链、测试、构建、输出和清理逻辑。四个单目标 Makefile 只把默认目标转发给主 Makefile，不复制工具链逻辑，也不增加 `common.mk`。

主 Makefile 的默认目标为 `all`：先使用 Go 1.26.4 运行 `go test ./...`，再构建全部四个目标。单目标 Makefile 默认只编译对应产物，不隐式运行完整测试。

所有路径均由当前 Makefile 的绝对路径推导，因此可以从任意工作目录调用。产物写入 `build/dist/bin/`。

## 目标矩阵

| Make 目标 | 输出文件 | Go 版本 | GOOS/GOARCH | 额外参数 | 链接目标值 |
|---|---|---|---|---|---|
| `ccu` | `GNSSAgent-CCU` | 1.26.4 | `linux/amd64` | — | `ccu` |
| `multiband-radio` | `GNSSAgent-MultibandRadio` | 1.26.4 | `linux/arm64` | `GOARM64=v8.0` | `multiband-radio` |
| `multiband-handheld` | `GNSSAgent-MultibandHandheld` | 1.26.4 | `linux/arm` | `GOARM=7` | `multiband-handheld` |
| `hf` | `GNSSAgent-HF` | 1.23.12 | `linux/arm` | `GOARM=7` | `hf` |

所有构建设置 `CGO_ENABLED=0`、`GOTOOLCHAIN=local`，并使用 `-trimpath -buildvcs=false` 与 `-ldflags="-s -w -X gnssagent/internal/buildinfo.Target=<target>"`。

## 工具链与完整性

Makefile 只使用仓库 `build/compiler/` 下指定的 Linux amd64 官方归档，不接受系统中的其他 Go 版本：

- `go1.26.4.linux-amd64.tar.gz`，SHA-256 `1153d3d50e0ac764b447adfe05c2bcf08e889d42a02e0fe0259bd47f6733ad7f`
- `go1.23.12.linux-amd64.tar.gz`，SHA-256 `d3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90`

Go 1.26.4 使用用户提供的现有归档。Go 1.23.12 从 `https://go.dev/dl/go1.23.12.linux-amd64.tar.gz` 下载到同一目录。下载后先核对 SHA-256，再允许使用。

每个版本解压到独立、已忽略的缓存目录。Makefile 在首次使用前校验归档哈希，解压后再校验 `go version` 必须精确匹配预期的 `linux/amd64` 工具链。Linux `.tar.gz` 与已有 Windows `.zip` 一样保留为本地构建资产，不提交到 Git；`.gitignore` 增加对应规则。

## 清理与错误处理

- 归档缺失、哈希不符、解压结果不完整或工具链版本不符时立即失败，并输出明确路径和预期值。
- 不自动联网下载工具链；下载 Go 1.23.12 是本次一次性准备动作。
- `clean` 只逐个删除四个明确的输出文件，不递归删除目录、工具链缓存或归档。
- 单一目标失败时 Make 返回非零；`all` 不掩盖测试或任一构建失败。

## 验证

先扩展 `tests/build.test.ps1`，使其在实现前因新目录和 Makefile 缺失而失败，再实现最小改动令其通过。契约测试覆盖：

- PowerShell 脚本新路径仍满足原有构建契约；
- 五个 Makefile 均存在，四个薄封装映射到正确目标；
- 两个精确 Go 版本、归档名、校验值、目标架构和 `buildinfo.Target` 均存在；
- 不生成 `GNSSAgent-CCU-Audio`。

随后在 Linux/WSL 中运行 GNU Make 的解析检查、单目标构建和全部目标构建，并用 `file` 检查四个产物的 ELF 架构。最后运行完整 Go 测试以及现有 PowerShell 构建契约测试。
