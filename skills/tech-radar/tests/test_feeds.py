import json
import os
import tempfile
import unittest
from datetime import date, datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

from helpers import NOW, art, read_fixture
from tech_radar import (
    FeedConfig,
    fetch_feeds,
    filter_new,
    load_feeds,
    load_seen,
    parse_feed,
    save_seen,
    select_recent,
    write_atomic,
)


class FeedsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())

    def path_with_text(self, text: str) -> Path:
        p = self.tmp / "seen.json"
        p.write_text(text)
        return p

    def path_with(self, data: dict) -> Path:
        return self.path_with_text(json.dumps(data))

    def test_load_feeds_reads_shipped_file(self):
        cfg = load_feeds(Path(__file__).resolve().parent.parent / "scripts" / "feeds.json")
        self.assertEqual([n for n, _ in cfg.feeds], ["Hacker News", "Lobsters", "Ars Technica", "TechCrunch"])
        self.assertEqual((cfg.max_age_hours, cfg.max_items_per_feed), (28, 20))

    def test_parse_rss_strips_html_and_dates(self):
        items = parse_feed(read_fixture("rss.xml"), "HN")
        self.assertEqual(items[0].title, "First story")
        self.assertIn("containers", items[0].description)
        self.assertNotIn("<p>", items[0].description)
        self.assertEqual(items[0].published, datetime(2026, 11, 21, 6, 0, tzinfo=timezone.utc))
        self.assertEqual(items[0].source, "HN")
        self.assertEqual(items[1].description, "")  # too short to count

    def test_parse_feed_normalizes_titles(self):
        data = (b'<?xml version="1.0"?><rss version="2.0"><channel><item>'
                b'<title>  Two\n  lines &lt;b&gt;bold&lt;/b&gt; </title><link>https://example.org/t</link>'
                b'</item></channel></rss>')
        self.assertEqual(parse_feed(data, "HN")[0].title, "Two lines bold")

    def test_parse_atom_prefers_alternate_link(self):
        items = parse_feed(read_fixture("atom.xml"), "Lobsters")
        self.assertEqual(items[0].link, "https://example.org/alt")
        self.assertEqual(items[0].published, datetime(2026, 11, 21, 6, 0, tzinfo=timezone.utc))

    def test_select_recent_window_and_cap(self):
        old = art("old", published=NOW - timedelta(hours=29))
        undated = art("undated", published=None)
        fresh = [art(f"f{i}", published=NOW - timedelta(hours=1)) for i in range(25)]
        got = select_recent([old, undated] + fresh, NOW, 28, 20)
        self.assertNotIn(old, got)
        self.assertIn(undated, got)
        self.assertEqual(len(got), 20)

    def test_fetch_feeds_survives_one_failure(self):
        cfg = FeedConfig([("A", "https://a"), ("B", "https://b")], 28, 20)

        def get(url):
            if url == "https://b":
                raise OSError("boom")
            return read_fixture("rss.xml")

        items, failed = fetch_feeds(cfg, get, NOW)
        self.assertEqual(failed, ["B"])
        self.assertEqual([a.source for a in items], ["A", "A"])

    def test_seen_prunes_after_14_days(self):
        seen = load_seen(self.path_with({"https://x": "2026-11-07", "https://y": "2026-11-09"}), date(2026, 11, 22))
        self.assertEqual(seen, {"https://y": date(2026, 11, 9)})

    def test_seen_missing_or_corrupt_file_is_empty(self):
        self.assertEqual(load_seen(self.path_with_text("{not json"), date(2026, 11, 22)), {})
        self.assertEqual(load_seen(self.tmp / "absent.json", date(2026, 11, 22)), {})

    def test_save_seen_round_trips_and_creates_parent(self):
        p = self.tmp / "sub" / "seen.json"
        save_seen(p, {"https://x": date(2026, 11, 22)})
        self.assertEqual(load_seen(p, date(2026, 11, 22)), {"https://x": date(2026, 11, 22)})

    def test_filter_new_drops_seen_and_duplicates(self):
        items = [art("a", link="https://x"), art("b", link="https://z"), art("c", link="https://z")]
        got = filter_new(items, {"https://x": date(2026, 11, 21)}, date(2026, 11, 22))
        self.assertEqual([a.title for a in got], ["b"])

    def test_seen_today_not_filtered(self):
        got = filter_new([art("a", link="https://x")], {"https://x": date(2026, 11, 22)}, date(2026, 11, 22))
        self.assertEqual(len(got), 1)

    def test_write_atomic_leaves_no_partial_file(self):
        target = self.tmp / "note.md"
        target.write_text("old")
        with mock.patch("tech_radar.os.replace", side_effect=OSError("disk")):
            with self.assertRaises(OSError):
                write_atomic(target, "new")
        self.assertEqual(target.read_text(), "old")
        self.assertEqual(os.listdir(self.tmp), ["note.md"])


if __name__ == "__main__":
    unittest.main()
