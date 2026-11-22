#!/usr/bin/env python3
"""Tech Radar: daily tech news digest written to the owner's Obsidian vault.

Run by a Hermes cron job with no agent (`no_agent`): RSS/Atom feeds -> one
deepseek-flash call that ranks and de-duplicates -> the 10 article bodies ->
one summary per article -> a daily note -> one status line on stdout, which
Hermes posts to Matrix verbatim. Handled failures print a `❌` line and exit 0
(a non-zero exit makes Hermes wrap the output in its own failure message).

Design: my-homelab docs/superpowers/specs/2026-11-20-tech-radar-design.md.
"""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from email.utils import parsedate_to_datetime
from pathlib import Path
from typing import Callable, Protocol

SCRIPT_DIR = Path(__file__).resolve().parent

DEFAULT_MODEL = "deepseek-flash"
DEFAULT_BASE_URL = "https://api.deepseek.com"
DEFAULT_KEY_FILE = "/run/secrets/tech_radar_deepseek_key"
DEFAULT_VAULT = "/opt/vault"
DEFAULT_STATE = "/opt/data/tech-radar/seen.json"
DEFAULT_FEEDS = str(SCRIPT_DIR / "feeds.json")

INTERESTS_PATH = "Tech/Tech Radar/interests.md"
DAILIES_DIR = "Tech/Tech Radar/Dailies"
SEEN_DAYS = 14

# Descriptions shorter than this are considered absent; longer ones are cut.
MIN_DESC_LEN = 80
DESC_LIMIT = 1200


@dataclass
class Article:
    title: str
    link: str
    description: str
    published: datetime | None
    source: str


@dataclass
class FeedConfig:
    feeds: list[tuple[str, str]]  # (name, url)
    max_age_hours: int
    max_items_per_feed: int


def load_feeds(path: Path) -> FeedConfig:
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    return FeedConfig(
        feeds=[(f["name"], f["url"]) for f in data["feeds"]],
        max_age_hours=int(data.get("max_age_hours", 28)),
        max_items_per_feed=int(data.get("max_items_per_feed", 20)),
    )


# --- Feed parsing (ported from tech-radar-skill/scripts/fetch_feeds.py) -------

_TAG_RE = re.compile(r"<[^>]+>")
_WS_RE = re.compile(r"\s+")
_BLOCK_RE = re.compile(r"<(script|style)[^>]*>.*?</\1>", re.DOTALL | re.IGNORECASE)
CONTENT_NS = "{http://purl.org/rss/1.0/modules/content/}"
ATOM_NS = "{http://www.w3.org/2005/Atom}"


def strip_html(text: str) -> str:
    text = _BLOCK_RE.sub(" ", text)
    text = _TAG_RE.sub(" ", text)
    return _WS_RE.sub(" ", text).strip()


def parse_date(raw: str | None) -> datetime | None:
    if not raw:
        return None
    raw = raw.strip()
    try:
        dt = parsedate_to_datetime(raw)
    except (TypeError, ValueError, IndexError):
        try:
            dt = datetime.fromisoformat(raw.replace("Z", "+00:00"))
        except ValueError:
            return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def _text(el: ET.Element | None) -> str:
    return (el.text or "") if el is not None else ""


def _desc(*candidates: str) -> str:
    for raw in candidates:
        text = strip_html(raw)
        if len(text) >= MIN_DESC_LEN:
            return text[:DESC_LIMIT]
    return ""


def parse_feed(data: bytes, source: str) -> list[Article]:
    root = ET.fromstring(data)
    items: list[Article] = []
    if root.tag == f"{ATOM_NS}feed":
        for entry in root.iter(f"{ATOM_NS}entry"):
            link_el = entry.find(f"{ATOM_NS}link[@rel='alternate']")
            if link_el is None:
                link_el = entry.find(f"{ATOM_NS}link")
            link = link_el.get("href", "") if link_el is not None else ""
            title = strip_html(_text(entry.find(f"{ATOM_NS}title")))
            if title and link.strip():
                items.append(Article(
                    title=title,
                    link=link.strip(),
                    description=_desc(_text(entry.find(f"{ATOM_NS}content")), _text(entry.find(f"{ATOM_NS}summary"))),
                    published=parse_date(_text(entry.find(f"{ATOM_NS}published")) or _text(entry.find(f"{ATOM_NS}updated"))),
                    source=source,
                ))
        return items
    for item in root.iter("item"):
        title = strip_html(_text(item.find("title")))
        link = _text(item.find("link")).strip()
        if title and link:
            items.append(Article(
                title=title,
                link=link,
                description=_desc(_text(item.find(f"{CONTENT_NS}encoded")), _text(item.find("description"))),
                published=parse_date(_text(item.find("pubDate"))),
                source=source,
            ))
    return items


def select_recent(items: list[Article], now: datetime, max_age_hours: int, max_items: int) -> list[Article]:
    cutoff = now - timedelta(hours=max_age_hours)
    return [a for a in items if a.published is None or a.published >= cutoff][:max_items]


def fetch_feeds(cfg: FeedConfig, get: Callable[[str], bytes], now: datetime) -> tuple[list[Article], list[str]]:
    articles: list[Article] = []
    failed: list[str] = []
    for name, url in cfg.feeds:
        try:
            items = parse_feed(get(url), name)
        except Exception:  # one broken feed never stops the others
            failed.append(name)
            continue
        articles.extend(select_recent(items, now, cfg.max_age_hours, cfg.max_items_per_feed))
    return articles, failed


# --- Links already sent ---------------------------------------------------------

def load_seen(path: Path, today: date) -> dict[str, date]:
    try:
        raw = json.loads(Path(path).read_text(encoding="utf-8"))
        seen = {str(link): date.fromisoformat(day) for link, day in raw.items()}
    except (OSError, ValueError, AttributeError, TypeError):
        return {}  # lost or unreadable: a repeat is harmless
    oldest = today - timedelta(days=SEEN_DAYS)
    return {link: day for link, day in seen.items() if day >= oldest}


def filter_new(items: list[Article], seen: dict[str, date], today: date) -> list[Article]:
    """Drop links sent on an earlier day (a rerun the same day keeps them) and duplicate links."""
    out: list[Article] = []
    links: set[str] = set()
    for a in items:
        if a.link in links or (a.link in seen and seen[a.link] < today):
            continue
        links.add(a.link)
        out.append(a)
    return out


def write_atomic(path: Path, text: str) -> None:
    """Write through a temp file in the same folder: readers never see half a file."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(text)
        os.chmod(tmp, 0o644)
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


def save_seen(path: Path, seen: dict[str, date]) -> None:
    write_atomic(path, json.dumps({link: day.isoformat() for link, day in sorted(seen.items())}, indent=1) + "\n")


# --- Model -------------------------------------------------------------------------

class LlmError(Exception):
    """The model call failed or its answer was not usable."""


class Llm(Protocol):
    def complete_json(self, system: str, user: str) -> dict: ...


class DeepSeekLlm:
    """DeepSeek's OpenAI-compatible API in JSON output mode. Errors never carry the key."""

    def __init__(self, api_key: str, base_url: str, model: str):
        import openai  # in Hermes's venv; imported here so the rest runs without it

        self._key = api_key
        self._model = model
        self._client = openai.OpenAI(api_key=api_key, base_url=base_url, timeout=120, max_retries=2)

    def _hide(self, text: str) -> str:
        return text.replace(self._key, "***") if self._key else text

    def complete_json(self, system: str, user: str) -> dict:
        try:
            resp = self._client.chat.completions.create(
                model=self._model,
                messages=[{"role": "system", "content": system}, {"role": "user", "content": user}],
                response_format={"type": "json_object"},
                temperature=0.2,
            )
            content = resp.choices[0].message.content or ""
        except Exception as e:
            raise LlmError(self._hide(f"{type(e).__name__}: {e}")) from None
        try:
            data = json.loads(content)
        except ValueError:
            raise LlmError("answer is not JSON") from None
        if not isinstance(data, dict):
            raise LlmError("answer is not a JSON object")
        return data


# --- Ranking -------------------------------------------------------------------------

RANK_DESC_LIMIT = 400

RANK_SYSTEM = (
    "You pick tech news for a personalized daily digest. You get the reader's interests and a "
    "numbered list of candidate articles. Choose the {top} articles most relevant to the interests: "
    "topics under \"Core Topics\" outweigh those under \"Watching\". When several articles cover the "
    "same news, keep only the most informative one. Score each chosen article 0-10 for relevance. "
    'Answer with a JSON object only: {{"selected": [{{"id": <number>, "score": <0-10>}}, ...]}}, '
    "best first, at most {top} entries."
)


def _int(value) -> int | None:
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def rank(llm: Llm, interests: str, items: list[Article], top: int = 10) -> list[Article]:
    lines = []
    for i, a in enumerate(items):
        desc = a.description[:RANK_DESC_LIMIT]
        lines.append(f"[{i}] {a.title} ({a.source})" + (f"\n    {desc}" if desc else ""))
    user = f"<Interests>\n{interests.strip()}\n</Interests>\n\n<Articles>\n" + "\n".join(lines) + "\n</Articles>"
    picked: dict[int, int] = {}
    for attempt in range(2):  # one retry: JSON mode can return an empty or unusable answer
        try:
            picked = _picked(llm.complete_json(RANK_SYSTEM.format(top=top), user), len(items))
        except LlmError:
            if attempt:
                raise
            continue
        if picked:
            break
    if not picked:
        raise LlmError("no article selected")
    order = sorted(picked, key=lambda i: picked[i], reverse=True)  # stable: ties keep the model's order
    return [items[i] for i in order[:top]]


def _picked(answer: dict, count: int) -> dict[int, int]:
    """Valid selected ids (in range, first occurrence) → score."""
    selected = answer.get("selected")
    picked: dict[int, int] = {}
    for entry in selected if isinstance(selected, list) else []:
        if not isinstance(entry, dict):
            continue
        i = _int(entry.get("id"))
        if i is None or not 0 <= i < count or i in picked:
            continue
        picked[i] = _int(entry.get("score")) or 0
    return picked


# --- Summaries (prompt ported from tech-radar-skill/scripts/summarize_article.py) ---

@dataclass
class Summary:
    emoji: str
    keywords: list[str]
    summary: str


SUMMARY_SYSTEM = """You summarize tech news articles for a personalized daily digest.
For each article, output a single JSON object with EXACTLY these three keys and nothing else:
  - "emoji": one Unicode emoji that fits the article's primary topic (e.g. 🤖 AI, 🔐 security, 🦀 Rust, ☁️ cloud, 🐧 Linux, 🛜 networking, 🧠 ML research, 📦 packaging).
  - "keywords": an array of 3 to 5 short, specific Title-Case strings (e.g. ["DevOps","Kubernetes","Self-Hosting"]). No spaces inside a keyword unless natural.
  - "summary": a 3 to 4 sentence paragraph describing what the article actually covers — what was announced or discovered, key technical details or findings, and why it is relevant to the user's interests. Be specific. Avoid generic filler like "this is important because…".
If the article body is unavailable, summarize from the title and say the content was unavailable.

Example input:
<Interests>
- AI/LLM inference
- Self-hosting
</Interests>

<Article title="vLLM 0.7 adds speculative decoding for Llama 3">
The vLLM team released v0.7 with built-in speculative decoding using a small draft model. Benchmarks on Llama 3 70B show 2.1x throughput at the same latency on H100s. The release also adds AWQ quantization fixes and OpenAI-compatible structured outputs.
</Article>

Example output:
{"emoji": "🤖", "keywords": ["AI", "LLM", "Inference", "vLLM", "Self-Hosting"], "summary": "vLLM 0.7 introduces built-in speculative decoding driven by a small draft model, delivering a 2.1x throughput gain on Llama 3 70B at unchanged latency on H100 GPUs. The release also ships AWQ quantization fixes and OpenAI-compatible structured outputs. For self-hosted inference stacks, this materially lowers the cost of serving large open models."}"""

FALLBACK_EMOJI = "📰"
UNAVAILABLE = "Content unavailable."


def _valid_summary(data: dict) -> Summary | None:
    emoji, keywords, summary = data.get("emoji"), data.get("keywords"), data.get("summary")
    if not isinstance(emoji, str) or not emoji.strip() or not isinstance(summary, str) or not summary.strip():
        return None
    if not isinstance(keywords, list):
        keywords = []
    words = [str(k).strip() for k in keywords if str(k).strip()][:5]
    return Summary(emoji.strip(), words, summary.strip())


def summarize(llm: Llm, interests: str, article: Article, body: str) -> Summary:
    user = (
        f"<Interests>\n{interests.strip()}\n</Interests>\n\n"
        f"<Article title={json.dumps(article.title, ensure_ascii=False)}>\n"
        f"{body.strip() or '(article body unavailable)'}\n</Article>"
    )
    for _ in range(2):  # one retry
        try:
            result = _valid_summary(llm.complete_json(SUMMARY_SYSTEM, user))
        except LlmError:
            result = None
        if result:
            return result
    return Summary(FALLBACK_EMOJI, [], article.description or UNAVAILABLE)


# --- Article bodies --------------------------------------------------------------

BODY_LIMIT = 3000
MIN_BODY_LEN = 200
_CHROME = ("script", "style", "noscript", "nav", "header", "footer", "aside", "form")


def extract_text(html: str) -> str:
    import lxml.html

    try:
        doc = lxml.html.document_fromstring(html)
    except Exception:  # lxml.etree.ParserError on empty or broken input
        return ""
    for el in doc.xpath("|".join(f"//{t}" for t in _CHROME)):
        el.drop_tree()
    for path in ("//article", "//main", "//body"):
        found = doc.xpath(path)
        if found:
            return _WS_RE.sub(" ", found[0].text_content()).strip()
    return _WS_RE.sub(" ", doc.text_content()).strip()


def fetch_body(get_page: Callable[[str], tuple[str, bytes]], article: Article) -> str:
    try:
        content_type, data = get_page(article.link)
        if "html" not in content_type.lower():
            return article.description
        text = extract_text(data.decode("utf-8", errors="replace"))
    except Exception:
        return article.description
    if len(text) < MIN_BODY_LEN:
        return article.description
    return text[:BODY_LIMIT]


# --- The note --------------------------------------------------------------------

PARIS = "Europe/Paris"


def _paris_day(dt: datetime) -> str:
    from zoneinfo import ZoneInfo

    return dt.astimezone(ZoneInfo(PARIS)).date().isoformat()


def _link_text(title: str) -> str:
    return title.replace("\\", "\\\\").replace("[", "\\[").replace("]", "\\]")


def note_path(day: date) -> str:
    return f"{DAILIES_DIR}/{day:%Y}/{day:%m}/{day.isoformat()}"


def render_note(day: date, at: str, items: list[tuple[Article, Summary]]) -> str:
    out = ["---", f"date: {day.isoformat()}", "type: tech-radar", "---", f"# 📡 Tech Radar — {day.isoformat()}", ""]
    for article, s in items:
        published = _paris_day(article.published) if article.published else "—"
        out.append(f"## {s.emoji} [{_link_text(article.title)}]({article.link})")
        out.append(f"**Source:** {article.source} | **Published:** {published}")
        if s.keywords:
            out.append(f"**Keywords:** {', '.join(s.keywords)}")
        out += ["", s.summary, ""]
    out += ["---", f"*Generated by Hermes at {at}*", ""]
    return "\n".join(out)


# --- The run -----------------------------------------------------------------------

WORKERS = 5


class StepError(Exception):
    def __init__(self, step: str, reason: str):
        super().__init__(f"{step}: {reason}")


def _fail(reason: str) -> str:
    return f"❌ Error generating tech radar ({reason})"


def run(now: datetime, vault: Path, state: Path, feeds: Path, llm: Llm,
        get: Callable[[str], bytes], get_page: Callable[[str], tuple[str, bytes]]) -> str:
    """One radar run; returns the status line. Never raises for a handled failure."""
    today = now.date()
    try:
        try:
            interests = (Path(vault) / INTERESTS_PATH).read_text(encoding="utf-8")
        except OSError as e:
            raise StepError("interests", f"cannot read {INTERESTS_PATH} ({type(e).__name__})")
        if not interests.strip():
            raise StepError("interests", f"{INTERESTS_PATH} is empty")

        cfg = load_feeds(feeds)
        candidates, failed = fetch_feeds(cfg, get, now)
        if len(failed) == len(cfg.feeds):
            raise StepError("feeds", "every feed failed")
        seen = load_seen(state, today)
        candidates = filter_new(candidates, seen, today)
        if not candidates:
            return "ℹ️ No new tech news since the last radar"

        try:
            chosen = rank(llm, interests, candidates)
        except LlmError as e:
            raise StepError("rank", str(e))

        with ThreadPoolExecutor(max_workers=WORKERS) as pool:
            bodies = list(pool.map(lambda a: fetch_body(get_page, a), chosen))
            summaries = list(pool.map(lambda ab: summarize(llm, interests, ab[0], ab[1]), zip(chosen, bodies)))

        rel = note_path(today)
        try:
            write_atomic(Path(vault) / f"{rel}.md", render_note(today, f"{now:%H:%M}", list(zip(chosen, summaries))))
        except OSError as e:
            raise StepError("write", str(e.strerror or e))
    except StepError as e:
        return _fail(str(e))

    line = f"✅ Daily tech radar available to {rel} on Obsidian"
    if failed:
        line += f" (skipped: {', '.join(failed)})"
    seen.update({a.link: today for a in chosen})
    try:
        save_seen(state, seen)
    except OSError:
        line += " (warning: seen list not saved)"
    return line


# --- Entry point -------------------------------------------------------------------

USER_AGENT = "TechRadar/2.0"
HTTP_TIMEOUT = 15
MAX_PAGE_BYTES = 2 * 1024 * 1024


def _http_fetch(url: str) -> tuple[str, bytes]:
    import httpx

    with httpx.stream("GET", url, timeout=HTTP_TIMEOUT, follow_redirects=True, headers={"User-Agent": USER_AGENT}) as r:
        r.raise_for_status()
        chunks, size = [], 0
        for chunk in r.iter_bytes():
            chunks.append(chunk)
            size += len(chunk)
            if size >= MAX_PAGE_BYTES:
                break
        return r.headers.get("content-type", ""), b"".join(chunks)[:MAX_PAGE_BYTES]


def http_get(url: str) -> bytes:
    return _http_fetch(url)[1]


def http_page(url: str) -> tuple[str, bytes]:
    return _http_fetch(url)


def main() -> int:
    from zoneinfo import ZoneInfo

    env = os.environ.get
    try:
        key = Path(env("TECH_RADAR_KEY_FILE", DEFAULT_KEY_FILE)).read_text(encoding="utf-8").strip()
    except OSError:
        key = ""
    if not key:
        print(_fail("config: key file missing"))
        return 0
    try:
        llm = DeepSeekLlm(key, env("TECH_RADAR_BASE_URL", DEFAULT_BASE_URL), env("TECH_RADAR_MODEL", DEFAULT_MODEL))
        line = run(
            datetime.now(ZoneInfo(PARIS)),
            Path(env("TECH_RADAR_VAULT", DEFAULT_VAULT)),
            Path(env("TECH_RADAR_STATE", DEFAULT_STATE)),
            Path(env("TECH_RADAR_FEEDS", DEFAULT_FEEDS)),
            llm, http_get, http_page,
        )
    except Exception as e:
        # Unhandled: the class name only, no message and no traceback (either could
        # quote the key, and Hermes posts stderr), then exit 1 so Hermes reports a crash.
        print(_fail(f"unexpected: {type(e).__name__}"), flush=True)
        raise SystemExit(1) from None
    print(line.replace(key, "***"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
