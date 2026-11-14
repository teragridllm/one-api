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
