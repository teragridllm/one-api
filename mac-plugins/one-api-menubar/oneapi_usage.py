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
