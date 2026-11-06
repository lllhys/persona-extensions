#!/usr/bin/env python3
"""Tests for the repository layout check: python3 -I scripts/test-layout.py."""
import importlib.util
import pathlib
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('check_layout', pathlib.Path(__file__).with_name('check-layout.py'))
layout = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layout)

GOOD = {
    'README.md': '',
    'sdk/go/go.mod': 'module github.com/nwverse/persona/sdk/go\n',
    'sdk/go/ext/ext.go': 'package ext\n',
    'pluginkit/go.mod': 'module example.com/repo/pluginkit\n',
    'pluginkit/rpc.go': 'package pluginkit\n',
    'tools/go.mod': 'module example.com/repo/tools\n',
    'tools/manifests/manifests_test.go': 'package manifests\n',
    'extensions/alpha/go.mod': 'module example.com/repo/extensions/alpha\n',
    'extensions/alpha/go.sum': '',
    'extensions/alpha/main.go': 'package main\n',
    'extensions/alpha/internal/x/x.go': 'package x\n',
    'extensions/node-one/package.json': '{}',
    'extensions/node-one/src/main.mjs': '',
    'extensions/skills-only/persona-extension.json': '{}',
    'dist/alpha/stray.go': 'package stray\n',
}


def tree(files):
    tmp = tempfile.TemporaryDirectory()
    root = pathlib.Path(tmp.name)
    for name, text in files.items():
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
    return tmp, str(root)


class LayoutTests(unittest.TestCase):
    def problems(self, files):
        tmp, root = tree(files)
        with tmp:
            return layout.problems(root)

    def test_good_tree(self):
        self.assertEqual(self.problems(GOOD), [])

    def test_modules(self):
        tmp, root = tree(GOOD)
        with tmp:
            self.assertEqual(layout.modules(root), ['pluginkit', 'tools', 'extensions/alpha'])

    def test_root_package_files(self):
        for name in ['go.mod', 'go.sum', 'go.work', 'package.json', 'package-lock.json', 'pnpm-lock.yaml',
                     'yarn.lock', 'node_modules/x/index.js', 'Cargo.toml', 'pyproject.toml', 'requirements.txt',
                     'requirements-dev.txt', 'vendor/modules.txt']:
            with self.subTest(name=name):
                bad = self.problems({**GOOD, name: ''})
                self.assertEqual(len(bad), 1, bad)
                self.assertTrue(bad[0].startswith(name.split('/')[0] + ':'), bad)

    def test_same_names_inside_an_extension_are_fine(self):
        self.assertEqual(self.problems({**GOOD, 'extensions/node-one/package-lock.json': '',
                                        'extensions/py/pyproject.toml': ''}), [])

    def test_go_extension_without_its_module(self):
        files = {**GOOD, 'extensions/beta/main.go': 'package main\n'}
        self.assertEqual(self.problems(files), [
            'extensions/beta: has Go code but no go.mod of its own',
            'extensions/beta: Go code that belongs to no module',
        ])

    def test_module_path_names_the_extension(self):
        files = {**GOOD, 'extensions/alpha/go.mod': 'module example.com/repo\n'}
        self.assertEqual(len(self.problems(files)), 1)

    def test_go_outside_every_module(self):
        files = {**GOOD, 'scripts/gen/main.go': 'package main\n'}
        self.assertEqual(self.problems(files), ['scripts/gen: Go code that belongs to no module'])


    def test_compiled_program_in_an_extension(self):
        for magic in [b'\x7fELF\x02\x01\x01', b'\xcf\xfa\xed\xfe\x0c\x00', b'\xca\xfe\xba\xbe\x00']:
            with self.subTest(magic=magic):
                tmp, root = tree(GOOD)
                with tmp:
                    (pathlib.Path(root) / 'extensions/alpha/alpha').write_bytes(magic + b'\x00' * 64)
                    self.assertEqual(layout.problems(root), [
                        'extensions/alpha/alpha: a compiled program; the release builds it from source, '
                        'so it does not go in the repository (go build -o elsewhere, or delete it)'])

    def test_untracked_program_in_a_checkout_is_fine(self):
        # A local `go build` in a git checkout leaves the program untracked: only what is committed counts.
        tmp, root = tree(GOOD)
        with tmp:
            subprocess.run(['git', 'init', '-q', root], check=True)
            subprocess.run(['git', '-C', root, 'add', '.'], check=True)
            (pathlib.Path(root) / 'extensions/alpha/alpha').write_bytes(b'\x7fELF' + b'\x00' * 64)
            self.assertEqual(layout.problems(root), [])
            subprocess.run(['git', '-C', root, 'add', 'extensions/alpha/alpha'], check=True)
            self.assertEqual(len(layout.problems(root)), 1)


if __name__ == '__main__':
    unittest.main()
