#!/usr/bin/env python3
"""Synthetic icon validation, provenance and old-hub packaging regression tests."""
import copy
import hashlib
import importlib.util
import json
import pathlib
import shutil
import struct
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock
import zlib

REPO = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('manifest', REPO / 'scripts/manifest.py')
manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manifest)
icons = manifest.icons


def png(width=256, height=256):
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    header = struct.pack('>IIBBBBB', width, height, 8, 6, 0, 0, 0)
    pixels = (b'\0' + b'\0\0\0\xff' * width) * height
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', header) + chunk(b'IDAT', zlib.compress(pixels)) + chunk(b'IEND', b'')


def svg(content='', dimensions='viewBox="0 0 256 256"'):
    return ('<svg xmlns="http://www.w3.org/2000/svg" %s>%s</svg>' % (dimensions, content)).encode()


class IconTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)
        self.ext = self.root / 'extensions/example'
        (self.ext / 'assets').mkdir(parents=True)
        self.source = {'id': 'example', 'name': 'Example', 'version': '0.1.0', 'tags': ['demo'], 'icon': 'assets/icon.png'}
        (self.ext / 'assets/icon.png').write_bytes(png())
        (self.ext / 'assets/LICENSE').write_text('Synthetic icon fixture license. No upstream artwork.')
        (self.ext / manifest.NEW).write_text(json.dumps(self.source))

    def test_png_and_svg_dimensions(self):
        self.assertEqual(icons.validate_image(png(), 'assets/icon.png'), (256, 256))
        self.assertEqual(icons.validate_image(svg('<path d="M0 0h256v256H0z"/>'), 'assets/icon.svg'), (256, 256))
        self.assertEqual(icons.validate_image(svg(dimensions='width="32px" height="32px"'), 'assets/icon.svg'), (32, 32))
        self.assertEqual(icons.validate_image(svg('<defs><linearGradient id="fill"><stop offset="0"/></linearGradient></defs><path fill="url(#fill)" d="M0 0"/>'), 'assets/icon.svg'), (256, 256))

    def test_invalid_and_oversized_pngs(self):
        for data in (b'PNG', png(128, 128), png(256, 512), png()[:-1], png() + b'junk', png()[:40] + b'bad', b'x' * (icons.MAX_BYTES + 1)):
            with self.subTest(length=len(data)), self.assertRaises(ValueError):
                icons.validate_image(data, 'assets/icon.png')

    def test_unsafe_svg_is_rejected(self):
        values = [
            svg('<script>alert(1)</script>'), svg('<foreignObject/>'), svg('<image href="https://example.com/icon.png"/>'),
            svg('<use href="https://example.com/icon.svg#x"/>'), svg('<use href="data:image/svg+xml,x"/>'),
            svg('<path onload="alert(1)"/>'), svg('<path fill="url(https://example.com/x)"/>'),
            svg('<path style="fill:u\\72l(https://example.com/x)"/>'), svg('<style>@import "https://example.com/x";</style>'),
            svg('<path xml:base="https://example.com/"/>'), svg('<path xmlns:x="https://example.com/ns" x:href="#x"/>'),
            b'<!DOCTYPE svg [<!ENTITY x "bad">]>' + svg('&x;'), b'<?unsafe?>' + svg(),
            svg(dimensions='viewBox="0 0 256 128"'), svg(dimensions='width="100%" height="100%"'),
            svg(dimensions='viewBox="0 0 nan nan"'), svg(dimensions=''),
            ('<!DOCTYPE svg [<!ENTITY x "bad">]>' + svg('&x;').decode()).encode('utf-16'),
        ]
        for data in values:
            with self.subTest(data=data[:100]), self.assertRaises(ValueError):
                icons.validate_image(data, 'assets/icon.svg')

    def test_paths_and_symlinks(self):
        for path in (None, '', '/assets/icon.png', 'assets/../icon.png', 'assets//icon.png', 'assets/./icon.png', 'looks/icon.png', 'assets\\icon.png', 'assets/icon.gif'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                icons.validate_icon(self.ext, path)
        (self.ext / 'assets/link.png').symlink_to('icon.png')
        (self.ext / 'assets/sub').symlink_to(self.ext / 'assets', target_is_directory=True)
        for path in ('assets/link.png', 'assets/sub/icon.png', 'assets/missing.png'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                icons.validate_icon(self.ext, path)
        with mock.patch.object(manifest, 'ROOT', str(self.root)):
            self.assertEqual(manifest.check_files('example', self.source), [])
            self.assertTrue(manifest.check_files('example', {**self.source, 'icon': 'assets/link.png'}))
            self.assertEqual(manifest.icon('example'), 'assets/icon.png')

    def registry(self):
        return {'schema_version': 1, 'sources': {'synthetic': {
            'repository': 'https://example.com/artwork', 'revision': 'synthetic-revision', 'license': 'CC0-1.0',
            'license_url': 'https://example.com/license', 'license_file': 'assets/LICENSE', 'attribution': 'Synthetic fixture',
        }}, 'icons': {'example': {**icons.validate_icon(self.ext, self.source['icon']), 'source_id': 'synthetic',
            'source_url': 'https://example.com/artwork/icon.png', 'trademark_owner': 'Synthetic fixture', 'usage': 'Test only'}}}

    def test_registry_matches_bytes_and_inventory(self):
        registry = self.registry()
        file = self.root / 'icon-sources.json'
        file.write_text(json.dumps(registry))
        icons.validate_registry(self.root, [self.source])
        for key, value in (('sha256', '0' * 64), ('width', 512), ('height', True), ('source_id', 'missing'), ('path', 'assets/other.png'), ('format', 'svg')):
            bad = copy.deepcopy(registry)
            bad['icons']['example'][key] = value
            file.write_text(json.dumps(bad))
            with self.subTest(key=key), self.assertRaises(ValueError):
                icons.validate_registry(self.root, [self.source])
        file.write_text(json.dumps(registry))
        (self.ext / 'assets/LICENSE').unlink()
        with self.assertRaises(ValueError):
            icons.validate_registry(self.root, [self.source])
        file.unlink()
        with self.assertRaises(ValueError):
            icons.validate_registry(self.root, [self.source])
        icons.validate_registry(self.root, [{k: v for k, v in self.source.items() if k != 'icon'}])

    def test_original_svg_provenance_and_safety(self):
        original = self.ext / 'assets/icon-source.svg'
        data = svg('<path d="M0 0h64v32z"/>', dimensions='viewBox="0 0 64 32"')
        original.write_bytes(data)
        registry = self.registry()
        record = registry['icons']['example']
        record['source_sha256'] = hashlib.sha256(data).hexdigest()
        record['transformation'] = 'Rendered on a transparent square canvas for this synthetic fixture.'
        file = self.root / 'icon-sources.json'
        file.write_text(json.dumps(registry))
        icons.validate_registry(self.root, [self.source])
        record['source_sha256'] = '0' * 64
        file.write_text(json.dumps(registry))
        with self.assertRaisesRegex(ValueError, 'source_sha256'):
            icons.validate_registry(self.root, [self.source])
        record.pop('source_sha256')
        file.write_text(json.dumps(registry))
        original.write_bytes(svg('<script>unsafe</script>'))
        with self.assertRaises(ValueError):
            icons.validate_registry(self.root, [self.source])

    def test_original_png_can_be_non_square_but_display_icon_cannot(self):
        data = png(512, 256)
        (self.ext / 'assets/icon-source.png').write_bytes(data)
        registry = self.registry()
        record = registry['icons']['example']
        record.update(source_path='assets/icon-source.png', source_sha256=hashlib.sha256(data).hexdigest(),
                      transformation='Synthetic image fitted to a square without changing its aspect ratio.')
        file = self.root / 'icon-sources.json'
        file.write_text(json.dumps(registry))
        icons.validate_registry(self.root, [self.source])
        with self.assertRaises(ValueError):
            icons.validate_image(data, 'assets/icon.png')
        record['source_path'] = 'assets/../../outside.png'
        file.write_text(json.dumps(registry))
        with self.assertRaisesRegex(ValueError, 'source_path'):
            icons.validate_registry(self.root, [self.source])

    def test_author_schema_and_python_path_validation_agree(self):
        import re
        schema = json.loads((REPO / 'schema/extension/author.schema.json').read_text())['$defs']['IconPath']
        for value in ('assets/icon.png', 'assets/nested/icon.svg', 'assets/../icon.png', 'assets//icon.png', 'assets/.hidden.svg', 'assets/icon.gif', 'assets/' + 'x' * 240 + '.png'):
            self.assertEqual(icons.valid_path(value), len(value) <= schema['maxLength'] and bool(re.fullmatch(schema['pattern'], value)), value)

    def test_package_keeps_assets_but_not_runtime_icon(self):
        (self.root / 'scripts').mkdir()
        for name in ('package.sh', 'manifest.py', 'catalog.py', 'icons.py'):
            shutil.copy2(REPO / 'scripts' / name, self.root / 'scripts' / name)
        r = subprocess.run(['bash', 'scripts/package.sh', 'example'], cwd=self.root, capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        with tarfile.open(self.root / 'dist/example-0.1.0.tar.gz') as archive:
            for name in (manifest.NEW, manifest.OLD):
                runtime = json.load(archive.extractfile('example/' + name))
                self.assertFalse(set(runtime) & {'icon', 'tags', 'replaces'})
            self.assertEqual(archive.extractfile('example/assets/icon.png').read(), png())
            self.assertEqual(archive.extractfile('example/assets/LICENSE').read(), (self.ext / 'assets/LICENSE').read_bytes())
        self.assertEqual(json.loads((self.ext / manifest.NEW).read_text()), self.source)


if __name__ == '__main__':
    unittest.main()
