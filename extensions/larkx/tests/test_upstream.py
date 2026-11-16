"""Getting LarkAgentX and its dependencies on first start, with no network: a synthetic archive stands in for GitHub's, and
the files the package ships (upstream.json, requirements.txt) are checked for what the program relies on."""
import hashlib
import io
import json
import os
import re
import sys
import tarfile
import tempfile
import unittest

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
sys.path.insert(0, os.path.join(ROOT, "src"))

from larkx_persona import real  # noqa: E402

COMMIT = "0123456789abcdef0123456789abcdef01234567"
FILES = {"larkx/__init__.py": b"x = 1\n", "larkx/proto/ids.py": b"y = 2\n"}


def archive(files, top="LarkAgentX-" + COMMIT):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w:gz") as t:
        for name, body in files.items():
            info = tarfile.TarInfo(top + "/" + name)
            info.size = len(body)
            t.addfile(info, io.BytesIO(body))
    return out.getvalue()


def lock(files=FILES):
    return {"commit": COMMIT, "archive": "https://example.com/" + COMMIT,
            "files": {k: hashlib.sha256(v).hexdigest() for k, v in files.items()}}


class Download:
    """urlopen for one archive, counting the calls."""

    def __init__(self, data):
        self.data = data
        self.calls = []

    def __call__(self, url, timeout):
        self.calls.append(url)
        if isinstance(self.data, Exception):
            raise self.data
        return io.BytesIO(self.data)


class FetchTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.dir = tmp.name

    def test_only_the_locked_files_are_kept_and_only_once(self):
        get = Download(archive(dict(FILES, **{"static/big.bin": b"z" * 100, "larkx/extra.py": b"import os\n", "README.md": b"r"})))
        os.makedirs(os.path.join(self.dir, "larkagentx-000000000000", "larkx"))
        os.makedirs(os.path.join(self.dir, "larkx"))
        target = real.fetch_upstream(lock(), self.dir, get)
        self.assertEqual(target, os.path.join(self.dir, "larkagentx-" + COMMIT[:12]))
        found = sorted(os.path.relpath(os.path.join(d, f), target) for d, _, fs in os.walk(target) for f in fs)
        self.assertEqual(found, ["larkx/__init__.py", "larkx/proto/ids.py"])
        with open(os.path.join(target, "larkx/proto/ids.py"), "rb") as f:
            self.assertEqual(f.read(), b"y = 2\n")
        # An earlier commit's copy goes; LarkAgentX's own data directory (larkx/) stays.
        self.assertEqual(sorted(os.listdir(self.dir)), ["larkagentx-" + COMMIT[:12], "larkx"])
        self.assertEqual(real.fetch_upstream(lock(), self.dir, get), target)
        self.assertEqual(len(get.calls), 1)

    def test_a_changed_file_fails_and_leaves_nothing(self):
        get = Download(archive(dict(FILES, **{"larkx/proto/ids.py": b"y = 3\n"})))
        with self.assertRaisesRegex(RuntimeError, "larkx/proto/ids.py"):
            real.fetch_upstream(lock(), self.dir, get)
        self.assertEqual(os.listdir(self.dir), [])

    def test_a_missing_file_fails(self):
        get = Download(archive({"larkx/__init__.py": FILES["larkx/__init__.py"]}))
        with self.assertRaisesRegex(RuntimeError, "少了文件"):
            real.fetch_upstream(lock(), self.dir, get)
        self.assertEqual(os.listdir(self.dir), [])

    def test_a_broken_or_unreachable_archive_fails_in_words(self):
        with self.assertRaisesRegex(RuntimeError, "不是完整的压缩包"):
            real.fetch_upstream(lock(), self.dir, Download(archive(FILES)[:40]))
        with self.assertRaisesRegex(RuntimeError, "下载不了"):
            real.fetch_upstream(lock(), self.dir, Download(OSError("no route")))

    def test_a_name_outside_the_lock_is_never_written(self):
        get = Download(archive(dict(FILES, **{"../escape.py": b"x"}), top="LarkAgentX"))
        target = real.fetch_upstream(lock(), self.dir, get)
        self.assertFalse(os.path.exists(os.path.join(self.dir, "escape.py")))
        self.assertTrue(os.path.isfile(os.path.join(target, "larkx", "__init__.py")))

    def test_old_dependency_directories_go(self):
        for name in ("deps-py312-aaaaaaaaaaaa", "deps-py313-bbbbbbbbbbbb.new", "deps-py313-cccccccccccc"):
            os.makedirs(os.path.join(self.dir, name))
        open(os.path.join(self.dir, "larkx.db"), "w").close()
        keep = os.path.join(self.dir, "deps-py313-cccccccccccc")
        real.drop_others(self.dir, "deps-", keep)
        self.assertEqual(sorted(os.listdir(self.dir)), ["deps-py313-cccccccccccc", "larkx.db"])


class ShippedTest(unittest.TestCase):
    def test_the_lock_pins_a_commit_and_every_file(self):
        with open(real.UPSTREAM, encoding="utf-8") as f:
            up = json.load(f)
        self.assertRegex(up["commit"], r"^[0-9a-f]{40}$")
        self.assertTrue(up["archive"].startswith("https://codeload.github.com/"))
        self.assertTrue(up["archive"].endswith("/" + up["commit"]))
        self.assertIn("larkx/__init__.py", up["files"])
        for name, digest in up["files"].items():
            self.assertTrue(name.startswith("larkx/") and ".." not in name.split("/"), name)
            self.assertRegex(digest, r"^[0-9a-f]{64}$")

    def test_every_dependency_is_pinned_with_hashes(self):
        with open(os.path.join(ROOT, "requirements.txt"), encoding="utf-8") as f:
            text = f.read()
        joined = re.sub(r"\\\n", " ", re.sub(r"(?m)^#.*\n", "", text))
        entries = [re.sub(r"\s+", " ", e).strip() for e in joined.splitlines() if e.strip()]
        self.assertGreater(len(entries), 10)
        names = set()
        for entry in entries:
            self.assertRegex(entry, r"^[A-Za-z0-9_.-]+==[0-9][0-9A-Za-z.]*( ;[^-]+)? (--hash=sha256:[0-9a-f]{64} ?)+$")
            names.add(entry.split("==")[0].lower())
        # requests' dependencies are pinned too, at releases with the fixes for its CVEs.
        self.assertTrue({"requests", "urllib3", "idna", "certifi", "charset-normalizer", "six", "typing-extensions"} <= names)
        for old in ("requests==2.31", "requests==2.32.0", "requests==2.32.3"):
            self.assertNotIn(old, text)


if __name__ == "__main__":
    unittest.main()
