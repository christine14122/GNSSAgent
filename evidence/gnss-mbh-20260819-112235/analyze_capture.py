import collections
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
from gnss_survey import parse_pcap_udp

HERE = Path(__file__).resolve().parent
events, pcap_errors = parse_pcap_udp((HERE / "udp-600s.pcap").read_bytes(), 29501)

errors = collections.Counter()
types = collections.Counter()
talkers = collections.Counter()
terminators = collections.Counter()
lengths = collections.Counter()
bad_examples = []
gsv_groups = collections.Counter()
gsv_incomplete = 0
gsv_duplicate_numbers = 0
gsv_out_of_order = 0
active_gsv = {}

def reject(reason, payload):
    errors[reason] += 1
    if len(bad_examples) < 20:
        bad_examples.append({"reason": reason, "hex": payload.hex()})

for event in events:
    payload = event.data
    lengths[len(payload)] += 1
    if not 1 <= len(payload) <= 1024: reject("bad_length", payload)
    if not payload.startswith(b"$"): reject("non_dollar_start", payload)
    if b"\x00" in payload: reject("nul", payload)
    if payload.count(b"$") != 1: reject("dollar_count_not_one", payload)
    if payload.endswith(b"\r\n"):
        terminators["CRLF"] += 1; body = payload[:-2]
    elif payload.endswith(b"\n"):
        terminators["LF"] += 1; body = payload[:-1]
    elif payload.endswith(b"\r"):
        terminators["bare_CR"] += 1; body = payload[:-1]; reject("illegal_bare_cr", payload)
    else:
        terminators["none"] += 1; body = payload
    if b"\r" in body or b"\n" in body: reject("embedded_newline", payload)
    match = re.fullmatch(br"\$([A-Z0-9]{5})([^*]*)\*([0-9A-Fa-f]{2})", body)
    if not match:
        reject("bad_nmea_shape", payload); continue
    ident = match.group(1).decode()
    talkers[ident[:2]] += 1
    types[ident[2:]] += 1
    expected = 0
    for byte in body[1:body.rfind(b"*")]: expected ^= byte
    if expected != int(match.group(3), 16): reject("checksum_failure", payload)
    if ident[2:] not in {"RMC", "GGA", "GSA", "GSV", "GST", "ZDA"}: reject("unapproved_type", payload)
    if ident.endswith("GSV"):
        fields = body[1:body.rfind(b"*")].decode("ascii").split(",")
        try: total, number = int(fields[1]), int(fields[2])
        except (ValueError, IndexError): reject("bad_gsv_header", payload); continue
        key = ident[:2]
        if number == 1:
            previous = active_gsv.get(key)
            if previous and previous[1] != previous[0]: gsv_incomplete += 1
            active_gsv[key] = [total, 1, {1}]
        elif key not in active_gsv:
            gsv_out_of_order += 1
        else:
            group = active_gsv[key]
            if number in group[2]: gsv_duplicate_numbers += 1
            if number != group[1] + 1: gsv_out_of_order += 1
            group[1] = number; group[2].add(number)
            if number == total:
                if group[2] != set(range(1, total + 1)): gsv_incomplete += 1
                else: gsv_groups[key] += 1
                del active_gsv[key]

duration = events[-1].timestamp - events[0].timestamp if len(events) > 1 else 0
log_text = (HERE / "gnssagent.log").read_text("utf-8", errors="replace")
warning_counts = collections.Counter()
for line in log_text.splitlines():
    if "level=WARN" in line:
        message = re.search(r'msg="([^"]+)"', line)
        warning_counts[message.group(1) if message else "unknown"] += 1

summary = {
    "pcap_parse_errors": pcap_errors,
    "datagrams": len(events),
    "bytes": sum(len(e.data) for e in events),
    "duration_seconds": duration,
    "datagrams_per_minute": len(events) * 60 / duration if duration else None,
    "length_min": min(lengths) if lengths else None,
    "length_max": max(lengths) if lengths else None,
    "length_distribution": dict(sorted(lengths.items())),
    "terminators": dict(terminators),
    "talkers": dict(talkers),
    "sentence_types": dict(types),
    "sentence_types_per_minute": {k: v * 60 / duration for k, v in types.items()} if duration else {},
    "contract_errors": dict(errors),
    "bad_examples": bad_examples,
    "gsv_complete_groups": dict(gsv_groups),
    "gsv_incomplete_groups": gsv_incomplete + len(active_gsv),
    "gsv_duplicate_numbers": gsv_duplicate_numbers,
    "gsv_out_of_order": gsv_out_of_order,
    "gnssagent_warning_lines": dict(warning_counts),
}
(HERE / "udp-analysis.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n", "utf-8")
print(json.dumps(summary, indent=2, ensure_ascii=False))
