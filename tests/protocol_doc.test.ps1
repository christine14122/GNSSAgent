$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$documentPath = Join-Path $projectRoot "docs\protocol\GNSSAgent-Binary-Protocol-v2.md"
$document = Get-Content -LiteralPath $documentPath -Raw -Encoding UTF8

function Require([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Get-Section([string]$Start, [string]$End) {
    $pattern = '(?s)' + [regex]::Escape($Start) + '(.*?)' + [regex]::Escape($End)
    $match = [regex]::Match($document, $pattern)
    Require $match.Success "Section not found: $Start"
    return $match.Groups[1].Value
}

function Parse-Layout([string]$Section, [int]$ExpectedSize, [int]$ExpectedLastBit, [string]$Name) {
    $rows = @()
    foreach ($line in ($Section -split "`r?`n")) {
        if ($line -notmatch '^\|\s*\d+\s*\|') { continue }
        $columns = @($line.Trim().Trim('|').Split('|') | ForEach-Object { $_.Trim() })
        if ($columns.Count -lt 6) { continue }
        $rows += [pscustomobject]@{
            Offset = [int]$columns[0]
            Length = [int]$columns[1]
            Bit = $columns[3].Trim('`')
        }
    }
    Require ($rows.Count -gt 1) "$Name layout rows were not parsed"
    $nextOffset = 0
    $bits = @()
    foreach ($row in $rows) {
        Require ($row.Offset -eq $nextOffset) "$Name layout gap/overlap at $($row.Offset), expected $nextOffset"
        $nextOffset += $row.Length
        if ($row.Bit -match '^\d+$') { $bits += [int]$row.Bit }
    }
    Require ($nextOffset -eq $ExpectedSize) "$Name payload totals $nextOffset, expected $ExpectedSize"
    Require (($bits -join ',') -eq ((0..$ExpectedLastBit) -join ',')) "$Name validity bits are not continuous 0-$ExpectedLastBit"
}

$header = Get-Section '## 3. 公共帧格式' '## 4. 消息类型'
Require ($header.Contains('8 字节公共头')) 'Common header is not documented as 8 bytes'
Require ($header.Contains('47 4E 53 53')) 'Common magic bytes are missing'
Require ($header.Contains('`0x02`')) 'Protocol version 2 is missing'
foreach ($expected in @(
    '| 0 | 4 | `magic` |',
    '| 4 | 1 | `version` |',
    '| 5 | 1 | `message_type` |',
    '| 6 | 2 | `payload_length` |',
    '| 8 | N | `payload` |'
)) {
    Require ($header.Contains($expected)) "Common header row is missing: $expected"
}

$messageSection = Get-Section '## 4. 消息类型' '## 5. 会话流程'
$activeMessages = @{
    '0x01' = @(1, 9)
    '0x02' = @(1, 9)
    '0x03' = @(136, 144)
    '0x04' = @(64, 72)
}
$parsedMessages = @{}
foreach ($line in ($messageSection -split "`r?`n")) {
    if ($line -notmatch '^\|\s*`0x[0-9A-Fa-f]+`\s*\|') { continue }
    $columns = @($line.Trim().Trim('|').Split('|') | ForEach-Object { $_.Trim() })
    if ($columns.Count -eq 5) {
        $parsedMessages[$columns[0].Trim('`')] = @([int]$columns[3], [int]$columns[4])
    }
}
foreach ($entry in $activeMessages.GetEnumerator()) {
    Require $parsedMessages.ContainsKey($entry.Key) "Message row missing: $($entry.Key)"
    Require ($parsedMessages[$entry.Key][0] -eq $entry.Value[0]) "$($entry.Key) payload length mismatch"
    Require ($parsedMessages[$entry.Key][1] -eq $entry.Value[1]) "$($entry.Key) frame length mismatch"
}
Require ($parsedMessages.Count -eq 4) 'Only four active message types are expected'

$simple = Get-Section '## 8. `GNSS_STATUS_SIMPLE` (`0x04`)' '## 9. `GNSS_STATUS_FULL` (`0x03`)'
$full = Get-Section '## 9. `GNSS_STATUS_FULL` (`0x03`)' '## 10. 保留的控制消息定义'
Parse-Layout $simple 64 8 'SIMPLE'
Parse-Layout $full 136 29 'FULL'
foreach ($row in @(
    '| 58 | 1 | uint8 | — | `time_state` |',
    '| 59 | 1 | uint8 | — | `time_reason` |',
    '| 60 | 4 | uint32 | — | `time_timeout_ms` |'
)) { Require ($simple.Contains($row)) "SIMPLE time quality field missing: $row" }
foreach ($row in @(
    '| 124 | 1 | uint8 | — | `time_state` |',
    '| 125 | 1 | uint8 | — | `time_reason` |',
    '| 126 | 2 | uint16 | — | `time_samples` |',
    '| 128 | 4 | uint32 | — | `time_timeout_ms` |',
    '| 132 | 4 | float32 | 29 | `time_rms` |'
)) { Require ($full.Contains($row)) "FULL time quality field missing: $row" }
Require ($document.Contains('## 17. 时间可信度字段')) 'Embedded time quality section is missing'
Require (-not $document.Contains('GNSS_TIME_QUALITY')) 'Standalone time quality message must not remain'

foreach ($golden in @(
    '47 4E 53 53 02 01 00 01 01',
    '47 4E 53 53 02 01 00 01 02',
    '47 4E 53 53 02 02 00 01 00',
    '47 4E 53 53 02 04 00 40',
    '47 4E 53 53 02 03 00 88'
)) {
    Require ($document.Contains($golden)) "Active golden bytes are missing: $golden"
}

$layoutGolden = Get-Section '### 13.5 SIMPLE 布局测试帧' '## 14. 版本兼容'
$hexLines = @($layoutGolden -split "`r?`n" | Where-Object { $_ -match '^[0-9A-F]{2}( [0-9A-F]{2})*$' })
$bytes = @((($hexLines -join ' ') -split ' ') | ForEach-Object { [Convert]::ToByte($_, 16) })
Require ($bytes.Count -eq 72) 'SIMPLE golden frame must contain exactly 72 bytes'
Require ($bytes[4] -eq 2 -and $bytes[5] -eq 4 -and $bytes[6] -eq 0 -and $bytes[7] -eq 64) 'SIMPLE golden header mismatch'
Require ($bytes[68] -eq 0 -and $bytes[69] -eq 0 -and $bytes[70] -eq 11 -and $bytes[71] -eq 184) 'SIMPLE golden timeout must be 3000ms in network byte order'

$document = Get-Content -LiteralPath (Join-Path $projectRoot 'docs\superpowers\specs\GNSSAgent-Requirements.md') -Raw -Encoding UTF8
Parse-Layout (Get-Section '### 9.3 SIMPLE 状态消息' '### 9.4 FULL 状态消息') 64 8 'Requirements SIMPLE'
Parse-Layout (Get-Section '### 9.4 FULL 状态消息' '### 9.5 会话与发布规则') 136 29 'Requirements FULL'
Require ($document.Contains('GNSSAgent-Binary-Protocol-v2.md')) 'Requirements must link to the v2 protocol'
Require (-not $document.Contains('GNSS_TIME_QUALITY')) 'Requirements must not prescribe the removed standalone quality message'

# The preserved 0x10/0x11 definitions are intentionally outside this consumer-facing check.
Write-Host "Active GNSSAgent binary protocol document contract passed."
