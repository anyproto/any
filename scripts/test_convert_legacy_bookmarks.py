"""Offline contract tests for the explicit Bookmark converter."""

import copy
import importlib.util
import json
import os
from pathlib import Path
import stat
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("converter", Path(__file__).with_name("convert-legacy-bookmarks.py"))
converter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(converter)


class FakeAPI:
    def __init__(self):
        self.rows = {
            "root": {"id": "root", "_applySeq": 1, "any": {"type": "__collection__", "name": "Bookmarks"},
                     "collection": {"xkey": "bookmark", "hidden": True, "meta": {"custom": "keep"}}},
            "a": {"id": "a", "_applySeq": 2, "any": {"type": "page", "collections": ["root", "other"], "name": "Original"},
                  "root": {"url": "https://example.com", "note": "Keep — сохранить", "readerText": "Clipped text", "tags": ["tag"]}},
            "b": {"id": "b", "_applySeq": 3, "any": {"type": "page", "collections": ["root"]}},
        }
        self.parts = []
        self.writes = []
        self.capabilities = ["conditional-writes"]
        self.failure = None

    def request(self, method, path, body=None):
        if path == "/v1/health":
            return {"capabilities": self.capabilities}
        if "/bundles/" in path:
            return {"bundle": {"id": converter.BUNDLE, "rootId": "root", "roots": ["root"]}, "synced": True}
        if path.endswith("/objects/query"):
            condition = body["filter"]
            after = ""
            if "$and" in condition:
                condition, cursor = condition["$and"]
                after = cursor["id"]["$gt"]
            rows = [r for key, r in sorted(self.rows.items()) if key > after]
            if "id" in condition:
                rows = [r for r in rows if r["id"] == condition["id"]]
            else:
                rows = [r for r in rows if condition["any.collections"] in r["any"].get("collections", [])]
            return {"records": copy.deepcopy(rows[:body["limit"]])}
        if path.endswith("/parts"):
            if method == "GET":
                return {"parts": copy.deepcopy(self.parts)}
            self.writes.append((path, copy.deepcopy(body)))
            self.parts.append({"key": "body", "datasets": [{"module": "editor", "collection": "editor_blocks"}]})
            return {"partId": "body-part"}
        if path.endswith("/modify"):
            self.writes.append((path, copy.deepcopy(body)))
            row = self.rows[body["objectId"]]
            failure = self.failure
            if failure and failure[0] == row["id"]:
                self.failure = None
                if failure[1] == "conflict":
                    row["any"]["type"] = "custom"
                    row["_applySeq"] += 100
                elif failure[1] == "rejection":
                    return {"rejections": [{"reason": "refused"}]}
            if body["ifUnchangedSince"] != row["_applySeq"]:
                raise converter.ConversionError("HTTP 409; stale read")
            for op in body["records"][0]["ops"]:
                if op["type"] == "$pull":
                    row["any"]["collections"].remove(op["value"])
                else:
                    fields = {op["path"]: op["value"]} if op.get("path") else op["value"]
                    for path, value in fields.items():
                        group, key = path.split(".")
                        row.setdefault(group, {})[key] = copy.deepcopy(value)
            row["_applySeq"] += 10
            if failure and failure == (row["id"], "lost response"):
                raise converter.ConversionError("response lost")
            return {"changeId": "change", "recordIds": [row["id"]]}
        raise AssertionError((method, path, body))


class ConversionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.api = FakeAPI()
        self.backup = str(Path(self.temp.name) / "backup.json")

    def apply(self, backup=None):
        return converter.convert(self.api, "space", True, backup or self.backup)

    def test_dry_run_has_no_writes_or_backup(self):
        result = converter.convert(self.api, "space")
        self.assertEqual(result["pending"], 2)
        self.assertTrue(result["rootPromotion"])
        self.assertEqual(self.api.writes, [])
        self.assertFalse(os.path.exists(self.backup))

    def test_private_backup_and_same_identity_values(self):
        original = copy.deepcopy(self.api.rows)
        result = self.apply()
        self.assertEqual(result["converted"], 2)
        self.assertTrue(result["bodyAdded"])
        with open(self.backup, encoding="utf-8") as f:
            backup = json.load(f)
        self.assertEqual(backup["root"], original["root"])
        self.assertEqual(backup["members"], [original["a"], original["b"]])
        self.assertEqual(stat.S_IMODE(os.stat(self.backup).st_mode), 0o600)
        self.assertEqual(self.api.rows["a"]["root"], original["a"]["root"])
        self.assertEqual(self.api.rows["a"]["any"]["collections"], ["other"])
        self.assertEqual(self.api.rows["a"]["any"]["type"], "root")
        self.assertEqual(self.api.rows["root"]["type"]["meta"], {"custom": "keep"})
        self.assertTrue(all("ifUnchangedSince" in body for path, body in self.api.writes if path.endswith("/modify")))

    def test_existing_backup_prevents_all_writes(self):
        Path(self.backup).write_text("do not overwrite")
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(Path(self.backup).read_text(), "do not overwrite")
        self.assertEqual(self.api.writes, [])

    def test_custom_type_preflight_refuses_before_root_write(self):
        self.api.rows["b"]["any"]["type"] = "custom"
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.writes, [])
        self.assertFalse(os.path.exists(self.backup))

    def test_old_server_fails_closed(self):
        self.api.capabilities = []
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.writes, [])

    def test_concurrent_member_edit_is_preserved_and_not_retried(self):
        self.api.failure = ("a", "conflict")
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.rows["a"]["any"]["type"], "custom")
        self.assertIn("root", self.api.rows["a"]["any"]["collections"])
        self.assertEqual(self.api.rows["b"]["any"]["type"], "page")
        self.assertEqual(sum(body.get("objectId") == "a" for _, body in self.api.writes), 1)

    def test_concurrent_root_edit_prevents_promotion(self):
        self.api.failure = ("root", "conflict")
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.rows["root"]["any"]["type"], "custom")
        self.assertEqual(len(self.api.writes), 1)

    def test_rejection_stops_before_next_member(self):
        self.api.failure = ("a", "rejection")
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.rows["b"]["any"]["type"], "page")
        self.assertEqual(sum(body.get("objectId") == "a" for _, body in self.api.writes), 1)

    def test_lost_response_resumes_without_replaying_converted_member(self):
        self.api.failure = ("a", "lost response")
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.rows["a"]["any"]["type"], "root")
        writes_before = len(self.api.writes)
        resumed = self.apply(str(Path(self.temp.name) / "resume.json"))
        self.assertEqual(resumed["converted"], 1)
        new_writes = self.api.writes[writes_before:]
        self.assertEqual(len(new_writes), 1)
        self.assertEqual(new_writes[0][1]["objectId"], "b")
        writes_before = len(self.api.writes)
        self.assertEqual(self.apply(str(Path(self.temp.name) / "again.json"))["converted"], 0)
        self.assertEqual(len(self.api.writes), writes_before)

    def test_lost_root_response_resumes_with_body_and_members(self):
        self.api.failure = ("root", "lost response")
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.rows["root"]["any"]["type"], "__type__")
        self.assertEqual(self.api.parts, [])
        resumed = self.apply(str(Path(self.temp.name) / "resume.json"))
        self.assertEqual(resumed["converted"], 2)
        self.assertTrue(resumed["bodyAdded"])
        self.assertEqual(sum(body.get("objectId") == "root" for _, body in self.api.writes), 1)

    def test_existing_body_is_not_replaced(self):
        self.api.rows["root"]["any"]["type"] = "__type__"
        self.api.rows["root"]["type"] = {"xkey": "bookmark"}
        self.api.parts = [{"key": "body", "datasets": [{"module": "records", "collection": "root_custom"}]}]
        before = copy.deepcopy(self.api.parts)
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.parts, before)
        self.assertEqual(self.api.writes, [])

    def test_pagination_backs_up_and_converts_every_member(self):
        for i in range(105):
            key = f"member-{i:03}"
            self.api.rows[key] = {"id": key, "_applySeq": i + 20,
                                  "any": {"type": "page", "collections": ["root"]}}
        self.assertEqual(self.apply()["converted"], 107)
        with open(self.backup) as f:
            self.assertEqual(len(json.load(f)["members"]), 107)

    def test_bad_revision_refuses_before_backup(self):
        self.api.rows["b"]["_applySeq"] = True
        with self.assertRaises(converter.ConversionError):
            self.apply()
        self.assertEqual(self.api.writes, [])

    def test_loopback_and_no_credentials(self):
        for addr in ("https://example.com", "http://127.0.0.1@evil.test", "http://127.0.0.1/path", "http://user:secret@127.0.0.1", "file:///tmp/local", "http://127.0.0.1:bad"):
            with self.subTest(addr=addr), self.assertRaises(converter.ConversionError):
                converter.API(addr)
        converter.API("http://127.0.0.1:12345")
        converter.API("http://[::1]:12345")


if __name__ == "__main__":
    unittest.main()
