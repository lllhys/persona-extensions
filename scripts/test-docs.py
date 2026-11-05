#!/usr/bin/env python3
"""Tests for the local Markdown link checker: python3 -I scripts/test-docs.py."""
import importlib.util
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('check_docs', pathlib.Path(__file__).with_name('check-docs.py'))
docs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(docs)


class LinkTests(unittest.TestCase):
    def check_files(self, files):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            for name, text in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(text)
            return docs.check(root, [path for path in files if path.endswith('.md')])

    def test_relative_directory_fragment_and_external(self):
        errors, count = self.check_files({
            'README.md': '[guide](docs/guide.md#setup) [directory](docs/) [site](https://example.com/missing) [mail](mailto:me@example.com)',
            'docs/guide.md': '# Guide\n## Setup\n[home](../README.md) [root](/README.md)',
        })
        self.assertEqual(errors, [])
        self.assertEqual(count, 4)

    def test_missing_file_heading_and_repository_escape(self):
        errors, _ = self.check_files({'README.md': '[file](missing.md) [heading](#absent) [escape](../outside.md)'})
        self.assertEqual(len(errors), 3)
        self.assertIn('missing target', errors[0])
        self.assertIn('missing heading', errors[1])
        self.assertIn('leaves repository', errors[2])

    def test_fenced_code_and_duplicate_headings(self):
        errors, count = self.check_files({'README.md': '# Title\n## Setup\n## Setup\n[again](#setup-1)\n```md\n[not a link](missing.md)\n## Hidden\n```\n'})
        self.assertEqual(errors, [])
        self.assertEqual(count, 1)
        self.assertNotIn('hidden', docs.anchors('~~~\n## Hidden\n~~~~\n'))

    def test_reference_and_collapsed_links(self):
        errors, count = self.check_files({'README.md': '# Home\n[home][root] [root][]\n[root]: #home\n'})
        self.assertEqual(errors, [])
        self.assertEqual(count, 1)
        errors, _ = self.check_files({'README.md': '[missing][unknown]'})
        self.assertIn('undefined reference', errors[0])

    def test_encoded_path_angle_target_and_unicode_heading(self):
        errors, count = self.check_files({
            'README.md': '[space](a%20b.md#setup) [angle](<a b.md#setup>) [unicode](#中文)\n## 中文\n',
            'a b.md': '## Setup\n',
        })
        self.assertEqual(errors, [])
        self.assertEqual(count, 3)

    def test_inline_code_is_not_a_link(self):
        errors, count = self.check_files({'README.md': '`^[a-z0-9][a-z0-9_-]{0,31}$` and `[example](missing.md)`'})
        self.assertEqual(errors, [])
        self.assertEqual(count, 0)

    def test_line_numbers_after_code_fences(self):
        errors, _ = self.check_files({'README.md': '```\nignored\n```\n[missing](missing.md)\n'})
        self.assertTrue(errors[0].startswith('README.md:4:'))


if __name__ == '__main__':
    unittest.main()
