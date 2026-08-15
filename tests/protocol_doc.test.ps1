$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$documentPath = Join-Path $projectRoot "docs\protocol\GNSSAgent-Binary-Protocol-v1.md"
$document = Get-Content -LiteralPath $documentPath -Raw

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
Require ($header.Contains('`0x01`')) 'Protocol version 1 is missing'
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
    '0x03' = @(124, 132)
    '0x04' = @(58, 66)
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

$simple = Get-Section '## 8. `GNSS_STATUS_SIMPLE` (`0x04`)' '## 9. `GNSS_STATUS_FULL` (`0x03`)'
$full = Get-Section '## 9. `GNSS_STATUS_FULL` (`0x03`)' '## 10. 控制消息'
Parse-Layout $simple 58 8 'SIMPLE'
Parse-Layout $full 124 28 'FULL'

foreach ($golden in @(
    '47 4E 53 53 01 01 00 01 01',
    '47 4E 53 53 01 01 00 01 02',
    '47 4E 53 53 01 02 00 01 00',
    '47 4E 53 53 01 04 00 3A',
    '47 4E 53 53 01 03 00 7C'
)) {
    Require ($document.Contains($golden)) "Active golden bytes are missing: $golden"
}

# The preserved 0x10/0x11 definitions are intentionally outside this consumer-facing check.
Write-Host "Active GNSSAgent binary protocol document contract passed."
