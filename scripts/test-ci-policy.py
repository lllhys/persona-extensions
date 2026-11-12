"""Publication policy for trusted-main pushes and explicit manual phases."""
import os
import re
import subprocess
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


class PublicationPolicyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = WORKFLOW.read_text()

    def test_only_manual_dispatch_and_trusted_main_push_can_run(self):
        self.assertEqual(events(self.text), ["push", "workflow_dispatch"])
        self.assertRegex(self.text, r"on:\n  push:\n    branches: \[main\]\n  workflow_dispatch:")

    def test_other_automatic_events_are_not_allowed(self):
        for event in ("pull_request", "schedule", "release", "merge_group"):
            with self.subTest(event=event):
                changed = self.text.replace("on:\n", "on:\n  " + event + ":\n", 1)
                self.assertNotEqual(events(changed), ["push", "workflow_dispatch"])

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
        self.assertIn("uses: actions/upload-artifact@v4", packages)
        anonymous = job(self.text, "anonymous")
        self.assertIn("needs: [identity, check, packages]", anonymous)
        self.assertIn("inputs.phase == 'catalog'", anonymous)
        self.assertIn("github.event_name == 'push'", anonymous)
        self.assertIn("github.run_id", anonymous)
        self.assertIn("github.event_name == 'push' && github.run_id || inputs.package_run_id", anonymous)
        self.assertIn("run-id: ${{ steps.package-run.outputs.run_id }}", anonymous)
        self.assertIn("secrets.EXTENSION_MARKET_SIGNING_KEY", anonymous)

    def test_packages_only_runs_when_the_current_run_publishes_packages(self):
        packages = job(self.text, "packages")
        self.assertIn("github.event_name == 'push' || inputs.phase == 'packages'", packages)
        self.assertNotIn("inputs.phase == 'catalog'", packages)

    def test_catalog_does_not_require_packages_to_run_again(self):
        anonymous = job(self.text, "anonymous")
        self.assertIn("needs: [identity, check, packages]", anonymous)
        self.assertIn("!cancelled()", anonymous)
        self.assertIn("needs.check.result == 'success'", anonymous)
        self.assertIn("github.event_name == 'push' && needs.packages.result == 'success'", anonymous)
        self.assertIn("github.event_name == 'workflow_dispatch' && inputs.phase == 'catalog' && needs.packages.result == 'skipped'", anonymous)
        self.assertIn("github.event_name == 'push' && github.run_id || inputs.package_run_id", anonymous)
        self.assertIn("run-id: ${{ steps.package-run.outputs.run_id }}", anonymous)
        self.assertIn("secrets.EXTENSION_MARKET_SIGNING_KEY", anonymous)
        self.assertIn("if: steps.payload.outputs.path != ''", anonymous)
        self.assertIn("Sign raw payload with the approved existing key", anonymous)
        self.assertIn("name: signed-oci-catalog", anonymous)
        self.assertIn("cmp -s persona-extensions.json", anonymous)
        self.assertIn("steps.payload.outputs.path != ''", anonymous)
        self.assertIn("outputs:\n      payload: ${{ steps.payload.outputs.path }}", anonymous)
        self.assertIn("path=%s\\n", anonymous)
        self.assertNotIn("artifact-ids:", anonymous)
        self.assertIn("CATALOG_UNCHANGED: ${{ needs.anonymous.outputs.payload == '' }}", job(self.text, "publication-required"))
        self.assertIn("sign_result: ${{ steps.sign.outcome }}", anonymous)
        self.assertIn("SIGN_RESULT: ${{ needs.anonymous.outputs.sign_result }}", job(self.text, "publication-required"))
        self.assertNotIn("needs.anonymous.outputs.catalog_unchanged", self.text)
        with self.assertRaises(ValueError):
            job(self.text, "sign")

    def test_publication_aggregate_rejects_skipped_stages(self):
        aggregate = job(self.text, "publication-required")
        self.assertIn("needs: [identity, packages, anonymous]", aggregate)
        self.assertIn("!cancelled()", aggregate)
        self.assertIn("github.event_name == 'push'", aggregate)
        self.assertIn("github.event_name == 'workflow_dispatch' && inputs.phase == 'catalog'", aggregate)
        self.assertNotIn("inputs.phase == 'packages'", aggregate)
        for result in ("PACKAGES_RESULT", "ANONYMOUS_RESULT", "SIGN_RESULT"):
            self.assertIn(f'"${result}" != success', aggregate)
        self.assertIn("SIGN_RESULT: ${{ needs.anonymous.outputs.sign_result }}", aggregate)
        self.assertIn("exit 1", aggregate)
        for missing_job in ("anonymous",):
            with self.subTest(missing_job=missing_job):
                changed = self.text.replace(job(self.text, missing_job), "", 1)
                with self.assertRaises(ValueError):
                    job(changed, missing_job)
        for condition in ("!cancelled()", "needs.packages.result == 'skipped'", "needs.packages.result == 'success'"):
            with self.subTest(condition=condition):
                changed = self.text.replace(condition, "false", 1)
                self.assertNotIn(condition, job(changed, "anonymous"))

    def test_publication_completion_command_rejects_skipped_stages(self):
        command = job(self.text, "publication-required").split("run: |\n", 1)[1]
        cases = [
            ("push", "success", "success", "success", 0),
            ("workflow_dispatch", "skipped", "success", "success", 0),
            ("push", "skipped", "skipped", "skipped", 1),
            ("workflow_dispatch", "skipped", "skipped", "skipped", 1),
            ("workflow_dispatch", "skipped", "success", "skipped", 1),
            ("push", "success", "success", "failure", 1),
            ("push", "success", "failure", "success", 1),
        ]
        for event, packages, anonymous, sign, expected in cases:
            with self.subTest(event=event, packages=packages, anonymous=anonymous, sign=sign):
                result = subprocess.run(
                    ["bash", "-c", command],
                    check=False,
                    capture_output=True,
                    text=True,
                    env={
                        **os.environ,
                        "GITHUB_EVENT_NAME": event,
                        "PACKAGES_RESULT": packages,
                        "ANONYMOUS_RESULT": anonymous,
                        "SIGN_RESULT": sign,
                        "CATALOG_UNCHANGED": "false",
                    },
                )
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
    def test_unchanged_catalog_is_an_explicit_successful_noop(self):
        command = job(self.text, "publication-required").split("run: |\n", 1)[1]
        for event in ("push", "workflow_dispatch"):
            result = subprocess.run(["bash", "-c", command], check=False, capture_output=True, text=True,
                                    env={**os.environ, "GITHUB_EVENT_NAME": event,
                                         "PACKAGES_RESULT": "success" if event == "push" else "skipped",
                                         "ANONYMOUS_RESULT": "success", "SIGN_RESULT": "skipped", "CATALOG_UNCHANGED": "true"})
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("no catalog change", result.stdout)
        for anonymous, sign in (("success", "success"), ("failure", "skipped")):
            with self.subTest(anonymous=anonymous, sign=sign):
                result = subprocess.run(["bash", "-c", command], check=False, capture_output=True, text=True,
                                        env={**os.environ, "GITHUB_EVENT_NAME": "push", "PACKAGES_RESULT": "success",
                                             "ANONYMOUS_RESULT": anonymous, "SIGN_RESULT": sign, "CATALOG_UNCHANGED": "true"})
                self.assertEqual(result.returncode, 1, result.stdout + result.stderr)

    def test_publication_gate_receives_the_workflow_signing_outcome(self):
        anonymous = job(self.text, "anonymous")
        aggregate = job(self.text, "publication-required")
        outputs = anonymous.split("    outputs:\n", 1)[1].split("    steps:\n", 1)[0]
        expressions = r"^\s+([\w-]+): \$\{\{ (.+?) \}\}$"

        for event in ("push", "workflow_dispatch"):
            for payload, sign, expected in (("", "skipped", 0), ("payload.json", "success", 0),
                                            ("payload.json", "skipped", 1)):
                with self.subTest(event=event, payload=payload, sign=sign):
                    context = {
                        "steps": {"payload": {"outputs": {"path": payload}}, "sign": {"outcome": sign}},
                        "needs": {"packages": {"result": "success" if event == "push" else "skipped"},
                                  "anonymous": {"result": "success", "outputs": {}}},
                    }

                    def value(expression):
                        if expression.endswith(" == ''"):
                            return str(value(expression[:-6]) == "").lower()
                        result = context
                        for part in expression.split("."):
                            result = result[part]
                        return result

                    for name, expression in re.findall(expressions, outputs, re.MULTILINE):
                        context["needs"]["anonymous"]["outputs"][name] = value(expression)
                    env = {name: value(expression) for name, expression in
                           re.findall(expressions, aggregate.split("        run: |\n", 1)[0], re.MULTILINE)}
                    result = subprocess.run(
                        ["bash", "-c", aggregate.split("run: |\n", 1)[1]],
                        check=False, capture_output=True, text=True,
                        env={**os.environ, "GITHUB_EVENT_NAME": event, **env},
                    )
                    self.assertEqual(result.returncode, expected, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
