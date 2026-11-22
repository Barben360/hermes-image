"""Shared test helpers: fixtures and article builders (no network)."""

import sys
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent / "scripts"))

import tech_radar  # noqa: E402

NOW = datetime(2026, 11, 22, 6, 0, tzinfo=timezone.utc)


def read_fixture(name: str) -> bytes:
    return (HERE / "fixtures" / name).read_bytes()


def read_fixture_text(name: str) -> str:
    return (HERE / "fixtures" / name).read_text(encoding="utf-8")


def art(title: str, link: str | None = None, description: str = "", published=NOW, source: str = "HN"):
    return tech_radar.Article(
        title=title,
        link=link or f"https://example.org/{title}",
        description=description,
        published=published,
        source=source,
    )


def arts(n: int):
    return [art(f"t{i}") for i in range(n)]
