#!/usr/bin/env python3
"""Explicit, resumable conversion of the unreleased Bookmark collection.

Dry run by default. Uses the existing loopback HTTP API only. Stop on any
conflict or uncertain write result; rerun with a NEW backup after inspection.
No account is created, no server is started, and catalog setup is never called.
"""

import argparse
import ipaddress
import http.client
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

BUNDLE = "system:bookmark/v1"
BODY = {"key": "body", "ui": {"type": "document"}, "datasets": [{"module": "editor"}]}


class ConversionError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ConversionError("HTTP redirect refused")


class API:
    def __init__(self, addr):
        try:
            url = urllib.parse.urlsplit(addr)
            valid = (url.scheme in ("http", "https") and url.hostname
                     and ipaddress.ip_address(url.hostname).is_loopback
                     and url.path in ("", "/") and not url.query and not url.fragment
                     and url.username is None and url.password is None)
            url.port  # Validate the port without making a request.
        except ValueError:
            valid = False
        if not valid:
            raise ConversionError("--addr must be an HTTP(S) loopback IP origin without credentials")
        self.addr = addr.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method, path, body=None):
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode()
        req = urllib.request.Request(self.addr + path, data=data, method=method,
                                     headers={"Content-Type": "application/json"})
        try:
            with self.opener.open(req, timeout=30) as response:
                result = json.load(response)
        except urllib.error.HTTPError as exc:
            raise ConversionError(f"HTTP {exc.code}; stopped without retrying the request") from None
        except (urllib.error.URLError, http.client.HTTPException, OSError, ValueError):
            raise ConversionError("Request failed or response was invalid; write outcome may be unknown. Rerun after inspection") from None
        if not isinstance(result, dict):
            raise ConversionError("Unexpected API response")
        return result


def revision(row):
    seq = row.get("_applySeq")
    if type(seq) is not int or seq <= 0:
        raise ConversionError("Object snapshot has no positive revision; upgrade the server")
    if not isinstance(row.get("id"), str) or not row["id"]:
        raise ConversionError("Object snapshot has no ID")
    return seq


def query(api, prefix, condition):
    # Omit projection: the full row includes its revision and all user values.
    rows, after = [], ""
    while True:
        where = condition if not after else {"$and": [condition, {"id": {"$gt": after}}]}
        result = api.request("POST", prefix + "/objects/query",
                             {"filter": where, "sort": ["id"], "limit": 100})
        batch = result.get("records")
        if not isinstance(batch, list):
            raise ConversionError("Missing query records")
        for row in batch:
            if not isinstance(row, dict):
                raise ConversionError("Invalid query record")
            revision(row)
            if row["id"] <= after:
                raise ConversionError("Query pagination did not advance")
            after = row["id"]
            rows.append(row)
        if len(batch) < 100:
            return rows


def backup_file(path, payload):
    if os.name != "posix":
        raise ConversionError("Apply requires macOS or Linux for a private, durable backup")
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as out:
            os.fchmod(out.fileno(), 0o600)
            json.dump(payload, out, ensure_ascii=False, indent=2)
            out.write("\n")
            out.flush()
            os.fsync(out.fileno())
        directory = os.open(os.path.dirname(os.path.abspath(path)), os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    except OSError:
        raise ConversionError("Cannot create exclusive private backup; no server writes attempted") from None


def modify(api, prefix, row, ops):
    result = api.request("POST", prefix + "/modify", {
        "objectId": row["id"], "dataset": "objects",
        "ifUnchangedSince": revision(row), "records": [{"id": row["id"], "ops": ops}],
    })
    if result.get("rejections"):
        raise ConversionError("Write reported rejected operations; stopped. Inspect the backup before rerunning")
    if not result.get("changeId") or row["id"] not in result.get("recordIds", []):
        raise ConversionError("Write acknowledgement incomplete; stopped without retry")


# The existing add-part endpoint is not conditional. The operator must pause
# schema editing for this one-time operation (see the conversion guide).
def ensure_body(api, prefix, root_id):
    path = prefix + "/types/" + urllib.parse.quote(root_id, safe="") + "/parts"
    parts = api.request("GET", path).get("parts")
    if not isinstance(parts, list):
        raise ConversionError("Missing type parts")
    for part in parts:
        for dataset in part.get("datasets", []):
            if dataset.get("collection") == "editor_blocks":
                if dataset.get("module") != "editor" or dataset.get("invalid"):
                    raise ConversionError("Existing editor declaration is incompatible; left unchanged")
                return False
    if any(part.get("key") == "body" for part in parts):
        raise ConversionError("Existing body part is incompatible; left unchanged")
    if not api.request("POST", path, BODY).get("partId"):
        raise ConversionError("Part creation acknowledgement incomplete; stopped without retry")
    return True


def convert(api, space_id, apply=False, backup=None):
    if apply and not backup:
        raise ConversionError("--apply requires --backup naming a new private file")
    if "conditional-writes" not in api.request("GET", "/v1/health").get("capabilities", []):
        raise ConversionError("Server does not advertise conditional writes; no conversion attempted")
    prefix = "/v1/spaces/" + urllib.parse.quote(space_id, safe="")
    bundle = api.request("GET", prefix + "/bundles/" + urllib.parse.quote(BUNDLE, safe="")).get("bundle", {})
    root_id = bundle.get("rootId")
    if not isinstance(root_id, str) or not root_id or len(bundle.get("roots", [])) > 1:
        raise ConversionError("Bookmark bundle has no single resolved root")
    roots = query(api, prefix, {"id": root_id})
    if len(roots) != 1:
        raise ConversionError("Bookmark definition is missing")
    root = roots[0]
    kind = root.get("any", {}).get("type")
    namespace = "collection" if kind == "__collection__" else "type"
    if kind not in ("__collection__", "__type__") or root.get(namespace, {}).get("xkey") != "bookmark":
        raise ConversionError("Bookmark definition has an unexpected kind or key; left unchanged")
    members = query(api, prefix, {"any.collections": root_id})
    if any(row.get("any", {}).get("type") not in ("page", root_id) for row in members):
        raise ConversionError("A legacy member has a custom type; no writes attempted")
    result = {"mode": "apply" if apply else "dry-run", "pending": len(members),
              "rootPromotion": kind == "__collection__", "converted": 0, "bodyAdded": False}
    if not apply:
        return result
    backup_file(backup, {"format": "legacy-bookmarks-backup-v1", "spaceId": space_id,
                         "bundle": bundle, "root": root, "members": members})
    if kind == "__collection__":
        fields = {"any.type": "__type__", "type.xkey": "bookmark"}
        for key in ("hidden", "meta"):
            if key in root.get("collection", {}):
                fields["type." + key] = root["collection"][key]
        if root.get("any", {}).get("name") == "Bookmarks":
            fields["any.name"] = "Bookmark"
        modify(api, prefix, root, [{"type": "$set", "value": fields}])
    result["bodyAdded"] = ensure_body(api, prefix, root_id)
    for row in members:
        modify(api, prefix, row, [{"type": "$set", "path": "any.type", "value": root_id},
                                  {"type": "$pull", "path": "any.collections", "value": root_id}])
        result["converted"] += 1
    if query(api, prefix, {"any.collections": root_id}):
        raise ConversionError("Legacy members remain or arrived during conversion; rerun with a new backup")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--addr", required=True, help="running server's loopback HTTP(S) origin")
    parser.add_argument("--space-id", required=True)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--backup", help="new backup file, required with --apply")
    args = parser.parse_args()
    try:
        result = convert(API(args.addr), args.space_id, args.apply, args.backup)
    except ConversionError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
