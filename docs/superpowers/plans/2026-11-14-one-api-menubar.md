# one-api macOS 菜单栏用量应用 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a macOS menu-bar app (Python + rumps) that shows the one-api account's last-7-days daily token totals and per-model usage, living in `mac-plugins/one-api-menubar/`.

**Architecture:** Two layers — a pure-logic module `oneapi_usage.py` (stdlib-only: config, HTTP fetch, aggregation, formatting, cache) that is fully unit-testable on Linux, and a thin `menubar/one-api-menubar.py` rumps app (NSStatusItem + NSMenu, background fetch thread, timers, JSON cache, LaunchAgent). Data comes from a single existing endpoint `GET /api/user/dashboard` (no backend changes).

**Tech Stack:** Python 3.12 (stdlib: `urllib`, `json`, `dataclasses`, `unittest`), `rumps` + `py2app` for the macOS bundle (only needed on macOS).

## Global Constraints

- Target dir is `mac-plugins/one-api-menubar/` in repo `one-api`. Spec: `docs/superpowers/specs/2026-11-14-one-api-menubar-design.md`.
- Language of app is Python; the pure-logic module and its tests use **only the standard library** (no requests/httpx). Logic tests MUST run on Linux with `python3 -m unittest`.
- Data source is exactly `GET {base_url}/api/user/dashboard` with header `Authorization: Bearer <access_token>`. No query params, no backend change.
- Response rows are PascalCase keys: `Day, ModelName, RequestCount, Quota, PromptTokens, CompletionTokens`. Total tokens = `PromptTokens + CompletionTokens` (there is no `total_tokens` field). Quota is NOT displayed.
- Missing calendar days are absent from the API payload; the app fills all 7 trailing days with 0 so the sparkline and daily rows always show 7 entries.
- Config file `~/.one-api-menubar.json`; cache `~/.one-api-menubar-cache.json` (TTL 55s); refresh interval default 60s, minimum 30s; language default `zh`; bar default mode `today`.
- Top-bar title format: `⚡ <tokens> <7-cell sparkline>` with spark chars `▁▂▃▄▅▆▇█`.
- Reference patterns (threading, cache, login item, tests-with-stubs) come from `/home/onehao/workspace/codex/ai-limit`.

---

## File Structure

```
mac-plugins/one-api-menubar/
├── oneapi_usage.py             # pure logic: config, exceptions, aggregation, formatting, fetch, cache
├── menubar/
│   ├── one-api-menubar.py      # rumps App (NSStatusItem + NSMenu), threads, timers, login item
│   ├── setup.py                # py2app build, LSUIElement=True
│   └── make-dmg.sh             # hdiutil packaging
├── one-api-menubar.example.json
├── tests/
│   ├── test_oneapi_usage.py    # pure-logic unit tests (stdlib only)
│   └── test_app.py             # menu_entries + orchestration logic via stubbed rumps/AppKit
└── README.md
```

Task dependency: Tasks 1-7 build `oneapi_usage.py` bottom-up; Task 8 builds the app on top of it; Task 9 packages + documents.

---

## Task 1: Scaffold + config load/save + exceptions

**Files:**
- Create: `mac-plugins/one-api-menubar/oneapi_usage.py`
- Create: `mac-plugins/one-api-menubar/tests/__init__.py` (empty)
- Create: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py`

**Interfaces:**
- Produces:
  - `CONFIG_PATH: str`, `CACHE_PATH: str`, `SPARK_CHARS: str`, `DEFAULT_CONFIG: dict`
  - `class OneApiError(Exception)`, `class UsageError(OneApiError)`, `class NetworkError(OneApiError)`
  - `load_config(path: str = CONFIG_PATH, environ: dict | None = None) -> dict`
  - `save_config(config: dict, path: str = CONFIG_PATH) -> None`

- [ ] **Step 1: Write the failing test**

Create `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py`:

```python
import json
import os
import tempfile
import unittest

import oneapi_usage as U


class ConfigTest(unittest.TestCase):
    def test_defaults_when_no_file(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "nope.json")
            cfg = U.load_config(path=path, environ={})
            self.assertEqual(cfg["refresh_sec"], 60)
            self.assertEqual(cfg["lang"], "zh")
            self.assertEqual(cfg["bar_mode"], "today")
            self.assertEqual(cfg["base_url"], "")

    def test_file_and_env_override_and_clamp(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "c.json")
            with open(path, "w") as f:
                json.dump({"base_url": "http://host/", "access_token": "t",
                           "refresh_sec": 5, "lang": "en"}, f)
            cfg = U.load_config(path=path, environ={"ONEAPI_ACCESS_TOKEN": "envtok"})
            self.assertEqual(cfg["base_url"], "http://host/")
            self.assertEqual(cfg["access_token"], "envtok")  # env wins
            self.assertGreaterEqual(cfg["refresh_sec"], 30)  # clamped to min 30
            self.assertEqual(cfg["lang"], "en")

    def test_invalid_enum_falls_back(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "c.json")
            with open(path, "w") as f:
                json.dump({"lang": "fr", "bar_mode": "weird"}, f)
            cfg = U.load_config(path=path, environ={})
            self.assertEqual(cfg["lang"], "zh")
            self.assertEqual(cfg["bar_mode"], "today")

    def test_save_roundtrip(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "c.json")
            U.save_config({"base_url": "http://x", "access_token": "a",
                           "refresh_sec": 90, "lang": "zh", "bar_mode": "total"}, path)
            cfg = U.load_config(path=path, environ={})
            self.assertEqual(cfg["bar_mode"], "total")
            self.assertEqual(cfg["refresh_sec"], 90)


class ExceptionTest(unittest.TestCase):
    def test_hierarchy(self):
        self.assertTrue(issubclass(U.UsageError, U.OneApiError))
        self.assertTrue(issubclass(U.NetworkError, U.OneApiError))
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `mac-plugins/one-api-menubar/`): `python3 -m unittest tests.test_oneapi_usage -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'oneapi_usage'` (module not created yet).

- [ ] **Step 3: Write minimal implementation**

Create `mac-plugins/one-api-menubar/oneapi_usage.py`:

```python
import json
import os

CONFIG_PATH = os.path.expanduser("~/.one-api-menubar.json")
CACHE_PATH = os.path.expanduser("~/.one-api-menubar-cache.json")
SPARK_CHARS = "▁▂▃▄▅▆▇█"

DEFAULT_CONFIG = {
    "base_url": "",
    "access_token": "",
    "refresh_sec": 60,
    "lang": "zh",
    "bar_mode": "today",
}


class OneApiError(Exception):
    pass


class UsageError(OneApiError):
    pass


class NetworkError(OneApiError):
    pass


def load_config(path=CONFIG_PATH, environ=None):
    environ = os.environ if environ is None else environ
    path = environ.get("ONEAPI_MENUBAR_CONFIG", path)
    cfg = dict(DEFAULT_CONFIG)
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
        if isinstance(data, dict):
            cfg.update(data)
    except (FileNotFoundError, ValueError):
        pass

    if environ.get("ONEAPI_BASE_URL"):
        cfg["base_url"] = environ["ONEAPI_BASE_URL"]
    if environ.get("ONEAPI_ACCESS_TOKEN"):
        cfg["access_token"] = environ["ONEAPI_ACCESS_TOKEN"]
    if environ.get("ONEAPI_REFRESH_SEC"):
        try:
            cfg["refresh_sec"] = int(environ["ONEAPI_REFRESH_SEC"])
        except ValueError:
            pass

    try:
        cfg["refresh_sec"] = max(30, int(cfg.get("refresh_sec", 60)))
    except (TypeError, ValueError):
        cfg["refresh_sec"] = 60
    if cfg.get("lang") not in ("zh", "en"):
        cfg["lang"] = "zh"
    if cfg.get("bar_mode") not in ("today", "total"):
        cfg["bar_mode"] = "today"
    return cfg


def save_config(config, path=CONFIG_PATH):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(config, f, ensure_ascii=False, indent=2)
```

Create empty `mac-plugins/one-api-menubar/tests/__init__.py`.

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage -v`
Expected: PASS (all ConfigTest + ExceptionTest).

- [ ] **Step 5: Commit**

```bash
cd mac-plugins/one-api-menubar
git add oneapi_usage.py tests/__init__.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): scaffold one-api menu-bar app with config load/save"
```

---

## Task 2: Day aggregation (with zero-fill) + totals

**Files:**
- Modify: `mac-plugins/one-api-menubar/oneapi_usage.py` (append)
- Modify: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py` (append)

**Interfaces:**
- Produces:
  - `@dataclass DayStat: day: str, tokens: int, requests: int`
  - `last_n_days(today: datetime.date | None = None, n: int = 7) -> list[str]`  (ascending ISO `YYYY-MM-DD`, ending at today)
  - `aggregate_by_day(stats: list[dict], days: list[str]) -> list[DayStat]`  (fills every day in `days`, 0 if absent)
  - `total_tokens(stats: list[dict]) -> int`
  - `today_total(stats: list[dict], today: datetime.date | None = None) -> int`

- [ ] **Step 1: Write the failing test**

Append to `tests/test_oneapi_usage.py`:

```python
import datetime


def _stat(day, model, tokens_p, tokens_c, reqs):
    return {"Day": day, "ModelName": model,
            "PromptTokens": tokens_p, "CompletionTokens": tokens_c,
            "RequestCount": reqs, "Quota": 0}


class DayAggregateTest(unittest.TestCase):
    def test_last_n_days_length_and_order(self):
        today = datetime.date(2026, 11, 14)
        days = U.last_n_days(today=today, n=7)
        self.assertEqual(len(days), 7)
        self.assertEqual(days[0], "2026-11-08")
        self.assertEqual(days[-1], "2026-11-14")

    def test_fills_missing_days_with_zero(self):
        days = ["2026-11-08", "2026-11-09", "2026-11-10"]
        stats = [_stat("2026-11-08", "gpt-4o", 100, 50, 2),
                 _stat("2026-11-10", "gpt-4o", 1, 1, 1)]
        daily = U.aggregate_by_day(stats, days)
        self.assertEqual([d.day for d in daily], days)
        self.assertEqual(daily[1].tokens, 0)         # 2026-11-09 absent -> 0
        self.assertEqual(daily[0].tokens, 150)       # 100 + 50
        self.assertEqual(daily[2].requests, 1)

    def test_total_and_today(self):
        stats = [_stat("2026-11-14", "a", 10, 5, 1), _stat("2026-11-13", "a", 7, 3, 2)]
        self.assertEqual(U.total_tokens(stats), 25)
        self.assertEqual(U.today_total(stats, today=datetime.date(2026, 11, 14)), 15)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_oneapi_usage.DayAggregateTest -v`
Expected: FAIL — `AttributeError: module 'oneapi_usage' has no attribute 'aggregate_by_day'`.

- [ ] **Step 3: Write minimal implementation**

Append to `oneapi_usage.py`:

```python
import datetime
from dataclasses import dataclass


@dataclass
class DayStat:
    day: str
    tokens: int
    requests: int


def last_n_days(today=None, n=7):
    today = today or datetime.date.today()
    return [(today - datetime.timedelta(days=i)).isoformat() for i in range(n - 1, -1, -1)]


def aggregate_by_day(stats, days):
    tokens = {d: 0 for d in days}
    reqs = {d: 0 for d in days}
    for s in stats:
        d = s.get("Day")
        if d in tokens:
            tokens[d] += int(s.get("PromptTokens", 0)) + int(s.get("CompletionTokens", 0))
            reqs[d] += int(s.get("RequestCount", 0))
    return [DayStat(d, tokens[d], reqs[d]) for d in days]


def total_tokens(stats):
    return sum(int(s.get("PromptTokens", 0)) + int(s.get("CompletionTokens", 0)) for s in stats)


def today_total(stats, today=None):
    today = (today or datetime.date.today()).isoformat()
    return sum(
        int(s.get("PromptTokens", 0)) + int(s.get("CompletionTokens", 0))
        for s in stats if s.get("Day") == today
    )
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage.DayAggregateTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add oneapi_usage.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): day aggregation with zero-fill and totals"
```

---

## Task 3: Per-model aggregation

**Files:**
- Modify: `mac-plugins/one-api-menubar/oneapi_usage.py` (append)
- Modify: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py` (append)

**Interfaces:**
- Consumes: `_stat` helper (Task 2), `PromptTokens/CompletionTokens/RequestCount` keys.
- Produces:
  - `@dataclass ModelStat: name: str, tokens: int, requests: int, share: float`
  - `aggregate_by_model(stats: list[dict]) -> list[ModelStat]`  (sorted by tokens desc, then name asc; `share` = model tokens / total tokens, 0.0 when total is 0)

- [ ] **Step 1: Write the failing test**

Append to `tests/test_oneapi_usage.py`:

```python
class ModelAggregateTest(unittest.TestCase):
    def test_groups_sorts_and_share(self):
        stats = [_stat("2026-11-08", "gpt-4o", 100, 100, 3),
                 _stat("2026-11-09", "gpt-4o", 0, 200, 1),
                 _stat("2026-11-09", "claude", 50, 50, 2)]
        models = U.aggregate_by_model(stats)
        self.assertEqual([m.name for m in models], ["gpt-4o", "claude"])  # desc tokens
        self.assertEqual(models[0].tokens, 400)
        self.assertEqual(models[0].requests, 4)
        self.assertEqual(models[1].tokens, 100)
        self.assertAlmostEqual(models[0].share, 400 / 500, places=6)
        self.assertAlmostEqual(models[1].share, 100 / 500, places=6)

    def test_zero_total_share(self):
        models = U.aggregate_by_model([_stat("2026-11-08", "m", 0, 0, 1)])
        self.assertEqual(models[0].share, 0.0)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_oneapi_usage.ModelAggregateTest -v`
Expected: FAIL — `AttributeError: ... has no attribute 'aggregate_by_model'`.

- [ ] **Step 3: Write minimal implementation**

Append to `oneapi_usage.py`:

```python
@dataclass
class ModelStat:
    name: str
    tokens: int
    requests: int
    share: float


def aggregate_by_model(stats):
    tokens = {}
    reqs = {}
    for s in stats:
        name = s.get("ModelName") or "(unknown)"
        tokens[name] = tokens.get(name, 0) + int(s.get("PromptTokens", 0)) + int(s.get("CompletionTokens", 0))
        reqs[name] = reqs.get(name, 0) + int(s.get("RequestCount", 0))
    total = sum(tokens.values())
    models = [ModelStat(name, tok, reqs[name], (tok / total) if total else 0.0)
              for name, tok in tokens.items()]
    models.sort(key=lambda m: (-m.tokens, m.name))
    return models
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage.ModelAggregateTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add oneapi_usage.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): per-model aggregation with share and ordering"
```

---

## Task 4: Formatting — tokens, sparkline, inline bars

**Files:**
- Modify: `mac-plugins/one-api-menubar/oneapi_usage.py` (append)
- Modify: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py` (append)

**Interfaces:**
- Consumes: `SPARK_CHARS` (Task 1).
- Produces:
  - `format_tokens(n: int) -> str`  (`>=1_000_000` → `"x.xM"`, `>=1000` → `"x.xk"`, else decimal string)
  - `sparkline(values: list[int], width: int = 7) -> str`  (linear scale to max; all-zero → all `SPARK_CHARS[0]`; `width==len(values)`)
  - `bar(value: int, max_value: int, width: int = 5) -> str`  (`█`*filled + `░`*rest; `value>0` gets >=1 filled; `max_value<=0` → all `░`)

- [ ] **Step 1: Write the failing test**

Append to `tests/test_oneapi_usage.py`:

```python
class FormatTest(unittest.TestCase):
    def test_format_tokens(self):
        self.assertEqual(U.format_tokens(999), "999")
        self.assertEqual(U.format_tokens(1500), "1.5k")
        self.assertEqual(U.format_tokens(3_200_000), "3.2M")
        self.assertEqual(U.format_tokens(0), "0")

    def test_sparkline_scales_to_max(self):
        spark = U.sparkline([0, 5, 10], width=3)
        self.assertEqual(spark, U.SPARK_CHARS[0] + U.SPARK_CHARS[4] + U.SPARK_CHARS[7])

    def test_sparkline_all_zero(self):
        self.assertEqual(U.sparkline([0, 0, 0], width=3), U.SPARK_CHARS[0] * 3)

    def test_sparkline_width_matches_values(self):
        self.assertEqual(len(U.sparkline([1, 2, 3, 4, 5, 6, 7])), 7)

    def test_bar_scaling(self):
        self.assertEqual(U.bar(10, 10, 5), "█████")
        self.assertEqual(U.bar(0, 10, 5), "░░░░░")
        self.assertEqual(len(U.bar(3, 10, 5)), 5)
        self.assertEqual(U.bar(1, 100, 5)[:1], "█")   # nonzero gets >=1 block
        self.assertEqual(U.bar(5, 0, 5), "░░░░░")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_oneapi_usage.FormatTest -v`
Expected: FAIL — `AttributeError: ... has no attribute 'format_tokens'`.

- [ ] **Step 3: Write minimal implementation**

Append to `oneapi_usage.py`:

```python
def format_tokens(n):
    n = int(n)
    if n >= 1_000_000:
        return "{:.1f}M".format(n / 1_000_000)
    if n >= 1000:
        return "{:.1f}k".format(n / 1000)
    return str(n)


def sparkline(values, width=7):
    values = list(values)[:width]
    top = max(values) if values else 0
    if top <= 0:
        return SPARK_CHARS[0] * len(values)
    out = []
    last = len(SPARK_CHARS) - 1
    for v in values:
        idx = max(0, min(last, int(round(v / top * last))))
        out.append(SPARK_CHARS[idx])
    return "".join(out)


def bar(value, max_value, width=5):
    value = int(value)
    if max_value <= 0 or value <= 0:
        filled = 0 if value <= 0 else width
    else:
        filled = int(round(value / max_value * width))
        filled = max(1, min(width, filled))
    return "█" * filled + "░" * (width - filled)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage.FormatTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add oneapi_usage.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): token formatting, sparkline and inline bars"
```

---

## Task 5: HTTP fetch layer (injectable urlopen)

**Files:**
- Modify: `mac-plugins/one-api-menubar/oneapi_usage.py` (append)
- Modify: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py` (append)

**Interfaces:**
- Consumes: `UsageError`, `NetworkError` (Task 1).
- Produces:
  - `fetch_dashboard(base_url: str, access_token: str, timeout: int = 15, urlopen=None) -> list[dict]`
    - URL: `{base_url}/api/user/dashboard`, header `Authorization: Bearer {access_token}`.
    - Raises `UsageError` on 401/403 or `success:false`; raises `NetworkError` on connection failure; returns `data` list otherwise.

- [ ] **Step 1: Write the failing test**

Append to `tests/test_oneapi_usage.py`:

```python
import io


class _FakeResp:
    def __init__(self, body): self._b = body.encode("utf-8")
    def read(self): return self._b
    def __enter__(self): return self
    def __exit__(self, *a): return False


class FetchTest(unittest.TestCase):
    def test_success_returns_data_and_sends_bearer(self):
        captured = {}
        def fake_urlopen(req, timeout=None):
            captured["url"] = req.full_url
            captured["auth"] = req.get_header("Authorization")
            return _FakeResp('{"success":true,"data":[{"Day":"2026-11-08"}]}')
        data = U.fetch_dashboard("http://host/", "tok", urlopen=fake_urlopen)
        self.assertEqual(captured["url"], "http://host/api/user/dashboard")
        self.assertEqual(captured["auth"], "Bearer tok")
        self.assertEqual(data, [{"Day": "2026-11-08"}])

    def test_auth_error(self):
        import urllib.error
        def fake_urlopen(req, timeout=None):
            raise urllib.error.HTTPError(req.full_url, 401, "no", {}, None)
        with self.assertRaises(U.UsageError):
            U.fetch_dashboard("http://host", "bad", urlopen=fake_urlopen)

    def test_network_error(self):
        import urllib.error
        def fake_urlopen(req, timeout=None):
            raise urllib.error.URLError("boom")
        with self.assertRaises(U.NetworkError):
            U.fetch_dashboard("http://host", "tok", urlopen=fake_urlopen)

    def test_api_success_false(self):
        def fake_urlopen(req, timeout=None):
            return _FakeResp('{"success":false,"message":"nope"}')
        with self.assertRaises(U.UsageError):
            U.fetch_dashboard("http://host", "tok", urlopen=fake_urlopen)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_oneapi_usage.FetchTest -v`
Expected: FAIL — `AttributeError: ... has no attribute 'fetch_dashboard'`.

- [ ] **Step 3: Write minimal implementation**

Append to `oneapi_usage.py`:

```python
import urllib.error
import urllib.request


def fetch_dashboard(base_url, access_token, timeout=15, urlopen=None):
    if not base_url:
        raise UsageError("base_url not configured")
    url = base_url.rstrip("/") + "/api/user/dashboard"
    req = urllib.request.Request(url, headers={
        "Authorization": "Bearer " + access_token,
        "Accept": "application/json",
    })
    opener = urlopen or urllib.request.urlopen
    try:
        resp = opener(req, timeout=timeout)
    except urllib.error.HTTPError as e:
        if e.code in (401, 403):
            raise UsageError("auth_failed")
        raise UsageError("http_{}".format(e.code))
    except (urllib.error.URLError, OSError) as e:
        raise NetworkError(str(e))
    obj = json.loads(resp.read().decode("utf-8"))
    if not obj.get("success"):
        raise UsageError(obj.get("message") or "api_error")
    return obj.get("data") or []
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage.FetchTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add oneapi_usage.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): dashboard fetch layer with injectable urlopen and error mapping"
```

---

## Task 6: Row/title builders + labels

**Files:**
- Modify: `mac-plugins/one-api-menubar/oneapi_usage.py` (append)
- Modify: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py` (append)

**Interfaces:**
- Consumes: `DayStat`, `ModelStat`, `format_tokens`, `bar`, `sparkline`, `SPARK_CHARS`.
- Produces:
  - `LABELS: dict[str, dict[str, str]]` (keys `zh`, `en`)
  - `daily_rows(daily: list[DayStat], labels: dict) -> list[str]`
  - `model_rows(models: list[ModelStat], labels: dict) -> list[str]`
  - `refresh_line(refresh_str: str, total: int, labels: dict) -> str`
  - `build_bar_title(daily: list[DayStat], bar_mode: str, today: str) -> str`

- [ ] **Step 1: Write the failing test**

Append to `tests/test_oneapi_usage.py`:

```python
class RowBuilderTest(unittest.TestCase):
    def setUp(self):
        self.zh = U.LABELS["zh"]
        self.daily = [U.DayStat("2026-11-08", 300, 40),
                      U.DayStat("2026-11-09", 0, 0),
                      U.DayStat("2026-11-10", 600, 80)]
        self.models = [U.ModelStat("gpt-4o", 600, 80, 0.6667),
                       U.ModelStat("claude", 300, 40, 0.3333)]

    def test_daily_rows_have_day_tokens_count_and_bar(self):
        rows = U.daily_rows(self.daily, self.zh)
        self.assertEqual(len(rows), 3)
        self.assertIn("2026-11-08", rows[0])
        self.assertIn("300", rows[0])
        self.assertTrue(rows[0].endswith("40" + self.zh["req"]))
        self.assertIn("░", rows[1])          # 0-token day shows empty bar

    def test_model_rows_show_share_percent(self):
        rows = U.model_rows(self.models, self.zh)
        self.assertIn("gpt-4o", rows[0])
        self.assertIn("67%", rows[0])        # round(0.6667*100)
        self.assertIn("%", rows[1])

    def test_refresh_line(self):
        line = U.refresh_line("07:17", 900, self.zh)
        self.assertIn("07:17", line)
        self.assertIn("900", line)

    def test_bar_title_today_and_total(self):
        spark = U.sparkline([300, 0, 600], width=3)
        self.assertEqual(U.build_bar_title(self.daily, "today", "2026-11-10"),
                         "⚡ 600 " + spark)
        self.assertEqual(U.build_bar_title(self.daily, "total", "2026-11-10"),
                         "⚡ 900 " + spark)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_oneapi_usage.RowBuilderTest -v`
Expected: FAIL — `AttributeError: ... has no attribute 'LABELS'`.

- [ ] **Step 3: Write minimal implementation**

Append to `oneapi_usage.py`:

```python
LABELS = {
    "zh": {
        "daily": "近7天用量", "model": "模型用量 (近7天)", "req": "次",
        "refresh": "最近刷新", "total": "共", "tokens_word": "tokens",
        "settings": "请配置 base_url 与 access_token",
        "stale": "数据过时", "auth": "令牌无效，请检查 access_token",
        "lang": "语言", "bar_mode": "菜单栏显示", "interval": "刷新间隔",
        "bar_today": "今日", "bar_total": "7日总量",
        "refresh_now": "立即刷新", "console": "打开 one-api 控制台",
        "login": "开机自启", "about": "关于", "quit": "退出",
        "interval_choices": [30, 60, 120],
    },
    "en": {
        "daily": "Last 7 days", "model": "By model (last 7 days)", "req": "req",
        "refresh": "Last refresh", "total": "total", "tokens_word": "tokens",
        "settings": "Configure base_url and access_token",
        "stale": "stale", "auth": "Invalid token, check access_token",
        "lang": "Language", "bar_mode": "Menu bar shows", "interval": "Interval",
        "bar_today": "Today", "bar_total": "7-day total",
        "refresh_now": "Refresh now", "console": "Open one-api console",
        "login": "Launch at login", "about": "About", "quit": "Quit",
        "interval_choices": [30, 60, 120],
    },
}


def daily_rows(daily, labels):
    maxv = max((d.tokens for d in daily), default=0)
    return ["{}  {}  {:>7}  {}{}".format(
        d.day, bar(d.tokens, maxv, 5), format_tokens(d.tokens),
        d.requests, labels["req"]) for d in daily]


def model_rows(models, labels):
    maxv = max((m.tokens for m in models), default=0)
    return ["{:<18}{}  {:>7}  {:>3}%  {}{}".format(
        m.name, bar(m.tokens, maxv, 5), format_tokens(m.tokens),
        int(round(m.share * 100)), m.requests, labels["req"]) for m in models]


def refresh_line(refresh_str, total, labels):
    return "{} {} · {} {} {}".format(
        labels["refresh"], refresh_str, labels["total"], format_tokens(total), labels["tokens_word"])


def build_bar_title(daily, bar_mode, today):
    if bar_mode == "total":
        num = sum(d.tokens for d in daily)
    else:
        num = sum(d.tokens for d in daily if d.day == today)
    spark = sparkline([d.tokens for d in daily], width=max(1, len(daily)))
    return "⚡ {} {}".format(format_tokens(num), spark)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage.RowBuilderTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add oneapi_usage.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): daily/model row builders, labels and bar title"
```

---

## Task 7: Cache with TTL

**Files:**
- Modify: `mac-plugins/one-api-menubar/oneapi_usage.py` (append)
- Modify: `mac-plugins/one-api-menubar/tests/test_oneapi_usage.py` (append)

**Interfaces:**
- Consumes: `CACHE_PATH`.
- Produces:
  - `save_cache(data: list[dict], path: str = CACHE_PATH, now: float | None = None) -> None`
  - `load_cache(path: str = CACHE_PATH, now: float | None = None, ttl: int = 55) -> tuple[list[dict] | None, bool]`  (returns `(data, is_fresh)`; missing file → `(None, False)`)

- [ ] **Step 1: Write the failing test**

Append to `tests/test_oneapi_usage.py`:

```python
class CacheTest(unittest.TestCase):
    def test_save_then_fresh_load(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "c.json")
            U.save_cache([{"Day": "2026-11-08"}], path=p, now=1000.0)
            data, fresh = U.load_cache(path=p, now=1040.0, ttl=55)
            self.assertTrue(fresh)
            self.assertEqual(data, [{"Day": "2026-11-08"}])

    def test_stale_after_ttl(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "c.json")
            U.save_cache([{"Day": "2026-11-08"}], path=p, now=1000.0)
            data, fresh = U.load_cache(path=p, now=1060.0, ttl=55)
            self.assertFalse(fresh)
            self.assertEqual(data, [{"Day": "2026-11-08"}])  # data still returned

    def test_missing_file(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "nope.json")
            self.assertEqual(U.load_cache(path=p), (None, False))
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_oneapi_usage.CacheTest -v`
Expected: FAIL — `AttributeError: ... has no attribute 'save_cache'`.

- [ ] **Step 3: Write minimal implementation**

Append to `oneapi_usage.py`:

```python
import time


def save_cache(data, path=CACHE_PATH, now=None):
    now = time.time() if now is None else now
    with open(path, "w", encoding="utf-8") as f:
        json.dump({"ts": now, "data": data}, f, ensure_ascii=False)


def load_cache(path=CACHE_PATH, now=None, ttl=55):
    now = time.time() if now is None else now
    try:
        with open(path, "r", encoding="utf-8") as f:
            obj = json.load(f)
    except (FileNotFoundError, ValueError):
        return None, False
    data = obj.get("data")
    ts = obj.get("ts", 0)
    return data, (now - ts) <= ttl
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_oneapi_usage.CacheTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add oneapi_usage.py tests/test_oneapi_usage.py
git commit -m "feat(menubar): response cache with TTL"
```

---

## Task 8: The rumps menu-bar app

**Files:**
- Create: `mac-plugins/one-api-menubar/menubar/one-api-menubar.py`
- Create: `mac-plugins/one-api-menubar/tests/test_app.py`

**Interfaces:**
- Consumes: everything from `oneapi_usage` (Tasks 1-7).
- Produces (pure, testable without AppKit):
  - `menu_entries(daily, models, config, refresh_str, total, error=None) -> list[dict]`
    each dict: `{"title": str, "disabled": bool, "sep": bool, "action": str|None, "checked": bool|None}`
  - `launch_plist_path() -> str`, `is_login_enabled() -> bool`, `set_login_item(enable: bool) -> None`

- [ ] **Step 1: Write the failing test**

Create `mac-plugins/one-api-menubar/tests/test_app.py`:

```python
import os
import sys
import types
import unittest

# Stub rumps + AppKit BEFORE importing the app module.
_rumps = types.ModuleType("rumps")
class _App:
    def __init__(self, *a, **k): pass
    def run(self): pass
_rumps.App = _App
_rumps.timer = lambda *a, **k: (lambda f: f)
_rumps.clicked = lambda *a, **k: (lambda f: f)
_rumps.Item = object
_rumps.separator = object
sys.modules["rumps"] = _rumps
sys.modules.setdefault("AppKit", types.ModuleType("AppKit"))
sys.modules.setdefault("Foundation", types.ModuleType("Foundation"))

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "menubar"))
import importlib
app_mod = importlib.import_module("one-api-menubar")


class MenuEntriesTest(unittest.TestCase):
    def setUp(self):
        self.daily = [app_mod.U.DayStat("2026-11-08", 300, 40),
                      app_mod.U.DayStat("2026-11-09", 600, 80)]
        self.models = [app_mod.U.ModelStat("gpt-4o", 900, 120, 1.0)]
        self.cfg = {"lang": "zh", "bar_mode": "today", "base_url": "http://h", "access_token": "t"}

    def titles(self, entries):
        return [e["title"] for e in entries]

    def test_includes_daily_and_model_sections(self):
        entries = app_mod.menu_entries(self.daily, self.models, self.cfg, "07:17", 900)
        t = self.titles(entries)
        self.assertTrue(any("近7天用量" in x for x in t))
        self.assertTrue(any("2026-11-08" in x for x in t))
        self.assertTrue(any("gpt-4o" in x for x in t))
        self.assertTrue(any("最近刷新" in x for x in t))

    def test_actions_present_and_login_checked_flag(self):
        entries = app_mod.menu_entries(self.daily, self.models, self.cfg, "07:17", 900)
        actions = {e["action"] for e in entries if e.get("action")}
        self.assertIn("refresh_now", actions)
        self.assertIn("toggle_bar_mode", actions)
        self.assertIn("set_login", actions)

    def test_missing_config_shows_settings_row(self):
        cfg = dict(self.cfg); cfg["base_url"] = ""
        entries = app_mod.menu_entries(self.daily, self.models, cfg, "07:17", 900)
        self.assertTrue(any("请配置" in x for x in self.titles(entries)))

    def test_auth_error_shows_auth_row(self):
        entries = app_mod.menu_entries(self.daily, self.models, self.cfg, "07:17", 900, error="auth")
        self.assertTrue(any("access_token" in x for x in self.titles(entries)))
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest tests.test_app -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'one-api-menubar'`.

- [ ] **Step 3: Write minimal implementation**

Create `mac-plugins/one-api-menubar/menubar/one-api-menubar.py`:

```python
import os
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import oneapi_usage as U  # noqa: E402

import rumps  # noqa: E402

LAUNCH_LABEL = "com.oneapi.menubar"
PROGRAM_PATH = os.path.abspath(__file__)


def launch_plist_path():
    return os.path.expanduser("~/Library/LaunchAgents/{}.plist".format(LAUNCH_LABEL))


def is_login_enabled():
    return os.path.exists(launch_plist_path())


def set_login_item(enable):
    path = launch_plist_path()
    if enable:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        plist = (
            '<?xml version="1.0" encoding="UTF-8"?>\n'
            '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" '
            '"http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
            '<plist version="1.0"><dict>'
            '<key>Label</key><string>{}</string>'
            '<key>ProgramArguments</key><array><string>{}</string></array>'
            '<key>RunAtLoad</key><true/><key>KeepAlive</key><false/>'
            '</dict></plist>\n'
        ).format(LAUNCH_LABEL, PROGRAM_PATH)
        with open(path, "w", encoding="utf-8") as f:
            f.write(plist)
    elif os.path.exists(path):
        os.remove(path)


def menu_entries(daily, models, config, refresh_str, total, error=None):
    labels = U.LABELS[config.get("lang", "zh")]
    out = []
    if error == "auth":
        out.append({"title": labels["auth"], "disabled": True, "sep": False,
                    "action": None, "checked": None})
    elif error == "stale":
        out.append({"title": labels["stale"], "disabled": True, "sep": False,
                    "action": None, "checked": None})
    out.append({"title": "── {} ──".format(labels["daily"]), "disabled": True,
                "sep": False, "action": None, "checked": None})
    for row in U.daily_rows(daily, labels):
        out.append({"title": row, "disabled": True, "sep": False, "action": None, "checked": None})
    out.append({"title": "", "disabled": True, "sep": True, "action": None, "checked": None})
    out.append({"title": "── {} ──".format(labels["model"]), "disabled": True,
                "sep": False, "action": None, "checked": None})
    for row in U.model_rows(models, labels):
        out.append({"title": row, "disabled": True, "sep": False, "action": None, "checked": None})
    out.append({"title": "", "disabled": True, "sep": True, "action": None, "checked": None})
    out.append({"title": U.refresh_line(refresh_str, total, labels), "disabled": True,
                "sep": False, "action": None, "checked": None})
    if not (config.get("base_url") and config.get("access_token")):
        out.append({"title": labels["settings"], "disabled": True, "sep": False,
                    "action": None, "checked": None})
    out.append({"title": "", "disabled": True, "sep": True, "action": None, "checked": None})

    lang_next = "en" if labels is U.LABELS["zh"] else "zh"
    bar_next = "total" if config.get("bar_mode") == "today" else "today"
    bar_next_label = labels["bar_total"] if bar_next == "total" else labels["bar_today"]
    out.append({"title": "{}: {}".format(labels["lang"], lang_next), "disabled": False,
                "sep": False, "action": "toggle_lang", "checked": None})
    out.append({"title": "{}: {}".format(labels["bar_mode"], bar_next_label),
                "disabled": False, "sep": False, "action": "toggle_bar_mode", "checked": None})
    for sec in labels["interval_choices"]:
        out.append({"title": "{} {}s".format(labels["interval"], sec), "disabled": False,
                    "sep": False, "action": "interval_{}".format(sec), "checked": config.get("refresh_sec") == sec})
    out.append({"title": labels["refresh_now"], "disabled": False, "sep": False,
                "action": "refresh_now", "checked": None})
    out.append({"title": labels["console"], "disabled": False, "sep": False,
                "action": "open_console", "checked": None})
    out.append({"title": labels["login"], "disabled": False, "sep": False,
                "action": "set_login", "checked": is_login_enabled()})
    out.append({"title": labels["about"], "disabled": False, "sep": False,
                "action": "about", "checked": None})
    out.append({"title": labels["quit"], "disabled": False, "sep": False,
                "action": "quit", "checked": None})
    return out


class OneApiMenuBarApp(rumps.App):
    def __init__(self):
        super().__init__("⚡ …", quit_button=None)
        self.config = U.load_config()
        self._lock = threading.Lock()
        self._pending = None          # dict(stats=..., error=None|'auth'|'stale')
        self._last_fetch_ts = 0.0
        self._last_refresh_str = "-"
        self._stats = None

    # --- data ---
    def _kick_fetch(self, force=False):
        now = time.time()
        interval = self.config.get("refresh_sec", 60)
        if not force and (now - self._last_fetch_ts) < interval:
            return
        self._last_fetch_ts = now
        threading.Thread(target=self._fetch, daemon=True).start()

    def _fetch(self):
        cached, fresh = U.load_cache(ttl=55)
        if cached is not None and fresh and self._stats is not None:
            return
        if not (self.config.get("base_url") and self.config.get("access_token")):
            if cached is not None:
                self._store(cached, error="stale")
            return
        try:
            stats = U.fetch_dashboard(self.config["base_url"], self.config["access_token"])
            U.save_cache(stats)
            self._store(stats, error=None)
        except U.UsageError:
            self._store(cached if cached is not None else [], error="auth")
        except U.NetworkError:
            self._store(cached if cached is not None else [], error="stale")

    def _store(self, stats, error):
        with self._lock:
            self._pending = {"stats": stats, "error": error}

    # --- rumps timers ---
    @rumps.timer(5)
    def _poll(self, _):
        self._kick_fetch()

    @rumps.timer(1)
    def _apply(self, _):
        with self._lock:
            pending, self._pending = self._pending, None
        if pending is None:
            return
        self._render(pending["stats"], pending["error"])

    def _render(self, stats, error):
        self._stats = stats
        self._last_refresh_str = time.strftime("%H:%M")
        today = U.last_n_days()[-1]
        days = U.last_n_days()
        daily = U.aggregate_by_day(stats, days)
        models = U.aggregate_by_model(stats)
        total = U.total_tokens(stats)
        self.title = U.build_bar_title(daily, self.config.get("bar_mode", "today"), today)
        entries = menu_entries(daily, models, self.config, self._last_refresh_str, total, error)
        items = []
        for e in entries:
            if e["sep"]:
                items.append(rumps.separator)
            else:
                item = rumps.Item(e["title"], key=e.get("action") or None)
                item.set_enabled_(not e["disabled"]) if hasattr(item, "set_enabled_") else None
                if e.get("checked") is not None:
                    item.set_state_(1 if e["checked"] else 0) if hasattr(item, "set_state_") else None
                items.append(item)
        self.menu = items

    # --- actions ---
    @rumps.clicked("refresh_now")
    def _click(self, sender):
        self._dispatch(getattr(sender, "key", None))

    def _dispatch(self, action):
        if not action:
            return
        if action == "refresh_now":
            try:
                os.remove(U.CACHE_PATH)
            except OSError:
                pass
            self._kick_fetch(force=True)
        elif action == "toggle_lang":
            self.config["lang"] = "en" if self.config.get("lang") == "zh" else "zh"
            U.save_config(self.config); self._redraw()
        elif action == "toggle_bar_mode":
            self.config["bar_mode"] = "total" if self.config.get("bar_mode") == "today" else "today"
            U.save_config(self.config); self._redraw()
        elif action.startswith("interval_"):
            self.config["refresh_sec"] = int(action.split("_", 1)[1])
            U.save_config(self.config); self._redraw()
        elif action == "set_login":
            set_login_item(not is_login_enabled()); self._redraw()
        elif action == "open_console":
            if self.config.get("base_url"):
                import webbrowser; webbrowser.open(self.config["base_url"])
        elif action == "quit":
            import sys as _s; _s.exit(0)

    def _redraw(self):
        stats = self._stats or []
        self._render(stats, None)


if __name__ == "__main__":
    OneApiMenuBarApp().run()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m unittest tests.test_app -v`
Expected: PASS (all MenuEntriesTest). Note `set_enabled_`/`set_state_` are guarded by `hasattr` so the stub `rumps.Item` (`object`) does not break `menu_entries` tests (which never call `_render`).

- [ ] **Step 5: Commit**

```bash
git add menubar/one-api-menubar.py tests/test_app.py
git commit -m "feat(menubar): rumps app with background fetch, timers, menu and login item"
```

---

## Task 9: Packaging, config example, README

**Files:**
- Create: `mac-plugins/one-api-menubar/menubar/setup.py`
- Create: `mac-plugins/one-api-menubar/menubar/make-dmg.sh`
- Create: `mac-plugins/one-api-menubar/one-api-menubar.example.json`
- Create: `mac-plugins/one-api-menubar/README.md`

**Interfaces:** Consumes: `menubar/one-api-menubar.py`, `oneapi_usage.py`.

- [ ] **Step 1: Create config example**

Create `mac-plugins/one-api-menubar/one-api-menubar.example.json`:

```json
{
  "base_url": "https://your-one-api.example.com",
  "access_token": "your 32-char access_token from one-api personal settings",
  "refresh_sec": 60,
  "lang": "zh",
  "bar_mode": "today"
}
```

- [ ] **Step 2: Create py2app setup**

Create `mac-plugins/one-api-menubar/menubar/setup.py`:

```python
from setuptools import setup

APP = ["one-api-menubar.py"]
DATA_FILES = []
OPTIONS = {
    "argv_emulation": False,
    "iconfile": None,
    "plist": {
        "CFBundleName": "one-api-menubar",
        "CFBundleIdentifier": "com.oneapi.menubar",
        "CFBundleShortVersionString": "0.1.0",
        "LSUIElement": True,
    },
    "packages": ["rumps", "oneapi_usage"],
}

setup(
    app=APP,
    data_files=DATA_FILES,
    options={"py2app": OPTIONS},
    setup_requires=["py2app"],
)
```

- [ ] **Step 3: Create DMG script**

Create `mac-plugins/one-api-menubar/menubar/make-dmg.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
APP="one-api-menubar"
VERSION="0.1.0"
DIST="dist"
STAGE="$(mktemp -d)"
cp -R "${DIST}/${APP}.app" "${STAGE}/${APP}.app"
ln -s /Applications "${STAGE}/Applications"
rm -f "${APP}-${VERSION}.dmg"
hdiutil create -volname "${APP}" -srcfolder "${STAGE}" -ov -format UDZO "${APP}-${VERSION}.dmg"
rm -rf "${STAGE}"
echo "built ${APP}-${VERSION}.dmg"
```

- [ ] **Step 4: Create README**

Create `mac-plugins/one-api-menubar/README.md`:

```markdown
# one-api menu-bar app

macOS 菜单栏常驻应用，展示 one-api 账号最近 7 天每日 token 总量与每个模型用量。
形式与 ai-limit 一致：顶部栏文字 + Unicode 火花线，点开是 NSMenu 文本行。

## 数据源
`GET {base_url}/api/user/dashboard`，Header `Authorization: Bearer <access_token>`。
access_token 来自 one-api「个人设置」的 32 位令牌。仅显示该账号自己的用量，无需改动后端。

## 安装配置（macOS）
1. 复制 `one-api-menubar.example.json` 到 `~/.one-api-menubar.json`，填入 `base_url` 与 `access_token`。
2. 安装依赖（仅 macOS）：`pip3 install rumps py2app`（建议 Homebrew Python）。

## 运行（开发）
`python3 menubar/one-api-menubar.py`

## 打包 .app + DMG（仅 macOS）
    cd menubar
    python3 setup.py py2app
    bash make-dmg.sh
产出 `one-api-menubar-0.1.0.dmg`（未签名，Gatekeeper 需在「系统设置 > 隐私与安全性」放行）。

## 测试（Linux 即可）
    cd mac-plugins/one-api-menubar
    python3 -m unittest discover -s tests -v
```

- [ ] **Step 5: Verify logic package compiles + tests pass**

Run:
```bash
cd mac-plugins/one-api-menubar
python3 -m py_compile oneapi_usage.py menubar/one-api-menubar.py
python3 -m unittest discover -s tests -v
```
Expected: `py_compile` prints nothing (success); unittest reports all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add menubar/setup.py menubar/make-dmg.sh one-api-menubar.example.json README.md
git commit -m "feat(menubar): py2app packaging, dmg script, config example and README"
```

---

## Self-Review

**Spec coverage:**
- §2/§3 data source + auth → Task 5 (`fetch_dashboard` Bearer, PascalCase keys). ✓
- §3 zero-fill 7 days → Task 2 (`last_n_days` + `aggregate_by_day` fill). ✓
- §4 architecture (pure module + thin app + cache + thread/timer + login item) → Tasks 1-8. ✓
- §5 top-bar title (today+sparkline) → Task 6 `build_bar_title`; spark chars in Task 1/4. ✓
- §5 menu rows (daily + model tokens + 次数, share) → Task 6 rows + Task 8 `menu_entries`. ✓
- §5 language + bar_mode + interval toggles → Task 8 `menu_entries` actions + `_dispatch`. ✓
- §6 config/cache/login files → Tasks 1, 7, 8. ✓
- §7 error handling (auth/stale) → Task 5 exceptions + Task 8 `_fetch`/`menu_entries` error rows. ✓
- §8 directory → Tasks 1-9 file paths. ✓
- §9 testing (unittest + stub rumps/AppKit) → Task 8 `test_app.py` + Task 9 discover. ✓
- Packaging py2app + dmg + README → Task 9. ✓

**Placeholder scan:** No TBD/TODO; every code step has full code. No dangling references.

**Type consistency:** `DayStat(day,tokens,requests)` and `ModelStat(name,tokens,requests,share)` used identically in Tasks 2/3/6/8. `daily_rows(daily,labels)`/`model_rows(models,labels)`/`refresh_line(refresh_str,total,labels)`/`build_bar_title(daily,bar_mode,today)` signatures consistent across Task 6 definition and Task 8 usage. `menu_entries(...)` signature matches Task 8 test call order. `load_config(path,environ)` / `save_config(config,path)` consistent.
