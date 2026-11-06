#!/usr/bin/env python3
"""Tests for scripts/manifest.py: what scripts/ci.sh release picks and the check() invariants.
Run: python3 -I scripts/test-manifest.py."""
import importlib.util
import io
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr

spec = importlib.util.spec_from_file_location('manifest', pathlib.Path(__file__).with_name('manifest.py'))
manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manifest)


class SelectedTests(unittest.TestCase):
    def setUp(self):
        self.id = manifest.ids()[0]

    def test_an_id_as_release_sh_takes_it(self):
        for arg in (self.id, self.id + '/', 'extensions/' + self.id, 'extensions/' + self.id + '/'):
            self.assertEqual(manifest.selected([arg]), [self.id], arg)

    def test_a_commit_is_a_base(self):
        self.assertEqual(manifest.selected(['HEAD']), manifest.changed('HEAD'))

    def test_neither_an_id_nor_a_commit_is_refused(self):
        # A misspelled id, or a word a glob expanded to, must not pass for a base where every extension is new.
        for arg in ('no-such-extension', 'README.md', '-h', ''):
            with self.assertRaises(SystemExit, msg=arg):
                manifest.selected([self.id, arg])

    def test_changed_refuses_an_unknown_ref(self):
        # Not "every extension changed": git can't tell what was there at a ref it doesn't have.
        r = subprocess.run([sys.executable, '-I', manifest.__file__, 'changed', 'no-such-ref'], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertEqual(r.stdout, '')

    def test_a_duplicate_key_is_rejected(self):
        # Two "version" lines once shipped silently (json last-wins); load must refuse instead.
        d = pathlib.Path(tempfile.mkdtemp())
        ext = d / 'extensions' / 'dup'
        ext.mkdir(parents=True)
        (ext / manifest.NEW).write_text('{"id": "dup", "version": "0.1.0", "version": "0.1.1"}\n')
        old_root = manifest.ROOT
        manifest.ROOT = str(d)
        try:
            with self.assertRaises(ValueError):
                manifest.load('dup')
        finally:
            manifest.ROOT = old_root


class JunkTests(unittest.TestCase):
    """macOS/editor junk (AppleDouble ._*, .DS_Store, __pycache__) under skills/assistant must fail the check:
    scripts/package.sh copies those trees verbatim, so the junk would ship (issue #99)."""

    def make_ext(self, files):
        d = pathlib.Path(tempfile.mkdtemp())
        ext = d / 'extensions' / 'junk'
        ext.mkdir(parents=True)
        (ext / manifest.NEW).write_text('{"id": "junk", "version": "0.1.0"}\n')
        for rel in files:
            p = ext / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text('')
        self.addCleanup(shutil.rmtree, d)
        return d

    def with_root(self, d):
        self._old_root = manifest.ROOT
        manifest.ROOT = str(d)
        self.addCleanup(self._restore_root)

    def _restore_root(self):
        manifest.ROOT = self._old_root

    def test_junk_under_skills_and_assistant_is_rejected(self):
        planted = [
            'skills/._SKILL.md',
            'skills/.DS_Store',
            'skills/review/__pycache__/mod.pyc',
            'skills/review/._SKILL.md',
            'assistant/.DS_Store',
            'assistant/._answer-style.md',
            # A real skill next to the junk must still be there.
            'skills/review/SKILL.md',
            'assistant/answer-style.md',
        ]
        d = self.make_ext(planted)
        self.with_root(d)
        bad = manifest.check_junk('junk')
        flagged = {b.split(': ', 1)[1].split(' is packaging junk')[0] for b in bad}
        self.assertEqual(flagged, {
            'skills/._SKILL.md',
            'skills/.DS_Store',
            'skills/review/__pycache__',
            'skills/review/._SKILL.md',
            'assistant/.DS_Store',
            'assistant/._answer-style.md',
        })
        with redirect_stderr(io.StringIO()):
            self.assertFalse(manifest.check())

    def test_a_clean_tree_passes(self):
        d = self.make_ext(['skills/review/SKILL.md', 'assistant/answer-style.md'])
        self.with_root(d)
        self.assertEqual(manifest.check_junk('junk'), [])
        self.assertTrue(manifest.check())

    def test_junk_outside_packaged_dirs_is_not_scanned(self):
        # package.sh copies only skills/ and assistant/, so junk elsewhere cannot land in the tarball.
        d = self.make_ext(['.DS_Store', '._foo', 'src/.DS_Store'])
        self.with_root(d)
        self.assertEqual(manifest.check_junk('junk'), [])
class GitFailureTests(unittest.TestCase):
    """Issue #100: git failing for a reason other than a name git does not know must surface and fail, not look
    like nothing changed (empty release) or every extension changed (a release of everything)."""

    def setUp(self):
        self.run = manifest.subprocess.run
        self.id = manifest.ids()[0]

    def tearDown(self):
        manifest.subprocess.run = self.run

    def stub(self, rules):
        # rules maps git's subcommand (argv[3] of "git -C ROOT <sub> ...") to its CompletedProcess.
        def run(argv, **kwargs):
            return rules[argv[3]]
        manifest.subprocess.run = run

    def cp(self, code, stdout='', stderr=''):
        return subprocess.CompletedProcess([], code, stdout=stdout, stderr=stderr)

    def test_version_at_hits_show_without_touching_ls_tree(self):
        # The happy path adds no extra git call: a missing 'ls-tree' rule raises if the code asks anyway.
        self.stub({'show': self.cp(0, stdout='{"version": "0.1.0"}\n')})
        self.assertEqual(manifest.version_at('HEAD', self.id), '0.1.0')

    def test_a_missing_git_binary_is_a_one_line_failure(self):
        def boom(argv, **kwargs):
            raise OSError('git not found')
        manifest.subprocess.run = boom
        with self.assertRaises(SystemExit) as cm:
            manifest.is_commit('HEAD')
        self.assertIn('could not run git', str(cm.exception))

    def test_version_at_none_when_the_path_is_absent_at_the_ref(self):
        # git show's "path does not exist" with an empty ls-tree is the one failure that means "not there yet".
        self.stub({'show': self.cp(128, stderr="fatal: path 'x' does not exist in 'HEAD'\n"),
                   'ls-tree': self.cp(0, stdout='')})
        self.assertIsNone(manifest.version_at('HEAD', self.id))

    def test_version_at_fails_when_git_reading_the_tree_fails(self):
        # ls-tree itself failing is git broken, not an absent path: None here would mark the extension new.
        self.stub({'show': self.cp(128, stderr='fatal: unable to read object\n'),
                   'ls-tree': self.cp(128, stderr='fatal: object store on fire\n')})
        with self.assertRaises(SystemExit) as cm:
            manifest.version_at('HEAD', self.id)
        self.assertIn('object store on fire', str(cm.exception))

    def test_version_at_fails_when_show_fails_for_a_path_that_is_present(self):
        # ls-tree lists the path, so show's failure is operational and must not be read as absence.
        self.stub({'show': self.cp(128, stderr='fatal: unable to read blob abc\n'),
                   'ls-tree': self.cp(0, stdout='100644 blob abc\textensions/x/f.json\n')})
        with self.assertRaises(SystemExit) as cm:
            manifest.version_at('HEAD', self.id)
        self.assertIn('though that path is in the tree', str(cm.exception))
        self.assertIn('unable to read blob abc', str(cm.exception))

    def test_is_commit_false_for_a_name_git_does_not_know(self):
        self.stub({'rev-parse': self.cp(1)})  # --quiet: an unknown revision fails with no stderr
        self.assertFalse(manifest.is_commit('no-such-ref'))

    def test_is_commit_surfaces_an_operational_failure(self):
        self.stub({'rev-parse': self.cp(128, stderr='fatal: not a git repository\n')})
        with self.assertRaises(SystemExit) as cm:
            manifest.is_commit('HEAD')
        self.assertIn('not a git repository', str(cm.exception))

    def test_changed_fails_instead_of_marking_every_extension_new(self):
        # A valid ref but a broken git: changed() must die, not return every id ("everything changed").
        self.stub({'rev-parse': self.cp(0),
                   'show': self.cp(128, stderr='fatal: object store on fire\n'),
                   'ls-tree': self.cp(128, stderr='fatal: object store on fire\n')})
        with self.assertRaises(SystemExit) as cm:
            manifest.changed('HEAD')
        self.assertIn('object store on fire', str(cm.exception))

    def test_selected_propagates_a_changed_failure(self):
        self.stub({'rev-parse': self.cp(0),
                   'show': self.cp(128, stderr='fatal: object store on fire\n'),
                   'ls-tree': self.cp(128, stderr='fatal: object store on fire\n')})
        with self.assertRaises(SystemExit):
            manifest.selected(['HEAD'])

    def test_changed_legitimately_reports_nothing_without_failing(self):
        # Working-tree versions equal what is at HEAD: an empty list is a normal answer, not a failure.
        self.assertEqual(manifest.changed('HEAD'), [])

    def test_selected_takes_several_ids_and_keeps_order(self):
        ids = manifest.ids()[:2]
        self.assertEqual(manifest.selected(list(reversed(ids))), list(reversed(ids)))


class CliGitFailureTests(unittest.TestCase):
    """End to end through the command scripts/ci.sh captures: a broken git must exit non-zero with the reason on
    stderr and nothing on stdout, so set -e stops the release instead of printing "nothing to release"."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.fake_git = pathlib.Path(self.dir) / 'git'
        self.env = dict(os.environ, PATH=self.dir + os.pathsep + os.environ.get('PATH', ''))

    def install_git(self, cases):
        # cases is the body of a POSIX sh case statement after the fixed "-C ROOT" prefix has been stripped.
        self.fake_git.write_text('#!/bin/sh\nif [ "$1" = "-C" ]; then shift 2; fi\ncase "$1" in\n'
                                 + cases + '\nesac\n')
        self.fake_git.chmod(0o755)

    def run_manifest(self, *args):
        return subprocess.run([sys.executable, '-I', str(pathlib.Path(manifest.__file__)), *args],
                              capture_output=True, text=True, env=self.env)

    def test_changed_fails_when_git_is_broken(self):
        self.install_git('rev-parse) exit 0 ;;\n'
                         '*) echo "fatal: simulated object-store failure" >&2; exit 128 ;;')
        r = self.run_manifest('changed', 'HEAD')
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(r.stdout, '')
        self.assertIn('simulated object-store failure', r.stderr)

    def test_selected_fails_when_git_is_broken(self):
        self.install_git('rev-parse) exit 0 ;;\n'
                         '*) echo "fatal: simulated object-store failure" >&2; exit 128 ;;')
        r = self.run_manifest('selected', 'HEAD')
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(r.stdout, '')
        self.assertIn('simulated object-store failure', r.stderr)

    def test_changed_fails_when_show_dies_but_the_path_is_listed(self):
        self.install_git('rev-parse) exit 0 ;;\n'
                         'show) echo "fatal: simulated blob read failure" >&2; exit 128 ;;\n'
                         'ls-tree) printf "100644 blob x\\t%s\\n" "$4"; exit 0 ;;')
        r = self.run_manifest('changed', 'HEAD')
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(r.stdout, '')
        self.assertIn('simulated blob read failure', r.stderr)


class BaseTests(unittest.TestCase):
    """manifest.base(<last release run>, <before>) is where a push's release diffs from: a run dropped while it waited
    (GitHub keeps one waiting run per concurrency group) released nothing, so the next one diffs from what was
    released last, not from its own push."""

    def setUp(self):
        self.old_root = manifest.ROOT
        self.dir = tempfile.mkdtemp()
        manifest.ROOT = self.dir
        self.git('init', '-q', '-b', 'main')

    def tearDown(self):
        manifest.ROOT = self.old_root
        shutil.rmtree(self.dir, ignore_errors=True)

    def git(self, *args):
        return subprocess.run(['git', '-C', self.dir, '-c', 'user.name=test', '-c', 'user.email=test@example.com', *args],
                              capture_output=True, text=True, check=True).stdout.strip()

    def put(self, id, version):
        d = pathlib.Path(self.dir, 'extensions', id)
        d.mkdir(parents=True, exist_ok=True)
        (d / manifest.NEW).write_text('{"id": "%s", "version": "%s"}\n' % (id, version))

    def commit(self, message='change'):
        self.git('add', '-A')
        self.git('commit', '-q', '--allow-empty', '-m', message)
        return self.git('rev-parse', 'HEAD')

    def test_last_release_run_first_then_before(self):
        self.put('alpha', '1.0.0')
        released = self.commit()
        self.put('alpha', '1.0.1')  # a push whose run was dropped while it waited
        dropped = self.commit()
        self.put('beta', '0.1.0')
        self.commit()
        self.assertEqual(manifest.base(released, dropped), released)
        self.assertEqual(manifest.changed(manifest.base(released, dropped)), ['alpha', 'beta'])
        # No release run finished yet, or the commit it names is not behind HEAD: the push's own before.
        self.assertEqual(manifest.base('', dropped), dropped)
        self.assertEqual(manifest.base('f' * 40, dropped), dropped)
        # Nothing usable (the all-zero before of a new branch): "", so CI runs the checks only.
        self.assertEqual(manifest.base('', '0' * 40), '')

    def test_last_released_is_the_newest_index_commit(self):
        self.put('alpha', '1.0.0')
        first = self.commit()
        self.assertEqual(manifest.last_released(), '')
        self.commit('market: index after %s' % first[:7])
        self.put('alpha', '1.0.1')
        self.commit()
        self.assertEqual(manifest.last_released(), first)
        self.assertEqual(manifest.changed(manifest.base(manifest.last_released(), 'HEAD~1')), ['alpha'])

    def test_a_commit_not_behind_head_is_not_a_base(self):
        self.put('alpha', '1.0.0')
        first = self.commit()
        self.git('checkout', '-q', '-b', 'side')
        self.put('alpha', '2.0.0')
        side = self.commit()
        self.git('checkout', '-q', 'main')
        self.assertEqual(manifest.base(side, first), first)


if __name__ == '__main__':
    unittest.main()
