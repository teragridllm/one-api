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
        filled = 0
    else:
        filled = int(round(value / max_value * width))
        filled = max(1, min(width, filled))
    return "█" * filled + "░" * (width - filled)


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
