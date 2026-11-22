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
import tempfile
import xml.etree.ElementTree as ET
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from email.utils import parsedate_to_datetime
from pathlib import Path
from typing import Callable

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
            title = _text(entry.find(f"{ATOM_NS}title")).strip()
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
        title = _text(item.find("title")).strip()
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
