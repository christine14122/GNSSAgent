# MultibandHandheld GNSS UDP 联调测试报告

- 测试日期：2026-08-19
- 设备 IP：172.16.0.139
- 设备内核：Linux 3.18.21 armv7l
- 被测程序：`/app/GNSSAgent-MultibandHandheld`
- 被测程序 SHA-256：`649279fb732f85a4851b55f5eee24fdfe7d9d8a7f3e21f22cba044cec8f6fc47`
- 本地构建 SHA-256：`81f69fbb755bedeef753da896b1e927dd2fca6f28488a0bbc397697a421686c4`
- UART：`/dev/ttyS2`，持有者 `copy_service`（测试时 PID 193）
- UDP：`127.0.0.1:29501`
- TCP：`0.0.0.0:29501`
- 结论：**FAIL**

## 失败摘要

GNSSAgent 对每秒一条的 `$GNZDA` 持续产生解析失败。每个完整 60 秒统计窗口均为
`parser_failures=60`；日志中的限频警告为一次实际打印并带 `suppressed=59/60`。
正式 pcap 同期确认 ZDA 为 60.007 条/分钟，且其结构和 checksum 全部正确。
这违反 GNSS-UDP-006 的 `parser_failures=0` 强制判据。

设备二进制与本地构建的 SHA-256 不一致。本地当前源码包含 ZDA 解析支持，因此应优先核对设备部署版本是否陈旧或构建内容是否与源码不一致。

## 用例结果

| 用例 | 结果 | 证据/说明 |
|---|---|---|
| GNSS-UDP-001 | PASS（临时启动后） | GNSSAgent PID 8321 独占 UDP/TCP 29501；UART 由 `copy_service` 独占。测试前 GNSSAgent 未运行，测试结束已恢复。 |
| GNSS-UDP-002 | PASS | 10 秒十六进制冒烟抓包持续有数据；每包单条 NMEA、CRLF 结尾、无 NUL。 |
| GNSS-UDP-003 | PASS | 599.927 秒、7799 包、493549 字节；契约错误 0、checksum 错误 0、内核丢包 0。 |
| GNSS-UDP-004 | INVALID | 设备没有 `strace`，且没有可用 UART 统计文件，无法同时证明 UART read 与 GNSSAgent recv 的逐字节一致性和转发延迟。 |
| GNSS-UDP-005 | PASS（观测频率） | RMC/GGA/ZDA 各约 60/min，GSA 约 120/min，GSV 约 480/min；完整窗口内 GPS/北斗 GSV 组连续完整。pcap 首尾边界造成 1 个非完整边界组，不计为链路缺失。 |
| GNSS-UDP-006 | **FAIL** | 每个 60 秒窗口 `parser_failures=60`；`udp_rejects=0`、`checksum_failures=0`、`kernel_drops=0`、`gsv_incomplete=0`。 |
| GNSS-UDP-007 | PASS（协议/布局） | SIMPLE/FULL ACK 正确；分别连续收到 5 个 66/132 字节状态帧，类型为 `0x04`/`0x03`，约 1 Hz，recv_time 单调递增。 |
| GNSS-UDP-008 | PARTIAL | 已定位场景有效；未执行无定位、遮挡与恢复场景。 |
| GNSS-UDP-009 | NOT RUN | 涉及 UART/GNSSAgent 启停及中断恢复，方案要求获批测试窗口。 |
| GNSS-UDP-010 | NOT RUN | 异常报文注入仅允许隔离实验环境；当前连接的是运行设备。 |
| GNSS-UDP-011 | NOT RUN | 24 小时长稳未包含在本次短时联调窗口。 |

## UDP 统计

- 长度：27–79 字节
- 终止符：CRLF 7799，其他 0
- Talker：GN 3000、GP 2400、BD 2399
- 类型：GSV 4799、GSA 1200、RMC 600、GGA 600、ZDA 600
- NUL、非 `$` 起始、拼包、非法换行、缺失/错误 checksum、超长：全部 0
- tcpdump：7799 captured，0 dropped by kernel

## 环境限制与恢复

- 设备 PATH 中无 `strace`；`/app/tcpdump` 存在但原权限不可执行。
- 测试期间仅临时启动已部署 GNSSAgent，并临时赋予 `/app/tcpdump` 用户执行权限。
- 测试结束已停止本次启动的 PID 8321，并将 `/app/tcpdump` 恢复为 `0644`；RadioApp、copy_service 未重启。

## 建议

1. 将确认包含 ZDA 支持的 MultibandHandheld 构建部署到设备，并核对 SHA-256。
2. 复测 GNSS-UDP-003、005、006、007，要求所有 60 秒窗口 `parser_failures=0`。
3. 补齐 `strace` 和 UART 统计路径后执行 GNSS-UDP-004，才能给出 UART→UDP 的 loss/duplicate/reorder 和延迟结论。
4. 在批准的隔离窗口另行执行 008–010；短测全部通过后再安排 011 的 24 小时长稳。
