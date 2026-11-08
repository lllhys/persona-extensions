#!/usr/bin/env python3
"""Minimal stdlib OCI Distribution client for the official extension artifact contract.

No external publisher binary, mutable latest tag, Docker image, Release creation or visibility API.
"""
import base64
import hashlib
import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request

import catalog

ARTIFACT_TYPE = "application/vnd.persona.extension.v1"
LAYER_TYPE = "application/vnd.persona.extension.package.v1+gzip"
MANIFEST_TYPE = "application/vnd.oci.image.manifest.v1+json"
CONFIG_TYPE = "application/vnd.oci.empty.v1+json"
EMPTY_CONFIG = b"{}"
SOURCE = "https://github.com/nwverse/persona-extensions"
PREFIX = "nwverse/persona-extensions/"
MAX_BLOB = catalog.PACKAGE_MOST


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def repository(extension_id):
    if not isinstance(extension_id, str) or len(extension_id) > 32 or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", extension_id):
        raise ValueError("unsafe extension registry path")
    return PREFIX + extension_id


def filename(extension_id, version, platform="any"):
    repository(extension_id)
    if not catalog.valid_version(version):
        raise ValueError("unsafe version (SemVer without build metadata, at most 96 characters)")
    if not re.fullmatch(r"any|(?:linux|darwin|windows)-(?:amd64|arm64)", platform):
        raise ValueError("unsafe platform")
    # Underscores are outside the version domain, so a prerelease cannot mimic this suffix.
    suffix = "" if platform == "any" else "__" + platform
    return f"{extension_id}-{version}{suffix}.tar.gz"


def version_tag(version, platform="any"):
    filename("demo", version, platform)
    return version if platform == "any" else "p_" + platform + "_" + version


def manifest(extension_id, version, archive_digest, size, platform="any"):
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", archive_digest) or type(size) is not int or not 0 < size <= MAX_BLOB:
        raise ValueError("invalid archive descriptor")
    title = filename(extension_id, version, platform)
    return {"schemaVersion": 2, "mediaType": MANIFEST_TYPE, "artifactType": ARTIFACT_TYPE,
            "config": {"mediaType": CONFIG_TYPE, "digest": digest(EMPTY_CONFIG), "size": len(EMPTY_CONFIG)},
            "layers": [{"mediaType": LAYER_TYPE, "digest": archive_digest, "size": size,
                        "annotations": {"org.opencontainers.image.title": title}}],
            "annotations": {"org.opencontainers.image.source": SOURCE,
                            "org.opencontainers.image.title": title,
                            "org.opencontainers.image.version": version}}


def manifest_bytes(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode("utf-8")


class Missing(Exception):
    """Only a registry HTTP 404; auth, server and transport failures never mean absent."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def fetch(method, url, headers=None, data=None, limit=MAX_BLOB):
    request = urllib.request.Request(url, data=data, headers=headers or {}, method=method)
    opener = urllib.request.build_opener(NoRedirect)
    try:
        response = opener.open(request, timeout=60)
    except urllib.error.HTTPError as error:
        # Callers handle only specific status codes; do not log body/URL (which can contain credentials).
        status, response_headers = error.code, dict(error.headers)
        error.close()
        return status, response_headers, b""
    except (urllib.error.URLError, TimeoutError, OSError):
        raise ValueError("OCI transport failed") from None
    with response:
        declared = response.headers.get("Content-Length")
        if method == "HEAD":
            return response.status, dict(response.headers), b""
        if declared and int(declared) > limit:
            raise ValueError("OCI response exceeds size limit")
        body = response.read(limit + 1)
        if len(body) > limit:
            raise ValueError("OCI response exceeds size limit")
        return response.status, dict(response.headers), body


def header(headers, key):
    return next((v for k, v in headers.items() if k.lower() == key.lower()), None)


class Registry:
    def __init__(self, path, credential=None, username=None, transport=fetch):
        if not path.startswith(PREFIX) or repository(path[len(PREFIX):]) != path:
            raise ValueError("registry path is outside official namespace")
        self.path, self.credential, self.username, self.transport = path, credential, username, transport
        self.token = None
        if credential and not username:
            raise ValueError("registry username is required")

    def authorize(self):
        # Fixed HTTPS endpoint and exact scope, not a server-supplied challenge/realm.
        scope = "repository:" + self.path + (":pull,push" if self.credential else ":pull")
        url = "https://ghcr.io/token?" + urllib.parse.urlencode({"service": "ghcr.io", "scope": scope})
        headers = {}
        if self.credential:
            basic = base64.b64encode((self.username + ":" + self.credential).encode()).decode()
            headers["Authorization"] = "Basic " + basic
        status, _, body = self.transport("GET", url, headers, limit=1024 * 1024)
        if status != 200:
            raise ValueError("OCI token request failed (HTTP %s)" % status)
        self.token = catalog.loads(body).get("token")
        if not isinstance(self.token, str) or not self.token:
            raise ValueError("OCI token response is missing token")

    def request(self, method, endpoint, data=None, content_type=None, limit=MAX_BLOB):
        if self.token is None:
            self.authorize()
        url = "https://ghcr.io/v2/" + self.path + "/" + endpoint
        headers = {"Authorization": "Bearer " + self.token, "Accept": MANIFEST_TYPE}
        if content_type:
            headers["Content-Type"] = content_type
        status, response_headers, body = self.transport(method, url, headers, data, limit=limit)
        if status == 401:
            self.authorize()
            headers["Authorization"] = "Bearer " + self.token
            status, response_headers, body = self.transport(method, url, headers, data, limit=limit)
        if method == "GET" and endpoint.startswith("blobs/") and status in (302, 303, 307, 308):
            target = header(response_headers, "Location") or ""
            parsed = urllib.parse.urlsplit(target)
            host = parsed.hostname or ""
            if parsed.scheme != "https" or parsed.username or parsed.password or parsed.port not in (None, 443) or not (
                    host.endswith(".blob.core.windows.net") or host.endswith(".githubusercontent.com")):
                raise ValueError("untrusted OCI blob redirect")
            # Never forward registry credentials to blob storage, even for an authenticated read.
            status, response_headers, body = self.transport("GET", target, {}, limit=limit)
        if status == 404:
            raise Missing()
        if status not in (200, 201, 202, 204):
            raise ValueError("OCI %s failed (HTTP %s)" % (method, status))
        return response_headers, body

    def read_manifest(self, ref):
        if not re.fullmatch(r"sha256:[0-9a-f]{64}|[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}", ref):
            raise ValueError("unsafe manifest reference")
        headers, body = self.request("GET", "manifests/" + ref, limit=2 * 1024 * 1024)
        actual = digest(body)
        if ref.startswith("sha256:") and actual != ref:
            raise ValueError("manifest digest mismatch")
        declared = header(headers, "Docker-Content-Digest")
        if declared and declared != actual:
            raise ValueError("registry manifest header digest mismatch")
        return actual, body

    def read_blob(self, expected_digest, size):
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_digest) or not 0 < size <= MAX_BLOB:
            raise ValueError("invalid blob descriptor")
        _, body = self.request("GET", "blobs/" + expected_digest, limit=size)
        if len(body) != size or digest(body) != expected_digest:
            raise ValueError("archive blob checksum or size mismatch")
        return body

    def upload_blob(self, data):
        expected = digest(data)
        try:
            self.request("HEAD", "blobs/" + expected, limit=1)
            return
        except Missing:
            pass
        headers, _ = self.request("POST", "blobs/uploads/", b"", limit=1024 * 1024)
        location = urllib.parse.urljoin("https://ghcr.io/", header(headers, "Location") or "")
        parsed = urllib.parse.urlsplit(location)
        # The distribution spec leaves the session path to the registry: GHCR answers /blobs/upload/<session>
        # (singular), others /blobs/uploads/<session>. Either way it must stay on GHCR and in this repository.
        base = "/v2/" + self.path + "/blobs/"
        session = next((parsed.path[len(base + kind):] for kind in ("uploads/", "upload/")
                        if parsed.path.startswith(base + kind)), None)
        if (parsed.scheme != "https" or parsed.netloc != "ghcr.io" or parsed.fragment or
                not session or not re.fullmatch(r"[A-Za-z0-9._=-]+", session)):
            raise ValueError("untrusted OCI upload location")
        query = urllib.parse.parse_qsl(parsed.query)
        if any(k == "digest" for k, _ in query):
            raise ValueError("upload location already has digest")
        endpoint = parsed.path[len("/v2/" + self.path + "/"):] + "?" + urllib.parse.urlencode(query + [("digest", expected)])
        headers, _ = self.request("PUT", endpoint, data, "application/octet-stream", limit=1024 * 1024)
        declared = header(headers, "Docker-Content-Digest")
        if declared and declared != expected:
            raise ValueError("uploaded blob digest mismatch")

    def existing(self, obj, tag):
        try:
            actual, body = self.read_manifest(tag)
        except Missing:
            return None
        if catalog.loads(body) != obj:
            raise ValueError("existing version tag metadata conflicts; bump version, never overwrite")
        self.read_blob(obj["config"]["digest"], obj["config"]["size"])
        layer = obj["layers"][0]
        self.read_blob(layer["digest"], layer["size"])
        return actual

    def push_once(self, extension_id, version, archive, platform="any"):
        if not self.credential:
            raise ValueError("refusing OCI push without credentials")
        if self.path != repository(extension_id):
            raise ValueError("publisher registry path differs from extension id")
        obj = manifest(extension_id, version, digest(archive), len(archive), platform)
        tag = version_tag(version, platform)
        prior = self.existing(obj, tag)
        if prior:
            return prior
        self.upload_blob(EMPTY_CONFIG)
        self.upload_blob(archive)
        # Workflow publication is serialized globally. Recheck after uploads, never silently replace a tag.
        prior = self.existing(obj, tag)
        if prior:
            return prior
        data = manifest_bytes(obj)
        self.request("PUT", "manifests/" + tag, data, MANIFEST_TYPE, limit=1024 * 1024)
        actual, body = self.read_manifest(tag)
        if digest(data) != actual or body != data:
            raise ValueError("published manifest changed during upload")
        return actual


def package_url(extension_id, manifest_digest):
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", manifest_digest):
        raise ValueError("invalid OCI manifest digest")
    return "oci:ghcr.io/" + repository(extension_id) + "@" + manifest_digest


def parse_url(url, extension_id):
    prefix = "oci:ghcr.io/" + repository(extension_id) + "@"
    if not isinstance(url, str) or not url.startswith(prefix):
        raise ValueError("package registry path differs from extension id")
    ref = url[len(prefix):]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", ref):
        raise ValueError("package must reference an OCI manifest digest")
    return ref


def verify_package(registry, extension_id, version, package, archive_validator=None):
    ref = parse_url(package["url"], extension_id)
    actual, body = registry.read_manifest(ref)
    expected = manifest(extension_id, version, "sha256:" + package["sha256"], package["size"],
                        catalog.package_platform(package))
    if catalog.loads(body) != expected:
        raise ValueError("OCI manifest does not match catalog archive descriptor and metadata")
    registry.read_blob(expected["config"]["digest"], expected["config"]["size"])
    archive = registry.read_blob("sha256:" + package["sha256"], package["size"])
    if archive_validator:
        archive_validator(archive)
    return actual
