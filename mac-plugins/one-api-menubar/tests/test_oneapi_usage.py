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
