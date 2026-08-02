# GNSSAgent 二进制通信协议 v1

- 文档版本：1.2
- 协议版本：1
- 日期：2026-08-02
- 适用对象：GNSSAgent 用户侧/消费者开发人员
- 传输方式：TCP 长连接
- 默认端口：29501

本文档是可独立使用的线协议规范。实现订阅和解码不需要了解设备串口或 GNSSAgent 内部实现。

## 1. 协议概览

- 服务端默认监听 `0.0.0.0:29501`。
- 客户端连接后首先订阅 SIMPLE 或 FULL 状态。
- 订阅成功后，服务端以最高约 1 Hz 的频率主动推送所选状态。
- 没有新 GNSS 数据时，服务端不发送状态帧，也不发送应用层心跳。
- 同一条已订阅连接可以发送控制请求，但服务端只执行来自 `127.0.0.1` 的控制。
- TCP 只提供字节流边界，客户端必须自行处理半帧、粘包和一次读取多帧。
- 协议不使用 Protobuf、JSON、应用层 CRC 或编译器原生结构体布局。

## 2. 基本编码规则

| 项目 | 规则 |
|---|---|
| 字节序 | 所有多字节字段均为大端序（network byte order） |
| uint8 | 1 字节无符号整数 |
| uint16 | 2 字节大端无符号整数 |
| uint32 | 4 字节大端无符号整数 |
| uint64 | 8 字节大端无符号整数 |
| float32 | IEEE-754 binary32，其 32 位位模式按大端序传输 |
| float64 | IEEE-754 binary64，其 64 位位模式按大端序传输 |
| bool | 使用 uint8，0=false，1=true；不得发送其他值 |
| 字符串 | v1 载荷中没有字符串字段 |
| 对齐 | 无填充、无隐式对齐；严格按本文偏移逐字段编码 |

不得直接把 C/C++ 结构体内存发送到网络。结构体填充、主机字节序和浮点字节序都可能导致不兼容。

## 3. 公共帧格式

每帧由 8 字节公共头和 `payload_length` 字节载荷组成：

| 偏移 | 长度 | 字段 | 取值/含义 |
|---:|---:|---|---|
| 0 | 4 | `magic` | ASCII `GNSS`，十六进制 `47 4E 53 53` |
| 4 | 1 | `version` | v1 固定为 `0x01` |
| 5 | 1 | `message_type` | 消息类型，见第 4 节 |
| 6 | 2 | `payload_length` | 载荷长度，大端 uint16；v1 最大 1024 |
| 8 | N | `payload` | 消息载荷 |

总帧长：

```text
frame_length = 8 + payload_length
```

v1 没有 flags、sequence、reserved、CRC 或帧尾字段。

## 4. 消息类型

| 值 | 名称 | 方向 | 载荷长度 | 总帧长度 |
|---:|---|---|---:|---:|
| `0x01` | `SUBSCRIBE_REQUEST` | 客户端 → 服务端 | 1 | 9 |
| `0x02` | `SUBSCRIBE_ACK` | 服务端 → 客户端 | 1 | 9 |
| `0x03` | `GNSS_STATUS_FULL` | 服务端 → 客户端 | 124 | 132 |
| `0x04` | `GNSS_STATUS_SIMPLE` | 服务端 → 客户端 | 58 | 66 |
| `0x10` | `GNSS_SWITCH_REQ` | 客户端 → 服务端 | 6 | 14 |
| `0x11` | `GNSS_SWITCH_ACK` | 服务端 → 客户端 | 5 | 13 |

未列出的消息类型属于未知类型。接收方在载荷长度不超过 1024 时，应跳过整帧并继续解析，不应仅因未知类型关闭 TCP 连接。

## 5. 会话流程

```text
客户端                         服务端
   |------ TCP connect ---------->|
   |------ SUBSCRIBE_REQUEST ---->|
   |<----- SUBSCRIBE_ACK ---------|
   |<----- GNSS_STATUS_* ---------|
   |<----- GNSS_STATUS_* ---------|
   |------ GNSS_SWITCH_REQ ------>|  仅本机客户端可执行
   |<----- GNSS_SWITCH_ACK -------|
   |             ...              |
```

规则：

1. 建立 TCP 后，第一个有效应用消息必须是 `SUBSCRIBE_REQUEST`。
2. 收到成功 ACK 后才算订阅完成。
3. 同一连接固定为 SIMPLE 或 FULL；v1 不支持连接内切换。
4. 同一连接重复订阅返回 `ALREADY_SUBSCRIBED`。
5. 服务端最多保留 5 条 TCP 连接，尚未订阅和已订阅连接都计入总上限；非环回连接最多 4 条，为 `127.0.0.1` 本机控制端保留至少 1 个总连接名额。
6. 客户端必须在 TCP 建立后 5 秒内完成有效订阅，否则服务端关闭连接。
7. 总上限或非环回上限已满时，服务端不得等待新连接发送订阅请求。仅当接受连接时接收缓冲区已经包含完整合法的 `SUBSCRIBE_REQUEST`，服务端才可非阻塞返回 `SERVER_FULL`；否则立即关闭。客户端必须处理这两种情况。
8. 连接断开后，客户端重连并重新订阅。
9. 重连和重新订阅不会要求服务端复位或重新配置 GNSS。
10. v1 没有退订消息；关闭连接即结束订阅。
11. v1 没有应用层心跳。消费者根据业务需要判断数据是否陈旧，不能假定每秒必然有一帧。

## 6. 订阅消息

### 6.1 `SUBSCRIBE_REQUEST` (`0x01`)

载荷固定为 1 字节：

| 载荷偏移 | 长度 | 字段 | 含义 |
|---:|---:|---|---|
| 0 | 1 | `status_type` | 订阅格式 |

`status_type`：

| 值 | 名称 | 服务端推送消息 |
|---:|---|---|
| 1 | `SIMPLE` | `GNSS_STATUS_SIMPLE` (`0x04`) |
| 2 | `FULL` | `GNSS_STATUS_FULL` (`0x03`) |

其他值无效。

订阅 SIMPLE 的完整帧：

```text
47 4E 53 53 01 01 00 01 01
```

订阅 FULL 的完整帧：

```text
47 4E 53 53 01 01 00 01 02
```

### 6.2 `SUBSCRIBE_ACK` (`0x02`)

载荷固定为 1 字节：

| 载荷偏移 | 长度 | 字段 | 含义 |
|---:|---:|---|---|
| 0 | 1 | `result` | 订阅结果 |

`result`：

| 值 | 名称 | 含义 |
|---:|---|---|
| 0 | `SUCCESS` | 订阅成功 |
| 1 | `SERVER_FULL` | TCP 总连接数或非环回连接数已达上限 |
| 2 | `ALREADY_SUBSCRIBED` | 当前连接已经订阅 |
| 3 | `UNSUPPORTED_VERSION` | 不支持请求使用的协议版本 |
| 4 | `INTERNAL_ERROR` | 服务端内部错误 |
| 5 | `INVALID_STATUS_TYPE` | `status_type` 不是 1 或 2 |

成功 ACK 的完整帧：

```text
47 4E 53 53 01 02 00 01 00
```

如果服务端收到 magic、消息类型和 1 字节订阅载荷都可可靠解析，但帧头 `version` 不是 1，服务端使用 v1 的 `SUBSCRIBE_ACK` 返回 `UNSUPPORTED_VERSION`。其他无法按 v1 安全理解的高版本消息按未知版本整帧跳过。

## 7. 字段有效性

两个状态消息的第一个字段都是 64 位 `field_validity_mask`，但 SIMPLE 和 FULL 的位号彼此独立。

对每个带有效位的业务字段：

- 位为 1：字段存在、格式正确，载荷值可以解释。
- 位为 0：字段不可用，发送方必须把该字段的载荷字节全部置 0。
- 接收方必须先检查有效位，不能用数值是否为 0 判断有效性。
- 0 可能是有效值，例如速度 0、差分龄期 0、卫星数 0 或 `valid=0`。
- mask 中保留位必须为 0；接收方应忽略未来版本中自己不认识的置位。
- 发送方不得发送 NaN 或 Infinity；无法表示为有限数时字段无效并置 0。

`valid` 字段有两层含义：

1. `valid` 对应的 mask 位说明“是否得到了明确的导航有效性结论”；
2. `valid` 的值说明导航解有效（1）或明确无效（0）。

因此，`valid=0` 且有效位为 1 是正常状态。使用位置时，至少同时满足：

```text
latitude 位有效
longitude 位有效
valid 位有效
valid == 1
```

时间字段可以在导航位置无效时保持有效。

## 8. `GNSS_STATUS_SIMPLE` (`0x04`)

载荷固定为 58 字节，帧固定为 66 字节。

| 载荷偏移 | 长度 | 类型 | 有效位 | 字段 | 单位/含义 |
|---:|---:|---|---:|---|---|
| 0 | 8 | uint64 | — | `field_validity_mask` | 位 0–8 见本表，位 9–63 为 0 |
| 8 | 8 | uint64 | 0 | `utc_time` | GNSS UTC，Unix epoch 毫秒 |
| 16 | 8 | uint64 | 1 | `recv_time` | 服务端接收本周期首条有效输入的本机 Unix 毫秒时间 |
| 24 | 8 | float64 | 2 | `latitude` | 纬度，度，范围 -90～90；北为正 |
| 32 | 8 | float64 | 3 | `longitude` | 经度，度，范围 -180～180；东为正 |
| 40 | 8 | float64 | 4 | `altitude_msl` | 相对平均海平面的海拔，米，可为负 |
| 48 | 4 | float32 | 5 | `ground_speed_mps` | 地速，米/秒，非负 |
| 52 | 4 | float32 | 6 | `course_over_ground_deg` | 地面航向，真北为 0°、顺时针，范围 [0,360) |
| 56 | 1 | uint8 | 7 | `valid` | 0=导航解无效，1=导航解有效 |
| 57 | 1 | uint8 | 8 | `used_satellites` | 参与定位的卫星总数 |

SIMPLE 的有效位不能按 FULL 的位号解释。

SIMPLE 帧头固定为：

```text
47 4E 53 53 01 04 00 3A
```

## 9. `GNSS_STATUS_FULL` (`0x03`)

载荷固定为 124 字节，帧固定为 132 字节。

| 载荷偏移 | 长度 | 类型 | 有效位 | 字段 | 单位/含义 |
|---:|---:|---|---:|---|---|
| 0 | 8 | uint64 | — | `field_validity_mask` | 位 0–28 见本表，位 29–63 为 0 |
| 8 | 8 | uint64 | 0 | `utc_time` | GNSS UTC，Unix epoch 毫秒 |
| 16 | 8 | uint64 | 1 | `recv_time` | 服务端接收本周期首条有效输入的本机 Unix 毫秒时间 |
| 24 | 8 | float64 | 2 | `latitude` | 纬度，度，范围 -90～90；北为正 |
| 32 | 8 | float64 | 3 | `longitude` | 经度，度，范围 -180～180；东为正 |
| 40 | 8 | float64 | 4 | `altitude_msl` | 相对平均海平面的海拔，米，可为负 |
| 48 | 8 | float64 | 5 | `altitude_ellipsoid` | 椭球高，米，可为负 |
| 56 | 1 | uint8 | 6 | `valid` | 0=导航解无效，1=导航解有效 |
| 57 | 1 | uint8 | 7 | `fix_dimension` | 1=无定位，2=2D，3=3D |
| 58 | 1 | uint8 | 8 | `solution_type` | 定位解类型，见 9.1 |
| 59 | 1 | uint8 | 9 | `used_satellites` | 参与定位的卫星总数 |
| 60 | 1 | uint8 | 10 | `gps_satellites` | 可见 GPS 卫星数 |
| 61 | 1 | uint8 | 11 | `beidou_satellites` | 可见北斗卫星数 |
| 62 | 1 | uint8 | 12 | `glonass_satellites` | 可见 GLONASS 卫星数 |
| 63 | 1 | uint8 | 13 | `galileo_satellites` | 可见 Galileo 卫星数 |
| 64 | 4 | float32 | 14 | `gga_hdop` | GGA 维度的 HDOP，非负 |
| 68 | 4 | float32 | 15 | `gsa_pdop` | GSA 维度的 PDOP，非负 |
| 72 | 4 | float32 | 16 | `gsa_hdop` | GSA 维度的 HDOP，非负 |
| 76 | 4 | float32 | 17 | `gsa_vdop` | GSA 维度的 VDOP，非负 |
| 80 | 4 | float32 | 18 | `differential_age` | 差分修正龄期，秒，非负；0 可以有效 |
| 84 | 4 | float32 | 19 | `avg_used_cn0` | 参与定位卫星的平均 C/N0，dB-Hz |
| 88 | 4 | float32 | 20 | `ground_speed_mps` | 地速，米/秒，非负 |
| 92 | 4 | float32 | 21 | `course_over_ground_deg` | 地面航向，真北为 0°、顺时针，范围 [0,360) |
| 96 | 4 | float32 | 22 | `gst_pseudorange_rms` | 伪距残差 RMS，米，非负 |
| 100 | 4 | float32 | 23 | `gst_semi_major_error` | 误差椭圆半长轴 1σ，米，非负 |
| 104 | 4 | float32 | 24 | `gst_semi_minor_error` | 误差椭圆半短轴 1σ，米，非负 |
| 108 | 4 | float32 | 25 | `gst_orientation_deg` | 误差椭圆方向，度 |
| 112 | 4 | float32 | 26 | `gst_latitude_error` | 纬度方向 1σ 误差，米，非负 |
| 116 | 4 | float32 | 27 | `gst_longitude_error` | 经度方向 1σ 误差，米，非负 |
| 120 | 4 | float32 | 28 | `gst_altitude_error` | 高度方向 1σ 误差，米，非负 |

FULL 帧头固定为：

```text
47 4E 53 53 01 03 00 7C
```

### 9.1 `solution_type`

| 值 | 名称 | 含义 |
|---:|---|---|
| 0 | `INVALID` | 无效定位 |
| 1 | `SINGLE` | 单点定位 |
| 2 | `DIFFERENTIAL` | 差分定位，包括 DGNSS/SBAS |
| 3 | `PPS_PRECISE` | PPS/高精度模式，接收机相关 |
| 4 | `RTK_FIXED` | RTK 固定解 |
| 5 | `RTK_FLOAT` | RTK 浮点解 |
| 6 | `DEAD_RECKONING` | 航位推算 |
| 7 | `MANUAL` | 手工输入 |
| 8 | `SIMULATION` | 仿真模式 |
| 9–255 | `VENDOR_DEFINED` | 厂商扩展或未知值，消费者应保留但不得擅自解释 |

`solution_type=0` 可以是一个有效报告值；是否成功收到该字段仍由有效位 8 判断。

### 9.2 DOP 字段

`gga_hdop`、`gsa_pdop`、`gsa_hdop`、`gsa_vdop` 是四个独立字段。某个字段无效时不能使用其他 DOP 字段补值。

- 无定位状态下的 DOP 不上报为有效。
- 当前 MultibandRadio 接收机使用 `127.000` 作为无定位 DOP 哨兵；服务端遇到该值时清除相应有效位，消费者不会收到“有效的 127.000 DOP”。
- 同一周期存在多组 GSA DOP 时，服务端不会上报互相冲突的 DOP 组；判定为一致时上报本周期第一组有效 GSA DOP，判定为冲突时三项 GSA DOP 均无效。精确的十进制判定规则属于服务端需求，不要求消费者实现。

### 9.3 高度字段

- `altitude_msl`：相对平均海平面的常用海拔。
- `altitude_ellipsoid`：相对参考椭球面的高度。

两者有效位独立；消费者应根据业务坐标基准明确选择，不能混用。服务端只有在来源定位有效、海拔与大地水准面分离量都可用时才设置 `altitude_ellipsoid` 有效位；无定位状态中的分离量 0 不会生成有效椭球高。消费者用于导航时仍应同时检查总体 `valid`。

### 9.4 目标设备字段可用性

协议固定布局覆盖多个设备目标。字段在某个目标上不可用时通过 mask 表达，不改变载荷长度或偏移。

当前 MultibandRadio 固件的预期如下：

- 不提供 GLONASS、Galileo 计数：FULL 位 12、13 为 0。
- 不提供 GST 七项误差统计：FULL 位 22–28 为 0。
- 无定位且没有日期时，`utc_time` 位 0 为 0；消费者不得用 `recv_time` 或自身日期补造 GNSS UTC。
- 完整的“0 颗可见卫星”报告会将相应星座计数有效位置 1、数值置 0；这不同于该星座字段不可用。

## 10. 控制消息

### 10.1 `GNSS_SWITCH_REQ` (`0x10`)

载荷固定为 6 字节，没有 reserved 字段：

| 载荷偏移 | 长度 | 类型 | 字段 | 含义 |
|---:|---:|---|---|---|
| 0 | 4 | uint32 | `request_id` | 请求关联 ID，由客户端生成 |
| 4 | 1 | uint8 | `switch` | 0=关闭，1=开启 |
| 5 | 1 | uint8 | `type` | 1=GPS，2=北斗，3=GPS+北斗 |

即使 `switch=0`，`type` 仍必须填写 1、2、3 之一，用于表达用户界面当前保存的星座选择。

控制语义：

- `switch=0`：通知服务端 GNSS 已被关闭；服务端停止当前聚合。
- `switch=1`：通知服务端 GNSS 已开启，并要求工作模式与 `type` 一致。
- 请求值与当前状态一致时，服务端应无副作用地返回成功。
- 普通 TCP 重连不得为了“恢复状态”自动发送本请求。
- 客户端在收到 ACK 前不应并发发送第二个控制请求。

示例：`request_id=0x01020304`，开启 GPS+北斗：

```text
47 4E 53 53 01 10 00 06 01 02 03 04 01 03
```

### 10.2 `GNSS_SWITCH_ACK` (`0x11`)

载荷固定为 5 字节：

| 载荷偏移 | 长度 | 类型 | 字段 | 含义 |
|---:|---:|---|---|---|
| 0 | 4 | uint32 | `request_id` | 原样返回请求 ID |
| 4 | 1 | uint8 | `result` | 执行结果 |

`result`：

| 值 | 名称 | 含义 |
|---:|---|---|
| 0 | `SUCCESS` | 请求已成功完成或本来就是目标状态 |
| 1 | `INVALID_ARGUMENT` | `switch` 或 `type` 非法 |
| 2 | `FORBIDDEN` | 对端不是 `127.0.0.1`，禁止控制 |
| 3 | `SERIAL_UNAVAILABLE` | GNSS 通道不可用或写入失败 |
| 4 | `TIMEOUT` | 等待设备输出超时 |
| 5 | `VERIFY_FAILED` | 收到设备数据，但实际模式与请求不一致 |
| 6 | `INTERNAL_ERROR` | 服务端内部错误 |
| 7 | `BUSY` | 另一个控制请求正在执行 |

对应上述示例的成功 ACK：

```text
47 4E 53 53 01 11 00 05 01 02 03 04 00
```

客户端使用 `request_id` 关联请求与响应。`request_id` 可以使用 uint32 全范围，但同一连接上的未完成请求不得重号。

## 11. TCP 流解析与错误恢复

推荐接收方使用以下确定性规则：

1. 在有界接收缓冲区中扫描 4 字节 magic `47 4E 53 53`。
2. magic 前的垃圾字节全部丢弃；保留末尾最多 3 字节，以处理 magic 跨读取边界。
3. magic 后不足 8 字节时继续读取。
4. 读取 `payload_length`。如果大于 1024，把当前 magic 的第一个字节视为错误起点，丢弃该字节并重新扫描。
5. 长度合理但整帧尚未到齐时继续读取，不得把 TCP 一次 read 当成一帧。
6. 整帧到齐后，根据 version、type 和该类型要求的精确载荷长度解析。
7. 未知 type 或不支持的 version 在长度合理时跳过整帧；第 6.2 节定义的可识别订阅版本错误除外。
8. 已知类型但载荷长度错误时丢弃整帧，然后继续扫描下一帧。
9. 单个格式错误不得导致 TCP 断开。

服务端接收错误请求时：

- 无法可靠取得请求类型或 request_id：记录错误并忽略，不发送通用错误帧。
- `GNSS_SWITCH_REQ` 的帧头和 request_id 可可靠取得、但参数非法：返回 `INVALID_ARGUMENT`。
- 远程客户端发出格式正确的控制请求：返回 `FORBIDDEN`。

协议错误不会主动断开连接；对端关闭、网络错误、写超时或容量拒绝仍可以结束连接。

## 12. 状态消费规则

消费者每收到一帧状态，应按以下顺序处理：

1. 校验 magic、version、message type 和精确 payload length。
2. 按大端序读取 `field_validity_mask`。
3. 只解读有效位置 1 的字段。
4. SIMPLE 使用 SIMPLE 位号；FULL 使用 FULL 位号。
5. 需要导航位置时再检查 `valid` 位和值。
6. 使用 `utc_time` 做 GNSS 时间时，确认其有效位；它不代表服务端接收时间。
7. 使用 `recv_time` 判断本机接收新鲜度时，注意其准确性取决于服务端系统时钟。
8. 接收到未来不认识的 mask 保留位时忽略，不应使进程崩溃。

### 12.1 本机消费者授时

本机消费者不能在收到状态后直接把系统时间设置为 `utc_time`，因为状态在服务端经过聚合后才发送。消费者应在收到完整状态帧时、修改系统时钟之前，立即记录当前系统 Unix 毫秒时间 `client_recv_time`：

```text
utc_time → recv_time → client_recv_time → 实际校时
```

在消息接收时刻，推荐目标时间为：

```text
target_at_client_receive = utc_time + (client_recv_time - recv_time)
```

等价形式为：

```text
target_at_client_receive = client_recv_time + (utc_time - recv_time)
```

使用条件：

- `utc_time` 和 `recv_time` 的有效位都为 1。
- 必须先记录 `client_recv_time`，再修改系统时钟。
- GNSSAgent 和消费者必须在同一台设备上，并使用同一个系统时钟。
- 从生成 `recv_time` 到记录 `client_recv_time` 期间，系统时钟不能发生其他跳变。
- 如果实际校时晚于消息接收，还要加上从 `client_recv_time` 到实际校时操作之间的单调时钟耗时。
- 远程消费者不能使用该公式，因为远端系统时间和 `recv_time` 不在同一时钟域。

该公式补偿的是每秒聚合和本机消息传递造成的 1～2 秒延迟。它不能补偿 GNSS 测量历元到接收机输出之间的内部延迟；需要更高精度时应使用 GNSS 1PPS。

消费者不得：

- 通过字段为 0 推断无效。
- 把 SIMPLE mask 当作 FULL mask。
- 假定每秒一定收到状态。
- 把海拔和椭球高混为同一基准。
- 将 `solution_type=0` 的字段自动视为“字段没收到”。

## 13. Golden Frames

### 13.1 SIMPLE 订阅

```text
47 4E 53 53 01 01 00 01 01
```

### 13.2 FULL 订阅

```text
47 4E 53 53 01 01 00 01 02
```

### 13.3 订阅成功

```text
47 4E 53 53 01 02 00 01 00
```

### 13.4 开启组合模式请求与成功响应

```text
# request_id=0x01020304, switch=1, type=3
47 4E 53 53 01 10 00 06 01 02 03 04 01 03

# request_id=0x01020304, result=SUCCESS
47 4E 53 53 01 11 00 05 01 02 03 04 00
```

### 13.5 SIMPLE 布局测试帧

下面是一条 66 字节的结构测试帧：只把 `recv_time` 标记为有效，值为 1；其他字段无效且为 0。它用于验证偏移、长度和大端序，不代表实际业务时间。

```text
# header: type=GNSS_STATUS_SIMPLE, payload_length=58
47 4E 53 53 01 04 00 3A

# field_validity_mask = bit 1
00 00 00 00 00 00 00 02
# utc_time = 0, invalid
00 00 00 00 00 00 00 00
# recv_time = 1, valid
00 00 00 00 00 00 00 01
# latitude = 0, invalid
00 00 00 00 00 00 00 00
# longitude = 0, invalid
00 00 00 00 00 00 00 00
# altitude_msl = 0, invalid
00 00 00 00 00 00 00 00
# ground_speed_mps = 0, invalid
00 00 00 00
# course_over_ground_deg = 0, invalid
00 00 00 00
# valid = 0, invalid; used_satellites = 0, invalid
00 00
```

## 14. 版本兼容

- 本文定义 version 1。
- v1 接收方必须按固定消息长度解析，不能根据本机结构体大小猜测。
- 新增消息类型时，旧接收方按“未知 type 跳过整帧”处理。
- 改变现有字段偏移、类型、长度或语义需要提升协议 version，不能悄悄修改 v1。
- v1 的 mask 保留位不得用于改变既有字段语义。

## 15. v1 明确不提供的内容

- 原始 NMEA。
- 单颗卫星明细。
- Protobuf/JSON 文本格式。
- 应用层 CRC32。
- 通用 `ERROR` 消息。
- 心跳消息。
- 设备 ID、天线 ID、天线状态。
- 系统校时命令。
- GNSS 1PPS 数据。

## 16. 用户侧实现验收清单

- 能发送 SIMPLE/FULL 订阅并解析 ACK。
- 能在任意 TCP 分片和粘包情况下恢复完整帧。
- 能解析 66 字节 SIMPLE 帧和 132 字节 FULL 帧。
- 所有整数、float32、float64 都按大端序处理。
- 所有字段都先检查对应 mask 位。
- 能区分“`valid` 字段无效”和“明确报告 `valid=0`”。
- 能区分“字段不可用”和“卫星数有效且为 0”。
- 不把无定位 DOP 哨兵或无定位椭球高当成有效数据。
- 不因未知消息类型或单个坏帧退出。
- 断线后能重连并重新订阅。
- 能处理总连接或非环回连接容量已满时收到 `SERVER_FULL` 或 TCP 直接断开。
- LAN 客户端不得依赖第 5 个非环回连接名额；该名额为本机控制端保留。
- 本机控制端能用 request_id 匹配 `GNSS_SWITCH_ACK`。
- 局域网普通消费者不发送控制消息。
