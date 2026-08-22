import collections
import ipaddress
import json
import re
import struct
from pathlib import Path

HERE = Path(__file__).resolve().parent
data = (HERE / "udp-60s.pcap").read_bytes()
formats = {b"\xd4\xc3\xb2\xa1": ("<", 1e6), b"\xa1\xb2\xc3\xd4": (">", 1e6),
           b"\x4d\x3c\xb2\xa1": ("<", 1e9), b"\xa1\xb2\x3c\x4d": (">", 1e9)}
endian, scale = formats[data[:4]]
linktype = struct.unpack_from(endian + "I", data, 20)[0]
offset = 24
packets = []
while offset < len(data):
    sec, frac, caplen, wirelen = struct.unpack_from(endian + "IIII", data, offset)
    offset += 16
    raw = data[offset:offset + caplen]
    offset += caplen
    ipoff = 14 if linktype == 1 else 4
    if len(raw) < ipoff + 28 or raw[ipoff] >> 4 != 4 or raw[ipoff + 9] != 17:
        continue
    ihl = (raw[ipoff] & 15) * 4
    udpoff = ipoff + ihl
    sport, dport, ulen, _ = struct.unpack_from("!HHHH", raw, udpoff)
    if dport != 29501 or udpoff + ulen > len(raw):
        continue
    src = str(ipaddress.IPv4Address(raw[ipoff + 12:ipoff + 16]))
    dst = str(ipaddress.IPv4Address(raw[ipoff + 16:ipoff + 20]))
    packets.append((sec + frac / scale, src, sport, dst, dport, raw[udpoff + 8:udpoff + ulen]))

errors = collections.Counter()
types = collections.Counter()
talkers = collections.Counter()
endpoints = collections.Counter()
lengths = collections.Counter()
terminators = collections.Counter()
examples = []
bad_examples = []

def fail(reason, payload):
    errors[reason] += 1
    if len(bad_examples) < 10:
        bad_examples.append({"reason": reason, "hex": payload.hex()})

for _, src, sport, dst, dport, payload in packets:
    endpoints[f"{src}:{sport} -> {dst}:{dport}"] += 1
    lengths[len(payload)] += 1
    if len(examples) < 8:
        examples.append(payload.decode("ascii", "backslashreplace"))
    if not payload.startswith(b"$"): fail("not_dollar_start", payload)
    if len(payload) > 1024: fail("over_1024", payload)
    if b"\x00" in payload: fail("contains_nul", payload)
    if payload.count(b"$") != 1: fail("multiple_or_missing_dollar", payload)
    if payload.endswith(b"\r\n"):
        terminators["CRLF"] += 1; body = payload[:-2]
    elif payload.endswith(b"\n"):
        terminators["LF"] += 1; body = payload[:-1]
    else:
        terminators["none"] += 1; body = payload
    if b"\r" in body or b"\n" in body: fail("embedded_newline", payload)
    m = re.fullmatch(br"\$([A-Z0-9]{5})([^*]*)\*([0-9A-Fa-f]{2})", body)
    if not m:
        fail("missing_or_malformed_checksum", payload); continue
    ident = m.group(1).decode("ascii")
    talkers[ident[:2]] += 1
    types[ident[2:]] += 1
    checksum = 0
    for byte in body[1:body.rfind(b"*")]: checksum ^= byte
    if checksum != int(m.group(3), 16): fail("checksum_mismatch", payload)

duration = packets[-1][0] - packets[0][0] if len(packets) > 1 else 0
summary = {
    "packet_count": len(packets), "duration_seconds_between_first_last": duration,
    "frequency_packets_per_second": len(packets) / duration if duration else None,
    "endpoints": dict(endpoints), "length_min": min(lengths) if lengths else None,
    "length_max": max(lengths) if lengths else None,
    "length_distribution": dict(sorted(lengths.items())), "terminators": dict(terminators),
    "talkers": dict(talkers), "sentence_types": dict(types), "errors": dict(errors),
    "examples": examples, "bad_examples": bad_examples,
}
(HERE / "udp-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", "utf-8")
print(json.dumps(summary, ensure_ascii=False, indent=2))
