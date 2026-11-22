import contextlib
import io
import json
import os
import tempfile
import threading
import unittest
from datetime import date, datetime, timedelta, timezone
from pathlib import Path
from unittest import mock
from zoneinfo import ZoneInfo

from helpers import art, read_fixture_text
import tech_radar
from tech_radar import Article, LlmError, Summary, extract_text, fetch_body, main, render_note, run

PARIS = ZoneInfo("Europe/Paris")
NOW_PARIS = datetime(2026, 11, 22, 7, 0, tzinfo=PARIS)
NOTE = "Tech/Tech Radar/Dailies/2026/11/2026-11-22.md"
OK_LINE = "✅ Daily tech radar available to Tech/Tech Radar/Dailies/2026/11/2026-11-22 on Obsidian"

GOLDEN_ITEMS = [
    (Article("Docker [beta] ships", "https://example.org/a", "", datetime(2026, 11, 21, 10, 0, tzinfo=timezone.utc), "Hacker News"),
     Summary("🐳", ["Docker", "Compose"], "Summary sentence.")),
    (Article("Second", "https://example.org/b", "Feed description.", None, "Lobsters"),
     Summary("📰", [], "Feed description.")),
]


def rss(source: str, n: int) -> bytes:
    pub = "Sun, 22 Nov 2026 05:00:00 +0000"
    items = "".join(
        f"<item><title>{source} story {i}</title><link>https://{source.lower()}.example/{i}</link>"
        f"<description>{'A long enough description of the story. ' * 3}</description><pubDate>{pub}</pubDate></item>"
        for i in range(n)
    )
    return f'<?xml version="1.0"?><rss version="2.0"><channel>{items}</channel></rss>'.encode()


class RoutingLlm:
    """Ranking answer for the ranking prompt, a summary for every other call (thread-safe)."""

    def __init__(self, rank_answer=None, rank_error=None):
        self.rank_answer = rank_answer or {"selected": [{"id": i, "score": 10 - i} for i in range(10)]}
        self.rank_error = rank_error
        self.lock = threading.Lock()
        self.summaries = 0

    def complete_json(self, system, user):
        if system.startswith("You pick tech news"):
            if self.rank_error:
                raise self.rank_error
            return self.rank_answer
        with self.lock:
            self.summaries += 1
        return {"emoji": "🐳", "keywords": ["Docker"], "summary": "A summary."}


def page_ok(url):
    return "text/html; charset=utf-8", ("<html><body><article><p>" + "Body text. " * 40 + "</p></article></body></html>").encode()


class RunTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.vault = self.tmp / "vault"
        (self.vault / "Tech/Tech Radar").mkdir(parents=True)
        (self.vault / "Tech/Tech Radar/interests.md").write_text("## Core Topics\n- Docker\n")
        self.state = self.tmp / "state" / "seen.json"
        self.feeds = self.tmp / "feeds.json"
        self.feeds.write_text(json.dumps({"feeds": [
            {"name": "Hacker News", "url": "https://hn"}, {"name": "Lobsters", "url": "https://lobsters"}],
            "max_age_hours": 28, "max_items_per_feed": 20}))

    def get_ok(self, url):
        return rss("Hacker News" if url == "https://hn" else "Lobsters", 8)

    def go(self, llm=None, get=None, get_page=page_ok, vault=None):
        return run(NOW_PARIS, vault or self.vault, self.state, self.feeds, llm or RoutingLlm(), get or self.get_ok, get_page)

    def test_render_matches_golden(self):
        self.assertEqual(render_note(date(2026, 11, 22), "07:00", GOLDEN_ITEMS), read_fixture_text("note.md"))

    def test_render_escapes_brackets(self):
        self.assertIn(r"[Docker \[beta\] ships](https://example.org/a)", render_note(date(2026, 11, 22), "07:00", GOLDEN_ITEMS))

    def test_extract_text_drops_chrome(self):
        t = extract_text("<html><body><nav>menu</nav><article><p>Real   text</p></article><footer>f</footer></body></html>")
        self.assertEqual(t, "Real text")

    def test_extract_text_without_article_uses_body(self):
        t = extract_text("<html><body><header>h</header><div>Main <b>part</b></div><script>x()</script></body></html>")
        self.assertEqual(t, "Main part")

    def test_fetch_body_non_html_falls_back(self):
        self.assertEqual(fetch_body(lambda u: ("application/pdf", b"%PDF"), art("t", description="d")), "d")

        def boom(u):
            raise OSError("403")

        self.assertEqual(fetch_body(boom, art("t", description="d")), "d")
        self.assertEqual(fetch_body(lambda u: ("text/html", b"<p>tiny</p>"), art("t", description="d")), "d")

    def test_fetch_body_cuts_to_3000(self):
        body = fetch_body(page_ok, art("t"))
        self.assertTrue(body.startswith("Body text."))
        big = lambda u: ("text/html", ("<p>" + "word " * 2000 + "</p>").encode())
        self.assertEqual(len(fetch_body(big, art("t"))), 3000)

    def test_fetch_body_non_utf8_does_not_crash(self):
        latin = lambda u: ("text/html", ("<p>" + "café " * 100 + "</p>").encode("latin-1"))
        self.assertIn("caf", fetch_body(latin, art("t", description="d")))

    def test_run_success_writes_note_and_seen(self):
        llm = RoutingLlm()
        self.assertEqual(self.go(llm), OK_LINE)
        note = (self.vault / NOTE).read_text()
        self.assertTrue(note.startswith("---\ndate: 2026-11-22\ntype: tech-radar\n---\n"))
        self.assertEqual(note.count("\n## 🐳 ["), 10)
        self.assertEqual(llm.summaries, 10)
        seen = json.loads(self.state.read_text())
        self.assertEqual(len(seen), 10)
        self.assertEqual(set(seen.values()), {"2026-11-22"})

    def test_run_rerun_same_day_rebuilds(self):
        self.assertEqual(self.go(), OK_LINE)
        (self.vault / NOTE).unlink()
        self.assertEqual(self.go(), OK_LINE)
        self.assertTrue((self.vault / NOTE).exists())

    def test_run_partial_feed_failure_names_feed(self):
        def get(url):
            if url == "https://lobsters":
                raise OSError("down")
            return rss("Hacker News", 12)

        self.assertEqual(self.go(get=get), OK_LINE + " (skipped: Lobsters)")

    def test_run_all_feeds_fail(self):
        def get(url):
            raise OSError("down")

        self.assertEqual(self.go(get=get), "❌ Error generating tech radar (feeds: every feed failed)")
        self.assertFalse(list(self.vault.rglob("2026-11-22.md")))

    def test_run_missing_interests(self):
        empty = self.tmp / "empty"
        empty.mkdir()
        self.assertTrue(self.go(vault=empty).startswith("❌ Error generating tech radar (interests:"))

    def test_run_rank_error_writes_nothing(self):
        line = self.go(RoutingLlm(rank_error=LlmError("no article selected")))
        self.assertEqual(line, "❌ Error generating tech radar (rank: no article selected)")
        self.assertFalse(list(self.vault.rglob("2026-11-22.md")))
        self.assertFalse(self.state.exists())

    def test_run_nothing_new(self):
        self.state.parent.mkdir(parents=True)
        links = {f"https://{s}.example/{i}": "2026-11-21" for s in ("hacker news", "lobsters") for i in range(8)}
        self.state.write_text(json.dumps(links))
        self.assertEqual(self.go(), "ℹ️ No new tech news since the last radar")
        self.assertFalse(list(self.vault.rglob("2026-11-22.md")))

    def test_run_write_error(self):
        with mock.patch("tech_radar.write_atomic", side_effect=OSError("read-only")):
            self.assertEqual(self.go(), "❌ Error generating tech radar (write: read-only)")

    def test_run_seen_save_error_is_a_warning(self):
        real = tech_radar.write_atomic

        def flaky(path, text):
            if Path(path).name == "seen.json":
                raise OSError("full")
            return real(path, text)

        with mock.patch("tech_radar.write_atomic", side_effect=flaky):
            self.assertEqual(self.go(), OK_LINE + " (warning: seen list not saved)")


class MainTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        vault = self.tmp / "vault"
        (vault / "Tech/Tech Radar").mkdir(parents=True)
        (vault / "Tech/Tech Radar/interests.md").write_text("- Docker\n")
        key = self.tmp / "key"
        key.write_text("sk-secret123\n")
        self.env = {"TECH_RADAR_KEY_FILE": str(key), "TECH_RADAR_VAULT": str(vault),
                    "TECH_RADAR_STATE": str(self.tmp / "seen.json")}

    def call_main(self):
        out, err = io.StringIO(), io.StringIO()
        with mock.patch.dict(os.environ, self.env), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err), \
                mock.patch("tech_radar.http_get", return_value=rss("Hacker News", 5)), \
                mock.patch("tech_radar.http_page", side_effect=page_ok):
            code = main()
        return code, out.getvalue(), err.getvalue()

    def test_main_never_prints_key(self):
        with mock.patch("openai.OpenAI") as client_cls:
            client_cls.return_value.chat.completions.create.side_effect = Exception("sk-secret123 rejected")
            code, out, err = self.call_main()
        self.assertEqual(code, 0)
        self.assertTrue(out.startswith("❌ Error generating tech radar (rank:"), out)
        self.assertNotIn("sk-secret123", out + err)

    def test_main_reads_key_file_and_model(self):
        with mock.patch("openai.OpenAI") as client_cls:
            client_cls.return_value.chat.completions.create.side_effect = Exception("x")
            self.call_main()
        kwargs = client_cls.call_args.kwargs
        self.assertEqual((kwargs["api_key"], kwargs["base_url"]), ("sk-secret123", "https://api.deepseek.com"))

    def test_main_missing_key_file(self):
        self.env["TECH_RADAR_KEY_FILE"] = str(self.tmp / "absent")
        code, out, _ = self.call_main()
        self.assertEqual((code, out.strip()), (0, "❌ Error generating tech radar (config: key file missing)"))

    def test_main_unexpected_error_exits_1_without_key(self):
        out, err = io.StringIO(), io.StringIO()
        with mock.patch.dict(os.environ, self.env), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err), \
                mock.patch("tech_radar.run", side_effect=RuntimeError("boom sk-secret123")):
            with self.assertRaises(SystemExit) as cm:
                main()
        self.assertEqual(cm.exception.code, 1)
        self.assertEqual(out.getvalue().strip(), "❌ Error generating tech radar (unexpected: RuntimeError)")
        self.assertNotIn("sk-secret123", out.getvalue() + err.getvalue())


if __name__ == "__main__":
    unittest.main()
