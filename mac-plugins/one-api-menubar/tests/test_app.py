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
_rumps.Item = object
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
