import struct
import unittest

from gnss_survey import (
    SentenceExtraction,
    SentenceOccurrence,
    TraceCapture,
    TraceEvent,
    compare_sequences,
    decode_c_string,
    delay_percentiles,
    evaluate_capture,
    extract_complete_sentences,
    match_forward_delays,
    nearest_rank,
    parse_pcap_udp,
    parse_strace,
)


class CStringAndStraceTests(unittest.TestCase):
    def test_c_escapes_and_unfinished_resumed_reconstruction(self):
        self.assertEqual(decode_c_string(r"A\n\x42\103\0"), b"A\nBC\x00")
        capture = parse_strace(
            "\n".join([
                r'100 1700000000.100000 read(4, "A\n\x42\103", 16) = 4',
                r'100 1700000000.200000 read(4,  <unfinished ...>',
                r'100 1700000000.250000 <... read resumed>"$X*00\r\n", 4096) = 7',
            ]),
            {"read"},
            fd_filter=4,
        )
        self.assertTrue(capture.valid, capture.errors)
        self.assertEqual([event.data for event in capture.events], [b"A\nBC", b"$X*00\r\n"])
        self.assertEqual(capture.events[1].timestamp, 1700000000.25)

    def test_length_mismatch_truncation_and_unmatched_calls_are_invalid(self):
        mismatch = parse_strace(
            r'100 1700000000.100000 read(4, "$AB", 16) = 4', {"read"}, fd_filter=4
        )
        self.assertIn("decoded 3 bytes", mismatch.errors[0])

        truncated = parse_strace(
            r'100 1700000000.100000 read(4, "$ABC"..., 16) = 4', {"read"}, fd_filter=4
        )
        self.assertIn("truncated", truncated.errors[0])

        unmatched = parse_strace(
            r'100 1700000000.100000 <... read resumed>"$X*00", 4096) = 5',
            {"read"}, fd_filter=4,
        )
        self.assertIn("unmatched resumed", unmatched.errors[0])

        unresolved = parse_strace(
            r'100 1700000000.100000 read(4,  <unfinished ...>', {"read"}, fd_filter=4
        )
        self.assertIn("unresolved unfinished", unresolved.errors[0])


class SentenceAndSequenceTests(unittest.TestCase):
    def test_only_leading_and_trailing_half_sentences_are_excluded(self):
        events = [
            TraceEvent(1.0, 1, "read", 4, b"TAIL\r\n$A*00\r", 12),
            TraceEvent(2.0, 1, "read", 4, b"\n$B*00\r\n$HALF", 15),
        ]
        result = extract_complete_sentences(events)
        self.assertEqual([item.payload for item in result.sentences], [b"$A*00", b"$B*00"])
        self.assertEqual([item.completed_at for item in result.sentences], [2.0, 2.0])
        self.assertGreater(result.leading_discarded, 0)
        self.assertGreater(result.trailing_discarded, 0)
        self.assertEqual(result.errors, [])

    def test_exact_loss_duplicate_reorder_and_first_mismatch(self):
        a, b, c = b"$A", b"$B", b"$C"
        exact = compare_sequences([a, b, c], [a, b, c])
        self.assertTrue(exact.exact)
        self.assertEqual((exact.loss_count, exact.duplicate_count, exact.reorder_count), (0, 0, 0))

        loss = compare_sequences([a, b, c], [a, c])
        self.assertEqual(loss.loss_count, 1)
        self.assertEqual(loss.first_difference, 1)
        self.assertEqual((loss.first_uart, loss.first_udp), (b, c))

        duplicate = compare_sequences([a, b], [a, a, b])
        self.assertEqual(duplicate.duplicate_count, 1)

        reorder = compare_sequences([a, b, c], [b, a, c])
        self.assertEqual(reorder.reorder_count, 1)
        self.assertEqual((reorder.loss_count, reorder.duplicate_count), (0, 0))
        self.assertEqual(reorder.first_difference, 0)


class CaptureAndDelayTests(unittest.TestCase):
    @staticmethod
    def complete_status():
        return {
            "collectors_ready": "yes",
            "uart_attached": "yes",
            "gnss_attached": "yes",
            "uart_detached": "yes",
            "gnss_detached": "yes",
            "tcpdump_detached": "yes",
            "tty_snapshots": "yes",
            "processes_stable": "yes",
        }

    def test_nonzero_tcpdump_drop_invalidates_capture(self):
        reasons = evaluate_capture(
            TraceCapture([TraceEvent(1.0, 1, "read", 4, b"$A\r\n", 4)], []),
            TraceCapture([], []),
            SentenceExtraction([SentenceOccurrence(b"$A", 1.0)], 0, 0, []),
            "10 packets captured\n10 packets received by filter\n2 packets dropped by kernel\n",
            self.complete_status(),
        )
        self.assertTrue(any("2 packets dropped" in reason for reason in reasons))

    def test_missing_collector_proofs_and_empty_capture_are_invalid(self):
        reasons = evaluate_capture(
            TraceCapture([], []),
            TraceCapture([], []),
            SentenceExtraction([], 0, 0, []),
            "0 packets captured\n0 packets received by filter\n0 packets dropped by kernel\n",
            {},
        )
        self.assertTrue(any("no complete UART NMEA" in reason for reason in reasons))
        for key in ("collectors_ready", "tcpdump_detached", "tty_snapshots"):
            self.assertTrue(any(key in reason for reason in reasons), reasons)

    def test_fifo_matching_handles_repeated_sentences(self):
        uart = [
            SentenceOccurrence(b"$A*00", 10.0),
            SentenceOccurrence(b"$A*00", 11.0),
            SentenceOccurrence(b"$B*00", 12.0),
        ]
        receives = [
            TraceEvent(10.1, 1, "recvmsg", 5, b"$A*00\r\n", 7),
            TraceEvent(11.2, 1, "recvmsg", 5, b"$A*00", 5),
            TraceEvent(12.3, 1, "recvmsg", 5, b"$B*00\n", 6),
        ]
        result = match_forward_delays(uart, receives)
        self.assertTrue(result.valid, result.reason)
        self.assertEqual(len(result.delays_ms), 3)
        self.assertAlmostEqual(result.delays_ms[0], 100.0)
        self.assertAlmostEqual(result.delays_ms[1], 200.0)
        self.assertAlmostEqual(result.delays_ms[2], 300.0)

    def test_missing_or_negative_delay_fails(self):
        uart = [SentenceOccurrence(b"$A", 2.0), SentenceOccurrence(b"$B", 3.0)]
        receives = [TraceEvent(1.0, 1, "recvfrom", 5, b"$A", 2)]
        result = match_forward_delays(uart, receives)
        self.assertFalse(result.valid)
        self.assertEqual((result.missing_count, result.negative_count), (1, 1))

    def test_nearest_rank_percentiles(self):
        values = [4.0, 1.0, 3.0, 2.0]
        self.assertEqual(nearest_rank(values, 0.50), 2.0)
        self.assertEqual(nearest_rank(values, 0.95), 4.0)
        self.assertEqual(nearest_rank(values, 0.99), 4.0)
        summary = delay_percentiles(values)
        self.assertEqual(summary["sample_count"], 4)
        self.assertEqual(summary["max_ms"], 4.0)


class PcapTests(unittest.TestCase):
    def test_extracts_complete_ipv4_udp_payload(self):
        payload = b"$GPGSV,1,1,00*79\r\n"
        udp = struct.pack("!HHHH", 40000, 29501, 8 + len(payload), 0) + payload
        ip_total = 20 + len(udp)
        ip = bytes([0x45, 0]) + struct.pack("!H", ip_total) + bytes(5) + bytes([17]) + bytes(2) + bytes(8)
        ethernet = bytes(12) + struct.pack("!H", 0x0800)
        packet = ethernet + ip + udp
        global_header = b"\xd4\xc3\xb2\xa1" + struct.pack("<HHIIII", 2, 4, 0, 0, 65535, 1)
        record = struct.pack("<IIII", 100, 500000, len(packet), len(packet)) + packet
        events, errors = parse_pcap_udp(global_header + record, 29501)
        self.assertEqual(errors, [])
        self.assertEqual([event.data for event in events], [payload])
        self.assertEqual(events[0].timestamp, 100.5)


if __name__ == "__main__":
    unittest.main()
