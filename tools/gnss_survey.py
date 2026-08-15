#!/usr/bin/env python3
"""Host-side GNSS UART-owner to GNSSAgent UDP tee acceptance verifier."""

from __future__ import annotations

import argparse
import base64
import collections
import csv
import dataclasses
import datetime as dt
import difflib
import json
import math
import os
import re
import shlex
import struct
import sys
import time
from pathlib import Path
from typing import Iterable, Optional, Sequence


@dataclasses.dataclass(frozen=True)
class TraceEvent:
    timestamp: float
    pid: int
    syscall: str
    fd: int
    data: bytes
    returned: int


@dataclasses.dataclass
class TraceCapture:
    events: list[TraceEvent]
    errors: list[str]

    @property
    def valid(self) -> bool:
        return not self.errors


@dataclasses.dataclass(frozen=True)
class SentenceOccurrence:
    payload: bytes
    completed_at: float


@dataclasses.dataclass
class SentenceExtraction:
    sentences: list[SentenceOccurrence]
    leading_discarded: int
    trailing_discarded: int
    errors: list[str]


@dataclasses.dataclass
class SequenceDiff:
    exact: bool
    loss_count: int
    duplicate_count: int
    reorder_count: int
    first_difference: Optional[int]
    first_uart: Optional[bytes]
    first_udp: Optional[bytes]


@dataclasses.dataclass
class ForwardDelayResult:
    valid: bool
    delays_ms: list[float]
    missing_count: int
    negative_count: int
    reason: str = ""


_SIMPLE_ESCAPES = {
    "a": 0x07,
    "b": 0x08,
    "f": 0x0C,
    "n": 0x0A,
    "r": 0x0D,
    "t": 0x09,
    "v": 0x0B,
    "\\": 0x5C,
    '"': 0x22,
    "'": 0x27,
}


def decode_c_string(text: str) -> bytes:
    """Decode the exact C escapes emitted by strace into bytes."""
    output = bytearray()
    index = 0
    while index < len(text):
        char = text[index]
        if char != "\\":
            codepoint = ord(char)
            if codepoint <= 0xFF:
                output.append(codepoint)
            else:
                output.extend(char.encode("utf-8"))
            index += 1
            continue
        index += 1
        if index >= len(text):
            raise ValueError("trailing backslash in strace C string")
        escape = text[index]
        if escape in _SIMPLE_ESCAPES:
            output.append(_SIMPLE_ESCAPES[escape])
            index += 1
            continue
        if escape == "x":
            digits = text[index + 1:index + 3]
            if not digits or not re.fullmatch(r"[0-9A-Fa-f]{1,2}", digits):
                raise ValueError("invalid hexadecimal escape in strace C string")
            output.append(int(digits, 16))
            index += 1 + len(digits)
            continue
        if escape in "01234567":
            end = index
            while end < len(text) and end < index + 3 and text[end] in "01234567":
                end += 1
            output.append(int(text[index:end], 8))
            index = end
            continue
        output.append(ord(escape) & 0xFF)
        index += 1
    return bytes(output)


_PREFIX = re.compile(
    r"^\s*(?:(?:\[pid\s+)?(?P<pid>\d+)\]?\s+)?"
    r"(?P<timestamp>\d+\.\d+)\s+(?P<body>.*)$"
)
_NORMAL_CALL = re.compile(
    r"^(?P<name>read|recvfrom|recvmsg)\((?P<args>.*)\)\s+=\s+(?P<ret>-?\d+)"
)
_UNFINISHED = re.compile(
    r"^(?P<name>read|recvfrom|recvmsg)\((?P<fd>\d+),.*<unfinished \.\.\.>$"
)
_RESUMED = re.compile(
    r"^<\.\.\.\s+(?P<name>read|recvfrom|recvmsg)\s+resumed>"
    r"(?P<args>.*)\)\s+=\s+(?P<ret>-?\d+)"
)
_C_STRING = re.compile(r'"(?P<data>(?:[^"\\]|\\.)*)"(?P<truncated>\.\.\.)?')


def parse_strace(
    text: str,
    syscall_names: Iterable[str],
    fd_filter: Optional[int] = None,
    start_time: Optional[float] = None,
    end_time: Optional[float] = None,
) -> TraceCapture:
    """Parse complete successful input syscalls and reject incomplete evidence."""
    allowed = set(syscall_names)
    events: list[TraceEvent] = []
    errors: list[str] = []
    pending: dict[tuple[int, str], int] = {}

    for line_number, line in enumerate(text.splitlines(), 1):
        prefix = _PREFIX.match(line)
        if not prefix:
            if any(name + "(" in line or f"<... {name} resumed>" in line for name in allowed):
                errors.append(f"line {line_number}: missing -ttt timestamp or malformed prefix")
            continue
        pid = int(prefix.group("pid") or 0)
        timestamp = float(prefix.group("timestamp"))
        body = prefix.group("body")

        unfinished = _UNFINISHED.match(body)
        if unfinished and unfinished.group("name") in allowed:
            fd = int(unfinished.group("fd"))
            if fd_filter is not None and fd != fd_filter:
                continue
            key = (pid, unfinished.group("name"))
            if key in pending:
                errors.append(f"line {line_number}: duplicate unfinished {key[1]} for pid {pid}")
            pending[key] = fd
            continue

        resumed = _RESUMED.match(body)
        if resumed and resumed.group("name") in allowed:
            key = (pid, resumed.group("name"))
            if key not in pending:
                errors.append(f"line {line_number}: unmatched resumed {key[1]} for pid {pid}")
                continue
            fd = pending.pop(key)
            _append_trace_event(
                events, errors, line_number, timestamp, pid, key[1], fd,
                resumed.group("args"), int(resumed.group("ret")), start_time, end_time,
            )
            continue

        normal = _NORMAL_CALL.match(body)
        if not normal or normal.group("name") not in allowed:
            continue
        fd_match = re.match(r"\s*(\d+)\s*,", normal.group("args"))
        if not fd_match:
            errors.append(f"line {line_number}: missing descriptor")
            continue
        fd = int(fd_match.group(1))
        if fd_filter is not None and fd != fd_filter:
            continue
        _append_trace_event(
            events, errors, line_number, timestamp, pid, normal.group("name"), fd,
            normal.group("args"), int(normal.group("ret")), start_time, end_time,
        )

    for (pid, name), fd in sorted(pending.items()):
        errors.append(f"unresolved unfinished {name} for pid {pid} fd {fd}")
    return TraceCapture(events=events, errors=errors)


def _append_trace_event(
    events: list[TraceEvent],
    errors: list[str],
    line_number: int,
    timestamp: float,
    pid: int,
    syscall_name: str,
    fd: int,
    arguments: str,
    returned: int,
    start_time: Optional[float],
    end_time: Optional[float],
) -> None:
    if returned <= 0:
        return
    if start_time is not None and timestamp < start_time:
        return
    if end_time is not None and timestamp > end_time:
        return
    string_match = _C_STRING.search(arguments)
    if not string_match:
        errors.append(f"line {line_number}: successful {syscall_name} has no decoded buffer")
        return
    if string_match.group("truncated"):
        errors.append(f"line {line_number}: strace truncated a successful {syscall_name} buffer")
        return
    try:
        data = decode_c_string(string_match.group("data"))
    except ValueError as error:
        errors.append(f"line {line_number}: {error}")
        return
    if len(data) != returned:
        errors.append(
            f"line {line_number}: decoded {len(data)} bytes but {syscall_name} returned {returned}"
        )
        return
    events.append(TraceEvent(timestamp, pid, syscall_name, fd, data, returned))


def extract_complete_sentences(events: Sequence[TraceEvent]) -> SentenceExtraction:
    stream = bytearray()
    byte_times: list[float] = []
    for event in events:
        stream.extend(event.data)
        byte_times.extend([event.timestamp] * len(event.data))
    data = bytes(stream)
    first_start = data.find(b"$")
    if first_start < 0:
        return SentenceExtraction([], len(data), 0, [])

    leading = first_start
    position = first_start
    sentences: list[SentenceOccurrence] = []
    errors: list[str] = []
    while position < len(data):
        newline = data.find(b"\n", position)
        if newline < 0:
            break
        raw = data[position:newline]
        if raw.endswith(b"\r"):
            raw = raw[:-1]
        embedded = raw.find(b"$", 1)
        if embedded >= 0:
            errors.append(f"interior sentence boundary missing before byte {position + embedded}")
            position += embedded
            continue
        if not raw.startswith(b"$"):
            errors.append(f"complete line at byte {position} does not start with '$'")
        else:
            sentences.append(SentenceOccurrence(raw, byte_times[newline]))
        position = newline + 1
        if position < len(data) and data[position:position + 1] != b"$":
            next_start = data.find(b"$", position)
            if next_start < 0:
                break
            errors.append(f"non-NMEA bytes between complete sentences at byte {position}")
            position = next_start
    trailing = len(data) - position
    return SentenceExtraction(sentences, leading, trailing, errors)


def normalize_udp_payload(payload: bytes) -> bytes:
    if payload.endswith(b"\r\n"):
        return payload[:-2]
    if payload.endswith(b"\n"):
        return payload[:-1]
    return payload


def compare_sequences(uart: Sequence[bytes], udp: Sequence[bytes]) -> SequenceDiff:
    first_difference: Optional[int] = None
    first_uart: Optional[bytes] = None
    first_udp: Optional[bytes] = None
    for index in range(max(len(uart), len(udp))):
        left = uart[index] if index < len(uart) else None
        right = udp[index] if index < len(udp) else None
        if left != right:
            first_difference = index
            first_uart = left
            first_udp = right
            break

    uart_counts = collections.Counter(uart)
    udp_counts = collections.Counter(udp)
    loss = sum(max(count - udp_counts[payload], 0) for payload, count in uart_counts.items())
    duplicate = sum(max(count - uart_counts[payload], 0) for payload, count in udp_counts.items())

    positions: dict[bytes, collections.deque[int]] = collections.defaultdict(collections.deque)
    for index, payload in enumerate(uart):
        positions[payload].append(index)
    matched_indices: list[int] = []
    for payload in udp:
        if positions[payload]:
            matched_indices.append(positions[payload].popleft())
    reorder = 0
    greatest = -1
    for index in matched_indices:
        if index < greatest:
            reorder += 1
        greatest = max(greatest, index)

    exact = len(uart) == len(udp) and first_difference is None
    return SequenceDiff(exact, loss, duplicate, reorder, first_difference, first_uart, first_udp)


def match_forward_delays(
    uart: Sequence[SentenceOccurrence],
    receives: Sequence[TraceEvent],
) -> ForwardDelayResult:
    receive_times: dict[bytes, collections.deque[float]] = collections.defaultdict(collections.deque)
    for event in receives:
        receive_times[normalize_udp_payload(event.data)].append(event.timestamp)
    delays: list[float] = []
    missing = 0
    negative = 0
    for occurrence in uart:
        queue = receive_times[occurrence.payload]
        if not queue:
            missing += 1
            continue
        delay = (queue.popleft() - occurrence.completed_at) * 1000.0
        if delay < 0:
            negative += 1
            continue
        delays.append(delay)
    valid = missing == 0 and negative == 0 and len(delays) == len(uart)
    reason = ""
    if missing:
        reason = f"{missing} UART sentences have no GNSSAgent receive event"
    elif negative:
        reason = f"{negative} matched receive events precede UART completion"
    return ForwardDelayResult(valid, delays, missing, negative, reason)


def nearest_rank(values: Sequence[float], percentile: float) -> Optional[float]:
    if not values:
        return None
    ordered = sorted(values)
    rank = max(1, math.ceil(percentile * len(ordered)))
    return ordered[rank - 1]


def delay_percentiles(values: Sequence[float]) -> dict[str, Optional[float]]:
    return {
        "sample_count": len(values),
        "p50_ms": nearest_rank(values, 0.50),
        "p95_ms": nearest_rank(values, 0.95),
        "p99_ms": nearest_rank(values, 0.99),
        "max_ms": max(values) if values else None,
    }


def parse_tcpdump_stats(text: str) -> dict[str, Optional[int]]:
    patterns = {
        "captured": r"(\d+) packets captured",
        "received_by_filter": r"(\d+) packets received by filter",
        "dropped_by_kernel": r"(\d+) packets dropped by kernel",
    }
    result: dict[str, Optional[int]] = {}
    for key, pattern in patterns.items():
        match = re.search(pattern, text)
        result[key] = int(match.group(1)) if match else None
    return result


def evaluate_capture(
    uart_capture: TraceCapture,
    receive_capture: TraceCapture,
    sentence_extraction: SentenceExtraction,
    tcpdump_stats: str,
    collector_status: dict[str, str],
    pcap_errors: Sequence[str] = (),
) -> list[str]:
    reasons = list(uart_capture.errors) + list(receive_capture.errors)
    reasons.extend(sentence_extraction.errors)
    reasons.extend(pcap_errors)
    if not sentence_extraction.sentences:
        reasons.append("no complete UART NMEA sentence was captured")
    stats = parse_tcpdump_stats(tcpdump_stats)
    if stats["dropped_by_kernel"] is None:
        reasons.append("tcpdump final kernel-drop count is unavailable")
    elif stats["dropped_by_kernel"] != 0:
        reasons.append(f"tcpdump reports {stats['dropped_by_kernel']} packets dropped by kernel")
    for key in (
        "collectors_ready",
        "uart_attached",
        "gnss_attached",
        "uart_detached",
        "gnss_detached",
        "tcpdump_detached",
        "tty_snapshots",
        "processes_stable",
    ):
        if collector_status.get(key) != "yes":
            reasons.append(f"collector status {key} is not yes")
    return reasons


def parse_pcap_udp(
    data: bytes,
    destination_port: int,
    start_time: Optional[float] = None,
    end_time: Optional[float] = None,
) -> tuple[list[TraceEvent], list[str]]:
    if len(data) < 24:
        return [], ["pcap global header is truncated"]
    magic = data[:4]
    formats = {
        b"\xd4\xc3\xb2\xa1": ("<", 1_000_000.0),
        b"\xa1\xb2\xc3\xd4": (">", 1_000_000.0),
        b"\x4d\x3c\xb2\xa1": ("<", 1_000_000_000.0),
        b"\xa1\xb2\x3c\x4d": (">", 1_000_000_000.0),
    }
    if magic not in formats:
        return [], ["unsupported pcap magic"]
    endian, fraction_scale = formats[magic]
    link_type = struct.unpack_from(endian + "I", data, 20)[0]
    offset = 24
    events: list[TraceEvent] = []
    errors: list[str] = []
    record_index = 0
    while offset < len(data):
        record_index += 1
        if offset + 16 > len(data):
            errors.append(f"pcap record {record_index} header is truncated")
            break
        seconds, fraction, captured_length, original_length = struct.unpack_from(endian + "IIII", data, offset)
        offset += 16
        if offset + captured_length > len(data):
            errors.append(f"pcap record {record_index} payload is truncated")
            break
        packet = data[offset:offset + captured_length]
        offset += captured_length
        timestamp = seconds + fraction / fraction_scale
        if start_time is not None and timestamp < start_time:
            continue
        if end_time is not None and timestamp > end_time:
            continue
        if captured_length < original_length:
            errors.append(f"pcap record {record_index} was snaplen-truncated")
            continue
        payload = _udp_payload(packet, link_type, destination_port)
        if payload is not None:
            events.append(TraceEvent(timestamp, 0, "pcap", -1, payload, len(payload)))
    return events, errors


def _udp_payload(packet: bytes, link_type: int, destination_port: int) -> Optional[bytes]:
    if link_type == 1:
        if len(packet) < 14:
            return None
        ether_type = struct.unpack_from("!H", packet, 12)[0]
        ip_offset = 14
        if ether_type == 0x8100 and len(packet) >= 18:
            ether_type = struct.unpack_from("!H", packet, 16)[0]
            ip_offset = 18
        if ether_type != 0x0800:
            return None
    elif link_type == 113:
        ip_offset = 16
    elif link_type == 276:
        ip_offset = 20
    elif link_type == 0:
        ip_offset = 4
    else:
        return None
    if len(packet) < ip_offset + 20 or packet[ip_offset] >> 4 != 4:
        return None
    ihl = (packet[ip_offset] & 0x0F) * 4
    if ihl < 20 or len(packet) < ip_offset + ihl + 8 or packet[ip_offset + 9] != 17:
        return None
    udp_offset = ip_offset + ihl
    _, dest_port, udp_length, _ = struct.unpack_from("!HHHH", packet, udp_offset)
    if dest_port != destination_port or udp_length < 8:
        return None
    end = udp_offset + udp_length
    if end > len(packet):
        return None
    return packet[udp_offset + 8:end]


def payload_text(payload: Optional[bytes]) -> Optional[str]:
    if payload is None:
        return None
    return payload.decode("ascii", "backslashreplace")


def payload_b64(payload: Optional[bytes]) -> Optional[str]:
    if payload is None:
        return None
    return base64.b64encode(payload).decode("ascii")


def parse_key_values(text: str) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in text.splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            values[key.strip()] = value.strip()
    return values


class Device:
    def __init__(self, host: str, user: str, password: str):
        try:
            import paramiko
            from cryptography.hazmat.primitives import hashes
        except ImportError as error:
            raise RuntimeError("tee verification requires paramiko") from error
        transport = paramiko.transport.Transport
        transport._preferred_kex = tuple(transport._preferred_kex) + (
            "diffie-hellman-group14-sha1",
            "diffie-hellman-group1-sha1",
        )
        transport._preferred_ciphers = tuple(transport._preferred_ciphers) + (
            "aes128-cbc", "aes256-cbc", "3des-cbc",
        )
        transport._preferred_macs = tuple(transport._preferred_macs) + ("hmac-sha1", "hmac-md5")
        transport._key_info = dict(transport._key_info)
        transport._key_info.setdefault("ssh-rsa", paramiko.RSAKey)
        paramiko.RSAKey.HASHES = dict(paramiko.RSAKey.HASHES)
        paramiko.RSAKey.HASHES.setdefault("ssh-rsa", hashes.SHA1)
        self.client = paramiko.SSHClient()
        self.client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        self.client.connect(
            host, username=user, password=password, port=22,
            timeout=15, banner_timeout=20, auth_timeout=20,
            look_for_keys=False, allow_agent=False,
        )

    def run(self, command: str, timeout: int = 120) -> tuple[str, str, int]:
        _, stdout, stderr = self.client.exec_command(command, timeout=timeout)
        output = stdout.read().decode("utf-8", "replace")
        error = stderr.read().decode("utf-8", "replace")
        return output, error, stdout.channel.recv_exit_status()

    def download(self, remote: str, local: Path) -> None:
        with self.client.open_sftp() as sftp:
            sftp.get(remote, str(local))

    def remove_file(self, remote: str) -> None:
        with self.client.open_sftp() as sftp:
            try:
                sftp.remove(remote)
            except OSError:
                pass

    def close(self) -> None:
        self.client.close()


def discover_uart_owner(device: Device, uart_device: str, requested_pid: Optional[int]) -> tuple[int, int, str]:
    quoted_device = shlex.quote(uart_device)
    pid_clause = f'[ "$(basename "$p")" = "{requested_pid}" ] || continue; ' if requested_pid else ""
    command = (
        "for p in /proc/[0-9]*; do " + pid_clause +
        "for f in \"$p\"/fd/*; do "
        "link=$(readlink \"$f\" 2>/dev/null || true); "
        f"[ \"$link\" = {quoted_device} ] || continue; "
        "pid=$(basename \"$p\"); fd=$(basename \"$f\"); "
        "start=$(awk '{print $22}' \"$p/stat\" 2>/dev/null); "
        "echo \"$pid $fd $start\"; "
        "done; done"
    )
    output, error, code = device.run(command)
    if code != 0:
        raise RuntimeError(f"UART owner discovery failed: {error.strip()}")
    rows = {tuple(line.split()) for line in output.splitlines() if line.strip()}
    pids = {row[0] for row in rows if len(row) == 3}
    if len(pids) != 1:
        raise RuntimeError(f"expected exactly one UART owner PID, found: {sorted(pids)}")
    matching = sorted(row for row in rows if len(row) == 3)
    if len(matching) != 1:
        raise RuntimeError(f"expected one matching UART descriptor, found: {matching}")
    pid, fd, start = matching[0]
    return int(pid), int(fd), start


def discover_gnss_pid(device: Device, requested_pid: Optional[int]) -> tuple[int, str]:
    if requested_pid:
        command = f"awk '{{print $22}}' /proc/{requested_pid}/stat 2>/dev/null"
        output, _, code = device.run(command)
        if code != 0 or not output.strip():
            raise RuntimeError(f"GNSSAgent PID {requested_pid} is not running")
        return requested_pid, output.strip()
    command = (
        "for p in /proc/[0-9]*; do "
        "exe=$(readlink \"$p/exe\" 2>/dev/null || true); name=${exe##*/}; "
        "case \"$name\" in GNSSAgent*) "
        "start=$(awk '{print $22}' \"$p/stat\" 2>/dev/null); "
        "echo \"$(basename \"$p\") $start\";; esac; done"
    )
    output, error, code = device.run(command)
    if code != 0:
        raise RuntimeError(f"GNSSAgent discovery failed: {error.strip()}")
    rows = [line.split() for line in output.splitlines() if line.strip()]
    if len(rows) != 1 or len(rows[0]) != 2:
        raise RuntimeError(f"expected exactly one GNSSAgent process, found: {rows}")
    return int(rows[0][0]), rows[0][1]


def remote_paths(run_id: str) -> dict[str, str]:
    prefix = f"/var/volatile/gnssagent-tee-{run_id}"
    return {
        "uart-read.strace": prefix + "-uart.strace",
        "gnss-recv.strace": prefix + "-gnss.strace",
        "udp.pcap": prefix + "-udp.pcap",
        "tcpdump.stats.txt": prefix + "-tcpdump.txt",
        "tty-before.txt": prefix + "-tty-before.txt",
        "tty-after.txt": prefix + "-tty-after.txt",
        "collector-status.txt": prefix + "-collector-status.txt",
        "window.txt": prefix + "-window.txt",
        "uart-attach.txt": prefix + "-uart-attach.txt",
        "gnss-attach.txt": prefix + "-gnss-attach.txt",
    }


def build_capture_command(
    paths: dict[str, str],
    owner_pid: int,
    owner_start: str,
    gnss_pid: int,
    gnss_start: str,
    tty_counter_path: str,
    seconds: int,
    udp_port: int,
) -> str:
    p = {key: shlex.quote(value) for key, value in paths.items()}
    tty = shlex.quote(tty_counter_path)
    return f"""
set -u
for tool in strace tcpdump awk grep date; do
    command -v "$tool" >/dev/null 2>&1 || {{ echo "missing tool: $tool" >&2; exit 20; }}
done
uart_tracer=''
gnss_tracer=''
packet_tracer=''
stop_collectors() {{
    [ -n "$uart_tracer" ] && kill -INT "$uart_tracer" 2>/dev/null || true
    [ -n "$gnss_tracer" ] && kill -INT "$gnss_tracer" 2>/dev/null || true
    [ -n "$packet_tracer" ] && kill -INT "$packet_tracer" 2>/dev/null || true
}}
trap stop_collectors HUP INT TERM
strace -f -ttt -s 4096 -e trace=read -p {owner_pid} -o {p['uart-read.strace']} 2>{p['uart-attach.txt']} &
uart_tracer=$!
strace -f -ttt -s 4096 -e trace=recvfrom,recvmsg -p {gnss_pid} -o {p['gnss-recv.strace']} 2>{p['gnss-attach.txt']} &
gnss_tracer=$!
tcpdump -i lo -s 0 -U -w {p['udp.pcap']} "udp dst port {udp_port}" 2>{p['tcpdump.stats.txt']} &
packet_tracer=$!
ready=no
i=0
while [ "$i" -lt 20 ]; do
    if grep -qi attached {p['uart-attach.txt']} 2>/dev/null &&
       grep -qi attached {p['gnss-attach.txt']} 2>/dev/null &&
       grep -qi listening {p['tcpdump.stats.txt']} 2>/dev/null; then
        ready=yes
        break
    fi
    i=$((i+1))
    sleep 1
done
if [ "$ready" != yes ]; then
    stop_collectors
    wait "$uart_tracer" 2>/dev/null || true
    wait "$gnss_tracer" 2>/dev/null || true
    wait "$packet_tracer" 2>/dev/null || true
    echo "collectors_ready=no" >{p['collector-status.txt']}
    exit 21
fi
tty_before=no
tty_after=no
if cat {tty} >{p['tty-before.txt']}; then tty_before=yes; fi
start_time=$(date +%s.%N)
case "$start_time" in *N*) start_time=$(date +%s);; esac
printf 'start=%s\n' "$start_time" >{p['window.txt']}
sleep {seconds}
end_time=$(date +%s.%N)
case "$end_time" in *N*) end_time=$(date +%s);; esac
printf 'end=%s\n' "$end_time" >>{p['window.txt']}
if cat {tty} >{p['tty-after.txt']}; then tty_after=yes; fi
stop_collectors
wait "$uart_tracer" 2>/dev/null; uart_exit=$?
wait "$gnss_tracer" 2>/dev/null; gnss_exit=$?
wait "$packet_tracer" 2>/dev/null; tcpdump_exit=$?
owner_after=$(awk '{{print $22}}' /proc/{owner_pid}/stat 2>/dev/null || true)
gnss_after=$(awk '{{print $22}}' /proc/{gnss_pid}/stat 2>/dev/null || true)
{{
    echo "collectors_ready=yes"
    grep -qi attached {p['uart-attach.txt']} && echo "uart_attached=yes" || echo "uart_attached=no"
    grep -qi attached {p['gnss-attach.txt']} && echo "gnss_attached=yes" || echo "gnss_attached=no"
    if [ "$uart_exit" -eq 0 ] || [ "$uart_exit" -eq 130 ]; then echo "uart_detached=yes"; else echo "uart_detached=no"; fi
    if [ "$gnss_exit" -eq 0 ] || [ "$gnss_exit" -eq 130 ]; then echo "gnss_detached=yes"; else echo "gnss_detached=no"; fi
    if [ "$tcpdump_exit" -eq 0 ] || [ "$tcpdump_exit" -eq 130 ]; then echo "tcpdump_detached=yes"; else echo "tcpdump_detached=no"; fi
    if [ "$tty_before" = yes ] && [ "$tty_after" = yes ]; then echo "tty_snapshots=yes"; else echo "tty_snapshots=no"; fi
    [ "$owner_after" = {shlex.quote(owner_start)} ] && [ "$gnss_after" = {shlex.quote(gnss_start)} ] && echo "processes_stable=yes" || echo "processes_stable=no"
    echo "uart_tracer_exit=$uart_exit"
    echo "gnss_tracer_exit=$gnss_exit"
    echo "tcpdump_exit=$tcpdump_exit"
}} >{p['collector-status.txt']}
"""


def analyze_evidence(directory: Path, uart_fd: int, udp_port: int) -> dict[str, object]:
    status = parse_key_values((directory / "collector-status.txt").read_text("utf-8", errors="replace"))
    window = parse_key_values((directory / "window.txt").read_text("utf-8", errors="replace"))
    start_time = float(window["start"])
    end_time = float(window["end"])
    uart_capture = parse_strace(
        (directory / "uart-read.strace").read_text("utf-8", errors="replace"),
        {"read"}, fd_filter=uart_fd, start_time=start_time, end_time=end_time,
    )
    receive_capture = parse_strace(
        (directory / "gnss-recv.strace").read_text("utf-8", errors="replace"),
        {"recvfrom", "recvmsg"}, start_time=start_time, end_time=end_time,
    )
    extraction = extract_complete_sentences(uart_capture.events)
    pcap_events, pcap_errors = parse_pcap_udp(
        (directory / "udp.pcap").read_bytes(), udp_port, start_time, end_time,
    )
    tcpdump_text = (directory / "tcpdump.stats.txt").read_text("utf-8", errors="replace")
    invalid_reasons = evaluate_capture(
        uart_capture, receive_capture, extraction, tcpdump_text, status, pcap_errors,
    )

    uart_payloads = [item.payload for item in extraction.sentences]
    udp_payloads = [normalize_udp_payload(event.data) for event in pcap_events]
    sequence = compare_sequences(uart_payloads, udp_payloads)
    delays = match_forward_delays(extraction.sentences, receive_capture.events)
    if not delays.valid:
        invalid_reasons.append(delays.reason or "forward-delay matching failed")
    capture_valid = not invalid_reasons
    acceptance_pass = capture_valid and sequence.exact and sequence.loss_count == 0 and sequence.duplicate_count == 0 and sequence.reorder_count == 0

    diff_lines = [
        f"capture_valid={str(capture_valid).lower()}",
        f"uart_complete_sentences={len(uart_payloads)}",
        f"udp_datagrams={len(udp_payloads)}",
        f"loss_count={sequence.loss_count}",
        f"duplicate_count={sequence.duplicate_count}",
        f"reorder_count={sequence.reorder_count}",
        f"first_difference={sequence.first_difference}",
        f"first_uart={payload_text(sequence.first_uart)}",
        f"first_udp={payload_text(sequence.first_udp)}",
    ]
    if invalid_reasons:
        diff_lines.append("invalid_reasons:")
        diff_lines.extend(f"- {reason}" for reason in invalid_reasons)
    (directory / "nmea-diff.txt").write_text("\n".join(diff_lines) + "\n", "utf-8")

    with (directory / "forward-delay.csv").open("w", newline="", encoding="utf-8") as handle:
        writer = csv.writer(handle)
        writer.writerow(["occurrence", "delay_ms"])
        for index, delay in enumerate(delays.delays_ms):
            writer.writerow([index, f"{delay:.6f}"])

    summary: dict[str, object] = {
        "capture_valid": capture_valid,
        "acceptance_pass": acceptance_pass,
        "invalid_reasons": invalid_reasons,
        "uart_complete_sentence_count": len(uart_payloads),
        "udp_datagram_count": len(udp_payloads),
        "loss_count": sequence.loss_count,
        "duplicate_count": sequence.duplicate_count,
        "reorder_count": sequence.reorder_count,
        "first_differing_index": sequence.first_difference,
        "first_uart_payload": payload_text(sequence.first_uart),
        "first_udp_payload": payload_text(sequence.first_udp),
        "first_uart_payload_base64": payload_b64(sequence.first_uart),
        "first_udp_payload_base64": payload_b64(sequence.first_udp),
        "leading_boundary_bytes_discarded": extraction.leading_discarded,
        "trailing_boundary_bytes_discarded": extraction.trailing_discarded,
        "forward_delay": {
            "valid": delays.valid and capture_valid,
            "reason": delays.reason,
            **delay_percentiles(delays.delays_ms),
        },
        "tcpdump": parse_tcpdump_stats(tcpdump_text),
        "collector_status": status,
        "uart_rx_snapshot_note": "Archived as independent sanity evidence; not equated directly to read-return bytes.",
    }
    (directory / "summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n", "utf-8")
    return summary


def run_tee_verify(args: argparse.Namespace) -> int:
    if not args.target:
        raise RuntimeError("--target is required for --tee-verify")
    if not args.uart_device or not args.tty_counter_path:
        raise RuntimeError("--uart-device and --tty-counter-path are required for --tee-verify")
    run_id = dt.datetime.now().strftime("%Y%m%dT%H%M%S")
    output_root = Path(args.output_dir).resolve()
    output_directory = output_root / f"{args.target}-{run_id}"
    output_directory.mkdir(parents=True, exist_ok=False)
    paths = remote_paths(run_id)
    device = Device(args.host, args.user, args.password)
    try:
        owner_pid, uart_fd, owner_start = discover_uart_owner(device, args.uart_device, args.owner_pid)
        gnss_pid, gnss_start = discover_gnss_pid(device, args.gnss_pid)
        command = build_capture_command(
            paths, owner_pid, owner_start, gnss_pid, gnss_start,
            args.tty_counter_path, args.seconds, args.udp_port,
        )
        output, error, code = device.run(command, timeout=args.seconds + 90)
        (output_directory / "remote-command.stdout.txt").write_text(output, "utf-8")
        (output_directory / "remote-command.stderr.txt").write_text(error, "utf-8")
        for local_name, remote_name in paths.items():
            try:
                device.download(remote_name, output_directory / local_name)
            except OSError:
                if local_name in {
                    "uart-read.strace", "gnss-recv.strace", "udp.pcap", "tcpdump.stats.txt",
                    "tty-before.txt", "tty-after.txt", "collector-status.txt", "window.txt",
                }:
                    raise RuntimeError(f"required remote artifact is missing: {remote_name}")
        if code != 0:
            raise RuntimeError(f"remote collectors failed with exit {code}; evidence retained in {output_directory}")
        summary = analyze_evidence(output_directory, uart_fd, args.udp_port)
        print(json.dumps(summary, indent=2, ensure_ascii=False))
        print(f"Evidence directory: {output_directory}")
        return 0 if summary["acceptance_pass"] else 2
    finally:
        for remote_name in paths.values():
            device.remove_file(remote_name)
        device.close()


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tee-verify", action="store_true", help="run synchronized UART-owner/UDP acceptance capture")
    parser.add_argument("--target", help="explicit device target label for the evidence directory")
    parser.add_argument("--host", default="192.168.7.2")
    parser.add_argument("--user", default="root")
    parser.add_argument("--password", default="root")
    parser.add_argument("--uart-device")
    parser.add_argument("--tty-counter-path")
    parser.add_argument("--owner-pid", type=int)
    parser.add_argument("--gnss-pid", type=int)
    parser.add_argument("--udp-port", type=int, default=29501)
    parser.add_argument("--seconds", type=int, default=60)
    parser.add_argument("--output-dir", default="evidence/gnss-tee")
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_argument_parser()
    args = parser.parse_args(argv)
    if not args.tee_verify:
        parser.error("only --tee-verify is supported")
    if args.seconds <= 0 or not (1 <= args.udp_port <= 65535):
        parser.error("--seconds and --udp-port must be positive and valid")
    try:
        return run_tee_verify(args)
    except Exception as error:  # device evidence failures must be explicit, never a silent skip
        print(f"tee verification failed: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
