"""Manual-only CI policy (#190), without dispatching jobs or running rebuilds."""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github/workflows/ci.yml"


def events(text):
    starts = list(re.finditer(r"^on:\s*$", text, re.MULTILINE))
    if len(starts) != 1:
        raise ValueError("one explicit on block is required")
    tail = text[starts[0].end():]
    end = re.search(r"^\S", tail, re.MULTILINE)
    block = tail[:end.start()] if end else tail
    return re.findall(r"^  ([a-z_]+):", block, re.MULTILINE)


def job(text, name):
    start = re.search(r"^  " + re.escape(name) + r":\s*$", text, re.MULTILINE)
    if not start:
        raise ValueError("missing job " + name)
    tail = text[start.end():]
    end = re.search(r"^  [a-z-]+:\s*$", tail, re.MULTILINE)
    return tail[:end.start()] if end else tail


class ManualCIPolicyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = WORKFLOW.read_text()

    def test_only_explicit_dispatch_can_run(self):
        self.assertEqual(events(self.text), ["workflow_dispatch"])

    def test_automatic_event_negative_controls(self):
        for event in ("pull_request", "push", "schedule", "release", "merge_group"):
            with self.subTest(event=event):
                changed = self.text.replace("on:\n", "on:\n  " + event + ":\n", 1)
                self.assertNotEqual(events(changed), ["workflow_dispatch"])

    def test_inline_or_duplicate_event_blocks_are_not_guessed(self):
        for text in ("on: [push, workflow_dispatch]\n", "on:\n  push:\non:\n  workflow_dispatch:\n"):
            with self.assertRaises(ValueError):
                events(text)

    def test_checks_only_is_the_default(self):
        self.assertRegex(self.text, r"phase:\s+type: choice\s+options: \[check, packages, catalog\][\s\S]*?default: check")
        self.assertRegex(self.text, r"confirm_cutover:[\s\S]*?default: false")
        self.assertIn("'Checks-only dispatch: no publication or signing.'", job(self.text, "identity"))
        self.assertIn("'publish=false'", job(self.text, "identity"))

    def test_all_source_rebuild_jobs_and_limits_remain(self):
        self.assertIn("scripts/test-source-rebuild.py", job(self.text, "source-plan"))
        self.assertIn("scripts/source-rebuild.py plan", job(self.text, "source-plan"))
        rebuild = job(self.text, "source-rebuild")
        self.assertIn("needs: [source-plan]", rebuild)
        self.assertIn("max-parallel: 4", rebuild)
        self.assertIn("timeout-minutes: 15", rebuild)
        self.assertIn("scripts/source-rebuild.py rebuild", rebuild)
        self.assertIn("node-version: '24.11.1'", rebuild)

    def test_manual_check_aggregate_does_not_accept_missing_rebuilds(self):
        aggregate = job(self.text, "ci-required")
        self.assertIn("always() && github.event_name == 'workflow_dispatch' && inputs.phase == 'check'", aggregate)
        self.assertIn("needs: [check, source-plan, source-rebuild]", aggregate)
        self.assertIn('"$SOURCE_REBUILD_RESULT" != success', aggregate)
        self.assertIn('"$SOURCE_REBUILD_RESULT" != skipped', aggregate)
        self.assertNotIn("pull_request", aggregate)

    def test_local_pipeline_checks_policy_lint_and_guarded_cleanup(self):
        pipeline = (ROOT / "scripts/ci.sh").read_text()
        self.assertIn("python3 -I scripts/test-ci-policy.py", pipeline)
        self.assertIn("go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml", pipeline)
        packager = (ROOT / "scripts/package.sh").read_text()
        self.assertIn('rm -rf "${out:?}"', packager)
        self.assertNotIn('rm -rf "$out"', packager)

    def test_publication_permissions_and_confirmation_stay_separate(self):
        self.assertIn("permissions:\n  contents: read\n", self.text)
        identity = job(self.text, "identity")
        self.assertIn('if [ "$CONFIRM" != true ]', identity)
        self.assertIn("scripts/publish-oci.py gate --confirm-cutover", identity)
        packages = job(self.text, "packages")
        self.assertIn("needs.identity.outputs.publish == 'true'", packages)
        self.assertIn("packages: write", packages)
        self.assertIn("group: persona-extension-oci-publication", packages)
        self.assertIn("cancel-in-progress: false", packages)


if __name__ == "__main__":
    unittest.main()
