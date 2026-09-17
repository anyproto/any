"""Exercise the generated Python quickstart without opening network connections."""

import argparse
import ast
import io
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import MagicMock, patch
import urllib.error


CLIENT = Path(__file__).resolve().parents[2] / "website/dist/assets/examples/client.py"


def load_helpers(path):
    # Use the downloaded source, omitting the top-level demo that creates data.
    tree = ast.parse(path.read_text(), filename=str(path))
    tree.body = [
        node for node in tree.body
        if isinstance(node, (ast.Import, ast.ImportFrom, ast.FunctionDef, ast.ClassDef))
        or (isinstance(node, ast.Assign)
            and any(isinstance(target, ast.Name) and target.id == "API"
                    for target in node.targets))
    ]
    namespace = {}
    exec(compile(tree, str(path), "exec"), namespace)
    return namespace


class Response(io.BytesIO):
    def __init__(self, status, body):
        super().__init__(body)
        self.status = status


class PythonClientTests(unittest.TestCase):
    def setUp(self):
        self.client = load_helpers(CLIENT)
        self.client["API"] = "http://any.example:8123/local/v1"

    def test_generated_client_compiles(self):
        compile(CLIENT.read_text(), str(CLIENT), "exec")

    def test_call_uses_configured_address_and_sends_json(self):
        response = Response(200, b'{"id":"space"}')
        with patch("urllib.request.urlopen", return_value=response) as open_request:
            result = self.client["call"]("POST", "/spaces", {"name": "Notes"})
        request = open_request.call_args.args[0]
        self.assertEqual(request.full_url, self.client["API"] + "/spaces")
        self.assertEqual(request.method, "POST")
        self.assertEqual(json.loads(request.data), {"name": "Notes"})
        self.assertEqual(result, {"id": "space"})
        self.assertTrue(response.closed)

    def test_call_accepts_no_content(self):
        response = Response(204, b"")
        with patch("urllib.request.urlopen", return_value=response):
            self.assertIsNone(self.client["call"]("DELETE", "/resource"))
        self.assertTrue(response.closed)

    def test_call_preserves_api_error_details(self):
        body = io.BytesIO(b'{"error":{"code":"space.not_found","message":"Missing"}}')
        error = urllib.error.HTTPError(self.client["API"], 404, "Not Found", {}, body)
        with patch("urllib.request.urlopen", side_effect=error):
            with self.assertRaisesRegex(RuntimeError, "404 space.not_found: Missing"):
                self.client["call"]("GET", "/resource")
        self.assertTrue(body.closed)

    def test_call_preserves_status_for_non_envelope_errors(self):
        for payload in (b"<html>Bad Gateway</html>", b"", b"\xff", b"null",
                        b"[]", b"{}", b'{"error":null}', b'{"error":{}}'):
            with self.subTest(payload=payload):
                body = io.BytesIO(payload)
                error = urllib.error.HTTPError(self.client["API"], 502, "Bad Gateway", {}, body)
                with patch("urllib.request.urlopen", side_effect=error):
                    with self.assertRaisesRegex(RuntimeError, "HTTP 502"):
                        self.client["call"]("GET", "/resource")
                self.assertTrue(body.closed)

    def test_sse_uses_same_address_and_path_as_call(self):
        response = Response(200, b'event: ready\ndata: {}\n\n')
        connection = MagicMock()
        connection.getresponse.return_value = response
        with patch("http.client.HTTPConnection", return_value=connection) as connect:
            frames = list(self.client["sse"]("/spaces/query/subscribe?view=pages", {"limit": 20}))
        connect.assert_called_once_with("any.example", 8123)
        request = connection.request.call_args
        self.assertEqual(request.args, ("POST", "/local/v1/spaces/query/subscribe?view=pages"))
        self.assertEqual(json.loads(request.kwargs["body"]), {"limit": 20})
        self.assertEqual(request.kwargs["headers"]["accept"], "text/event-stream")
        self.assertEqual(frames, [("ready", {})])
        self.assertTrue(response.closed)
        connection.close.assert_called_once()

    def test_sse_preserves_http_errors_and_closes_resources(self):
        for payload, message in (
            (b'{"error":{"code":"server.unavailable","message":"Restarting"}}',
             "503.*server.unavailable"),
            (b"<html>Service Unavailable</html>", "HTTP 503"),
        ):
            with self.subTest(payload=payload):
                response = Response(503, payload)
                connection = MagicMock()
                connection.getresponse.return_value = response
                with patch("http.client.HTTPConnection", return_value=connection):
                    with self.assertRaisesRegex(RuntimeError, message):
                        list(self.client["sse"]("/subscribe", {}))
                self.assertTrue(response.closed)
                connection.close.assert_called_once()

    def test_sse_emits_final_frame_without_blank_line(self):
        for ending in (b"", b"\n", b"\n\n"):
            with self.subTest(ending=ending):
                response = Response(200, b'event: snapshot\ndata: {"records":[]}' + ending)
                connection = MagicMock()
                connection.getresponse.return_value = response
                with patch("http.client.HTTPConnection", return_value=connection):
                    frames = list(self.client["sse"]("/subscribe", {}))
                self.assertEqual(frames, [("snapshot", {"records": []})])
                self.assertTrue(response.closed)
                connection.close.assert_called_once()

    def test_sse_handles_comments_multiline_data_and_event_reset(self):
        response = Response(200, (
            b': keepalive\r\nevent: snapshot\r\ndata: {\r\ndata: "records":[]\r\n'
            b'data: }\r\n\r\ndata: {"default":true}\r\n\r\n: keepalive\r\n'
        ))
        connection = MagicMock()
        connection.getresponse.return_value = response
        with patch("http.client.HTTPConnection", return_value=connection):
            frames = list(self.client["sse"]("/subscribe", {}))
        self.assertEqual(frames, [("snapshot", {"records": []}), ("message", {"default": True})])

    def test_sse_closes_when_consumer_stops_early(self):
        response = Response(200, b'event: ready\ndata: {}\n\nevent: snapshot\ndata: {}\n\n')
        connection = MagicMock()
        connection.getresponse.return_value = response
        with patch("http.client.HTTPConnection", return_value=connection):
            stream = self.client["sse"]("/subscribe", {})
            self.assertEqual(next(stream), ("ready", {}))
            stream.close()
        self.assertTrue(response.closed)
        connection.close.assert_called_once()

    def test_sse_closes_connection_when_opening_fails(self):
        connection = MagicMock()
        connection.getresponse.side_effect = OSError("Connection reset")
        with patch("http.client.HTTPConnection", return_value=connection):
            with self.assertRaisesRegex(OSError, "Connection reset"):
                list(self.client["sse"]("/subscribe", {}))
        connection.close.assert_called_once()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("client", nargs="?", type=Path, default=CLIENT)
    options, remaining = parser.parse_known_args()
    CLIENT = options.client
    unittest.main(argv=[sys.argv[0], *remaining])
