import os
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import oneapi_usage as U  # noqa: E402

import rumps  # noqa: E402

LAUNCH_LABEL = "com.oneapi.menubar"
PROGRAM_PATH = os.path.abspath(__file__)
# 在 py2app 打包后的 .app 里，__file__ 指向 Resources 里的脚本，不能直接作为
# 可执行文件交给 launchd；此时改用 app 主二进制。
LAUNCH_PROGRAM = sys.executable if getattr(sys, "frozen", False) else PROGRAM_PATH


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
        ).format(LAUNCH_LABEL, LAUNCH_PROGRAM)
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
        days = U.last_n_days()
        today = days[-1]
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
                action = e.get("action")
                item = rumps.MenuItem(e["title"],
                                      callback=(None if e["disabled"] else self._make_handler(action)))
                ns = getattr(item, "_menuitem", None)
                if ns is not None:
                    if e["disabled"]:
                        ns.setEnabled_(False)
                    if e.get("checked") is not None:
                        ns.setState_(1 if e["checked"] else 0)
                items.append(item)
        # rumps 的 Menu.update 以标题为 key 且忽略已存在的条目，直接赋值会让
        # 标题变化的行（如刷新时间、每日用量）不断堆积，先清空再重建。
        self.menu.clear()
        self.menu = items

    def _make_handler(self, action):
        return lambda sender: self._dispatch(action)

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
                import webbrowser
                webbrowser.open(self.config["base_url"])
        elif action == "about":
            rumps.notification("one-api menubar", "v0.1.0", "one-api 用量菜单栏")
        elif action == "quit":
            rumps.quit_application()

    def _redraw(self):
        stats = self._stats or []
        self._render(stats, None)


if __name__ == "__main__":
    OneApiMenuBarApp().run()
