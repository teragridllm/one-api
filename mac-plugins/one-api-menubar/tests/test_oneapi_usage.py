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
