#!/usr/bin/env python3
"""OCI contract, idempotency, corruption and anonymous/auth boundary tests, without network."""
import copy
import json
import pathlib
import sys
import unittest
import urllib.parse
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import oci


class FakeRegistry:
    def __init__(self):
        self.blobs, self.manifests, self.calls = {}, {}, []
        self.failure = None
        self.redirect = None

    def __call__(self, method, url, headers=None, data=None, limit=oci.MAX_BLOB):
        self.calls.append((method, url, headers or {}, data))
        parsed = urllib.parse.urlsplit(url)
        if parsed.path == "/token":
            return 200, {}, b'{"token":"synthetic-registry-session"}'
        if self.failure:
            return self.failure, {}, b""
        if "/blobs/uploads/" in parsed.path:
            if method == "POST":
                return 202, {"Location": "https://ghcr.io" + parsed.path + "synthetic?_state=synthetic"}, b""
            expected = dict(urllib.parse.parse_qsl(parsed.query))["digest"]
            if oci.digest(data) != expected:
                raise AssertionError("blob upload digest mismatch")
            self.blobs[expected] = data
            return 201, {"Docker-Content-Digest": expected}, b""
        if "/blobs/" in parsed.path:
            ref = parsed.path.split("/blobs/", 1)[1]
            if self.redirect and method == "GET":
                return 307, {"Location": self.redirect}, b""
            if ref not in self.blobs:
                return 404, {}, b""
            body = self.blobs[ref] if method == "GET" else b""
            return 200, {}, body
        if "/manifests/" in parsed.path:
            ref = parsed.path.split("/manifests/", 1)[1]
            if method == "PUT":
                self.manifests[ref] = data
                self.manifests[oci.digest(data)] = data
                return 201, {"Docker-Content-Digest": oci.digest(data)}, b""
            if ref not in self.manifests:
                return 404, {}, b""
            body = self.manifests[ref]
            return 200, {"Docker-Content-Digest": oci.digest(body)}, body
        raise AssertionError("unexpected fake request")

    def client(self, credential="synthetic", username="synthetic"):
        return oci.Registry(oci.repository("demo"), credential, username, transport=self)

    def preload(self, archive=b"synthetic archive", platform="any"):
        obj = oci.manifest("demo", "1.2.3", oci.digest(archive), len(archive), platform)
        data = oci.manifest_bytes(obj)
        self.blobs.update({oci.digest(archive): archive, oci.digest(oci.EMPTY_CONFIG): oci.EMPTY_CONFIG})
        self.manifests.update({oci.version_tag("1.2.3", platform): data, oci.digest(data): data})
        return obj, oci.digest(data)


class OCITests(unittest.TestCase):
    def setUp(self):
        self.server = FakeRegistry()
        self.client = self.server.client()

    def test_schema2_empty_config_single_layer_contract(self):
        obj = oci.manifest("demo", "1.2.3", oci.digest(b"archive"), 7)
        self.assertEqual(obj["schemaVersion"], 2)
        self.assertEqual(obj["artifactType"], "application/vnd.persona.extension.v1")
        self.assertEqual(obj["config"], {"mediaType": oci.CONFIG_TYPE, "digest": oci.digest(b"{}"), "size": 2})
        self.assertEqual(len(obj["layers"]), 1)
        self.assertEqual(obj["layers"][0]["mediaType"], "application/vnd.persona.extension.package.v1+gzip")
        self.assertEqual(obj["layers"][0]["annotations"]["org.opencontainers.image.title"], "demo-1.2.3.tar.gz")
        self.assertEqual(obj["annotations"]["org.opencontainers.image.source"], "https://github.com/nwverse/persona-extensions")

    def test_publish_and_rerun_do_not_write_existing_tag(self):
        first = self.client.push_once("demo", "1.2.3", b"archive")
        writes = sum(c[0] in ("PUT", "POST") for c in self.server.calls)
        second = self.client.push_once("demo", "1.2.3", b"archive")
        self.assertEqual(first, second)
        self.assertEqual(writes, sum(c[0] in ("PUT", "POST") for c in self.server.calls))
        self.assertNotEqual(first, oci.digest(b"archive"))

    def test_existing_tag_archive_conflict_hard_fails_without_writes(self):
        self.server.preload(b"original")
        with self.assertRaisesRegex(ValueError, "conflicts"):
            self.client.push_once("demo", "1.2.3", b"replacement")
        self.assertFalse(any(c[0] in ("PUT", "POST") for c in self.server.calls))

    def test_same_tag_source_annotation_conflict_fails(self):
        obj, _ = self.server.preload(b"original")
        obj["annotations"]["org.opencontainers.image.source"] = "https://example.com/other"
        self.server.manifests["1.2.3"] = oci.manifest_bytes(obj)
        with self.assertRaisesRegex(ValueError, "conflicts"):
            self.client.push_once("demo", "1.2.3", b"original")

    def test_unknown_auth_or_server_failure_is_not_absence(self):
        for status in (401, 403, 429, 500, 503):
            self.server.failure = status
            with self.assertRaises(ValueError):
                self.client.push_once("demo", "1.2.3", b"archive")
        self.assertFalse(any(c[0] in ("PUT", "POST") for c in self.server.calls))

    def test_transport_failure_is_not_absence(self):
        self.client.token = "synthetic"
        self.client.transport = mock.Mock(side_effect=ValueError("transport failed"))
        with self.assertRaisesRegex(ValueError, "transport"):
            self.client.push_once("demo", "1.2.3", b"archive")

    def test_anonymous_public_gate_checks_manifest_and_blob_digests(self):
        obj, ref = self.server.preload(b"archive")
        package = {"url": oci.package_url("demo", ref), "sha256": oci.digest(b"archive")[7:], "size": 7}
        anon = self.server.client(None, None)
        self.assertEqual(oci.verify_package(anon, "demo", "1.2.3", package), ref)
        token_request = self.server.calls[0]
        self.assertNotIn("Authorization", token_request[2])
        self.assertEqual(dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(token_request[1]).query))["scope"],
                         "repository:nwverse/persona-extensions/demo:pull")
        self.server.blobs[obj["layers"][0]["digest"]] = b"corrupt"
        with self.assertRaisesRegex(ValueError, "checksum"):
            oci.verify_package(anon, "demo", "1.2.3", package)

    def test_private_package_fails_anonymous_gate(self):
        self.server.failure = 403
        anon = self.server.client(None, None)
        with self.assertRaisesRegex(ValueError, "403"):
            anon.read_manifest("sha256:" + "1" * 64)

    def test_corrupted_manifest_digest_fails(self):
        _, ref = self.server.preload()
        self.server.manifests[ref] += b" "
        with self.assertRaisesRegex(ValueError, "manifest digest"):
            self.client.read_manifest(ref)

    def test_redirect_to_blob_storage_strips_credentials(self):
        obj, _ = self.server.preload()
        target = "https://pkg-containers.githubusercontent.com/synthetic"
        self.server.redirect = target
        layer = obj["layers"][0]
        calls = []
        def transport(method, url, headers=None, data=None, limit=oci.MAX_BLOB):
            if url == target:
                calls.append(headers)
                return 200, {}, b"synthetic archive"
            return self.server(method, url, headers, data, limit)
        self.client.transport = transport
        self.client.read_blob(layer["digest"], layer["size"])
        self.assertEqual(calls, [{}])

    def test_redirect_to_untrusted_storage_or_upload_fails(self):
        self.server.redirect = "https://example.com/collect"
        with self.assertRaisesRegex(ValueError, "untrusted"):
            self.client.read_blob(oci.digest(b"archive"), 7)
        self.client.token = "synthetic"
        self.client.transport = mock.Mock(side_effect=[(404, {}, b""), (202, {"Location": "https://example.com/upload"}, b"")])
        with self.assertRaisesRegex(ValueError, "untrusted"):
            self.client.upload_blob(b"archive")
        prefix = "/v2/" + self.client.path + "/blobs/"
        for location in ("/v2/other/repository/blobs/upload/7.session", prefix + "upload/", prefix + "upload/a/../../x",
                         prefix + "upload/session#fragment", "http://ghcr.io" + prefix + "upload/session",
                         "https://ghcr.io.example.com" + prefix + "upload/session", prefix + "uploadsession"):
            self.client.transport = mock.Mock(side_effect=[(404, {}, b""), (202, {"Location": location}, b"")])
            with self.subTest(location=location), self.assertRaisesRegex(ValueError, "untrusted"):
                self.client.upload_blob(b"archive")

    def test_ghcr_singular_upload_session_location(self):
        # GHCR answers POST /blobs/uploads/ with a relative /blobs/upload/<session> location (observed live).
        calls = []
        self.client.token = "synthetic"

        def transport(method, url, headers, data=None, limit=None):
            calls.append((method, url))
            if method == "HEAD":
                return 404, {}, b""
            if method == "POST":
                return 202, {"Location": "/v2/" + self.client.path + "/blobs/upload/7.0f1e2d3c-synthetic"}, b""
            return 201, {"Docker-Content-Digest": oci.digest(b"archive")}, b""
        self.client.transport = transport
        self.client.upload_blob(b"archive")
        put = [url for method, url in calls if method == "PUT"]
        self.assertEqual(len(put), 1)
        self.assertTrue(put[0].startswith("https://ghcr.io/v2/" + self.client.path + "/blobs/upload/7.0f1e2d3c-synthetic?"))
        self.assertIn("digest=sha256%3A", put[0])

    def test_registry_path_filename_and_digest_safety(self):
        for value in ("../demo", "demo/other", "Demo", "demo?x", "demo@x"):
            with self.assertRaises(ValueError):
                oci.repository(value)
        for url in ("oci:ghcr.io/other/demo@sha256:" + "1" * 64, "oci:ghcr.io/" + oci.repository("demo") + ":latest"):
            with self.assertRaises(ValueError):
                oci.parse_url(url, "demo")
        with self.assertRaises(ValueError):
            oci.filename("demo", "../../version")

    def test_explicit_platform_mapping_and_safe_titles(self):
        obj, ref = self.server.preload(b"archive", "darwin-arm64")
        package = {"url": oci.package_url("demo", ref), "sha256": oci.digest(b"archive")[7:], "size": 7, "platforms": ["darwin-arm64"]}
        self.assertEqual(oci.verify_package(self.server.client(None, None), "demo", "1.2.3", package), ref)
        self.assertEqual(obj["layers"][0]["annotations"]["org.opencontainers.image.title"], "demo-1.2.3__darwin-arm64.tar.gz")


if __name__ == "__main__":
    unittest.main()
