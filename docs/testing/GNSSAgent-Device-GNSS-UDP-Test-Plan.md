# 设备侧 GNSS UDP 消息测试方案

- 文档版本：1.1（TCP v2，2026-09-30）
- 日期：2026-08-19
- 适用目标：CCU、HF、MultibandRadio、MultibandHandheld
- 被测链路：GNSS UART → 设备 UART 唯一读写进程 → UDP/29501 → GNSSAgent → TCP 状态协议

## 1. 测试目的

验证设备侧应用转发给 GNSSAgent 的 GNSS 消息满足以下要求：

1. UDP 地址、端口、来源和发送频率正确。
2. 一个 UDP 数据报恰好包含一条完整、未经修改的 NMEA 语句。
3. 无 NUL、截断、拼包、拆包、乱序、重复、丢失和 checksum 错误。
4. GNSSAgent 能持续接收、校验、解析和聚合消息。
5. SIMPLE/FULL TCP 状态帧的有效位、字段值和发送频率正确。
6. 启动顺序变化、上游短时中断和长时间运行后能够正常恢复。

协议依据：

- `docs/protocol/GNSSAgent-UDP-NMEA-Protocol-v1.md`
- `docs/protocol/GNSSAgent-Binary-Protocol-v2.md`

## 2. 测试角色与边界

| 角色 | 职责 |
|---|---|
| GNSS 模块 | 从 UART 输出原始 NMEA |
| UART 唯一读写进程 | 按 UART 顺序逐句、原样转发到 UDP/29501 |
| GNSSAgent | 接收 UDP，校验、解析、聚合并发布 TCP 状态 |
| 测试主机 | SSH 控制、同步采集证据、解析数据并保存报告 |

正式验收必须同时观察 UART、UDP 和 GNSSAgent 接收点。只抓 UDP 可以证明 UDP payload 已经错误，但不能区分错误来自 GNSS 模块还是转发应用。

## 3. 测试环境与前置条件

测试前记录：

| 项目 | 记录内容 |
|---|---|
| 设备型号/序列号 | 例如 MultibandRadio / SN |
| 软件版本 | 固件、UART 进程、GNSSAgent SHA-256/版本 |
| 网络 | 设备 IP、测试主机 IP、UDP/TCP 端口 |
| GNSS 配置 | 模式、波特率、NMEA 输出类型和频率 |
| UART | 设备节点、UART 进程 PID 和 fd |
| 天线与环境 | 天线状态、室内/室外、是否预期定位 |
| 测试时间 | 开始、结束、设备 UTC/本地时间 |

前置条件：

- UART 只由设备现有唯一读写进程持有。
- GNSSAgent 已启动并监听 UDP/TCP 29501。
- 设备存在 `strace`、`tcpdump`、`awk`、`grep`、`date`。
- 测试主机可运行 Python 3、SSH 和本仓库 `tools/gnss_survey.py`。
- 正式采集期间不要改变 GNSS 模式、天线或系统时间。
- 重启、停进程、断开上游等恢复性测试只在获批的测试窗口执行。

## 4. 总体验收判据

以下任一项不满足即判定设备侧 GNSS UDP 转发不通过：

- UDP 数据报数量必须大于 0。
- UART 完整 NMEA 与 UDP 数据报逐句一致。
- `loss_count = 0`、`duplicate_count = 0`、`reorder_count = 0`。
- NUL、空包、非 `$` 起始、超长、截断、拼包和非法换行数量全部为 0。
- checksum 缺失、格式错误和校验失败数量全部为 0。
- tcpdump、GNSSAgent socket 和 UART 的内核丢包/错误增量全部为 0。
- 捕获期间 UART 进程和 GNSSAgent 不得重启。
- SIMPLE/FULL 订阅 ACK 成功，帧长和字段布局符合 TCP v2 协议；UDP NMEA 输入仍为 v1。
- 有效位为 1 的字段必须格式正确、范围正确；有效位为 0 的字段载荷必须为 0。
- 长稳测试期间不得持续产生 UDP reject、checksum failure 或 parser failure。

转发延迟必须记录 p50、p95、p99 和最大值。若产品尚未批准固定上限，本轮只记录数据，不自行编造通过阈值；发布前必须由产品需求给出可接受上限。

## 5. 测试用例

### GNSS-UDP-001：进程与端口确认

步骤：

1. 确认 UART 进程和 GNSSAgent 均在运行。
2. 确认 UDP/29501 由 GNSSAgent 持有。
3. 确认 TCP/29501 由 GNSSAgent 监听。
4. 确认 UDP 发送 socket 属于预期 UART 进程。

设备侧参考命令：

```sh
ps w | grep -E '[G]NSSAgent|[R]adioApp'
grep ':733D ' /proc/net/udp
grep ':733D ' /proc/net/tcp /proc/net/tcp6 2>/dev/null
```

`733D` 是十进制端口 29501 的十六进制表示。根据 `/proc/net/udp` 第 10 列 inode 查询进程：

```sh
ls -l /proc/[0-9]*/fd/* 2>/dev/null | grep 'socket:\[<inode>\]'
```

通过条件：端口归属与设计一致，没有第二个进程错误占用 GNSSAgent 的 UDP 监听端口。

### GNSS-UDP-002：快速实时抓包

步骤：

```sh
timeout 10 tcpdump -ni lo -s 0 -XX 'udp dst port 29501'
```

若发送方不是本机，按实际接口替换 `lo`。

人工检查：

- 目的端口为 29501。
- payload 以 `24`（ASCII `$`）开始。
- payload 以 `*HH` 结束，可附带 `0A` 或 `0D 0A`。
- NMEA payload 中不得出现 `00`。
- 一个包中不得出现第二个 `24`。

通过条件：持续有数据且未发现明显格式错误。此用例只作冒烟检查，不能替代自动化正式验收。

### GNSS-UDP-003：UDP 数据报契约检查

至少连续采集 10 分钟，逐包自动检查：

1. 长度为 1～1024 字节。
2. 第一个字节是 `$`。
3. payload 内不存在 NUL。
4. payload 只有一个 `$`。
5. 一包一条 NMEA，不拆包、不拼包。
6. 只允许无换行、LF 或 CRLF 结尾。
7. `*HH` 位于语句结尾且 `HH` 为两个十六进制字符。
8. XOR checksum 与 `HH` 完全一致。
9. NMEA identifier 为 5 个字符且属于目标设备批准的语句集合。

必须分别统计：总包数、总字节数、长度分布、源端口、语句类型、talker、终止符类型和每种拒绝原因。错误样例必须保存原始十六进制，不能只保存转换后的文本。

通过条件：所有错误计数均为 0。

### GNSS-UDP-004：UART 到 UDP 逐字节一致性

使用仓库已有同步验收工具：

```powershell
python tools/gnss_survey.py `
  --tee-verify `
  --target MultibandRadio `
  --host <设备IP> `
  --user <SSH用户> `
  --password <SSH密码> `
  --uart-device <GNSS_UART设备> `
  --tty-counter-path <UART统计文件> `
  --udp-port 29501 `
  --seconds 600 `
  --output-dir evidence/gnss-tee
```

工具会同步采集：

- UART 进程 `read()` 返回的原始字节；
- GNSSAgent `recvfrom/recvmsg()` 收到的字节；
- UDP pcap；
- tcpdump 丢包统计；
- UART 测试前后统计；
- 进程启动时间和采集器状态；
- UART→GNSSAgent 转发延迟。

重点检查输出目录中的：

- `summary.json`
- `nmea-diff.txt`
- `forward-delay.csv`
- `udp.pcap`
- `collector-status.txt`

通过条件：

```text
capture_valid=true
acceptance_pass=true
loss_count=0
duplicate_count=0
reorder_count=0
first_differing_index=null
```

### GNSS-UDP-005：语句频率与序列完整性

根据目标设备批准的 GNSS 输出配置，统计每分钟：

- RMC、GGA、GLL、GSA、GSV、GST、ZDA 数量；
- 每个 talker 的数量；
- 每组 GSV 的 `total_messages` 和 `message_number`；
- GSV 是否完整覆盖 `1..N`；
- 同一 UTC 秒是否存在缺失、重复或跨秒混入。

MultibandRadio 至少单独检查 GPS 和北斗 GSV，不允许因为一条 GSV checksum 错误导致整组长期不完整。

通过条件：实际频率与目标设备配置一致；每组 GSV 完整，无缺号、重复号和跨代拼接。

### GNSS-UDP-006：GNSSAgent 接收统计

以 debug 日志运行，检查每秒统计和 60 秒汇总：

```text
udp_datagrams > 0
udp_bytes > 0
udp_rejects = 0
checksum_failures = 0
parser_failures = 0
kernel_drops = 0
gsv_incomplete = 0
published_cycles > 0
```

限频日志中的 `suppressed=N` 必须计入错误总数，不能只统计实际打印的 WARN 行数。

通过条件：正式正常流量下所有拒绝和失败计数均为 0。

### GNSS-UDP-007：SIMPLE/FULL 端到端验证

分别发送 SIMPLE 和 FULL 订阅请求：

```text
SIMPLE: 47 4E 53 53 02 01 00 01 01
FULL:   47 4E 53 53 02 01 00 01 02
```

检查：

- ACK 为 `47 4E 53 53 02 02 00 01 00`；v1 订阅必须收到不支持版本的结果。
- SIMPLE 帧固定 72 字节，类型 `0x04`，payload 64 字节。
- FULL 帧固定 144 字节，类型 `0x03`，payload 136 字节。
- 发布频率不超过 1 Hz。
- `recv_time` 单调递增。
- 经纬度、海拔、速度、航向、卫星数、DOP 和有效位符合原始 NMEA。
- RMC/ZDA 正常时 UTC 应有效；RMC 正常时速度/航向应按字段情况有效。
- GSV 完整时对应星座卫星数字段应有效。
- 每周期只发送所订阅的一条状态帧，不再发送独立时间质量消息。
- SIMPLE 载荷偏移 58/59/60 分别检查 time_state、time_reason、time_timeout_ms；FULL 偏移 124/125/126/128/132 分别检查 time_state、time_reason、time_samples、time_timeout_ms、time_rms。
- 开启 GST 后，按配置构造或实测连续低 RMS、稳定窗口、连续超限、GST 缺失、UTC 冲突/倒退及恢复；确认两种输出中的时间状态一致。FULL 的 time_rms 用有效位 bit29 表达，0 值不能视作缺失。
- 断流后不得收到伪造的新状态；客户端断连或自上一条新状态起达到 time_timeout_ms 必须撤销缓存可信状态，恢复后须重新累计窗口。

通过条件：协议、频率、有效位和值全部一致。

### GNSS-UDP-008：无定位与有定位场景

至少覆盖：

1. 天线正常且已定位。
2. 天线正常但尚未定位。
3. 允许测试时，断开/遮挡天线形成无定位状态。
4. 恢复天线并重新定位。

检查 GNSSAgent 不得用旧值或系统时间伪造无效字段。状态从无效到有效、从有效到无效时，有效位和值必须同步变化。

### GNSS-UDP-009：启动顺序与恢复

在获批测试窗口分别验证：

1. 先启动 GNSSAgent，再启动 UART 进程。
2. 先启动 UART 进程，再启动 GNSSAgent。
3. UART 转发短时中断后恢复。
4. GNSSAgent 重启后恢复接收。

通过条件：无永久端口冲突；恢复后自然继续接收；不重复、补发或乱序历史 NMEA；日志出现明确的 interrupted/recovered 状态。

### GNSS-UDP-010：异常报文防护

仅在隔离实验环境注入：空包、非 `$` 开头、NUL、超过 1024 字节、截断、多条拼包、非法 CR/LF、错误 checksum、非批准来源。

通过条件：GNSSAgent 拒绝异常包、计数和限频日志正确，进程持续运行，后续合法数据能够正常恢复。

### GNSS-UDP-011：长时间稳定性

建议持续 24 小时，至少保存每分钟汇总。检查：

- UART、UDP、GNSSAgent 进程稳定；
- 零 loss、duplicate、reorder；
- 零 UDP reject、checksum failure、parser failure；
- 零内核丢包；
- RSS、fd 数和 CPU 无持续增长；
- 日志轮转符合大小和备份数量限制；
- TCP 客户端断开重连后可重新订阅。

## 6. 已知故障的专项回归

每次修改设备 UART 转发应用后，必须专门验证以下历史问题不再出现：

| 故障 | 回归判据 |
|---|---|
| RMC/ZDA 逗号被改成 NUL | 所有 RMC/ZDA payload 中 `00` 数量为 0 |
| checksum 与内容不一致 | 所有语句重新计算 checksum 100% 通过 |
| 两条 GSA 拼入一个 UDP 包 | 每包 `$` 数量固定为 1 |
| `$GNZDA` 变为 `$NZDA` | identifier 长度和内容全部正确 |
| `$GNGSA` 变为 `$GNSA` | identifier 长度和内容全部正确 |
| GSV 内容损坏造成星座统计间歇无效 | 每组 GSV 完整，FULL 星座字段持续按输入有效 |
| 共享缓冲区被解析线程修改 | UART 与 UDP 字节逐句完全一致 |

## 7. 测试记录与报告

每次正式测试必须保存独立、带时间戳的证据目录，不覆盖历史数据。至少包括：

- 测试环境记录；
- UART/UDP/GNSSAgent 原始采集；
- pcap 与 tcpdump 统计；
- 自动分析 JSON/CSV；
- SIMPLE/FULL 原始帧和解码结果；
- GNSSAgent 日志；
- 失败样例的原始十六进制；
- 测试结论和未解决问题。

报告结论只允许：

- `PASS`：所有强制验收项通过；
- `FAIL`：任一强制项失败；
- `INVALID`：采集器、前置条件或证据不完整，必须重新测试，不能按通过处理。

## 8. 推荐执行顺序

1. 执行 GNSS-UDP-001、002，确认基本链路。
2. 执行 GNSS-UDP-003、004，先证明转发数据本身正确。
3. 执行 GNSS-UDP-005、006，检查频率、完整性和接收统计。
4. 执行 GNSS-UDP-007、008，验证业务输出。
5. 在获批窗口执行 GNSS-UDP-009、010。
6. 所有短测通过后执行 GNSS-UDP-011 长稳测试。
