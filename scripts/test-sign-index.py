#!/usr/bin/env python3
"""Tests for sign-index's None-vs-reason verdict: python3 -I scripts/test-sign-index.py."""
import base64
import contextlib
import importlib.util
import io
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('sign_index', pathlib.Path(__file__).with_name('sign-index.py'))
sign_index = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sign_index)


def openssl(*args):
    return subprocess.run(['openssl', *args], capture_output=True, check=True).stdout


class VerifyOneTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        d = pathlib.Path(self.tmp.name)
        key_pem = d / 'key.pem'
        openssl('genpkey', '-algorithm', 'ed25519', '-out', str(key_pem))
        der = openssl('pkey', '-in', str(key_pem), '-pubout', '-outform', 'DER')
        self.raw = der[len(sign_index.SPKI_PREFIX):]
        self.assertEqual(len(self.raw), 32)
        self.index = d / 'index.json'
        self.index.write_bytes(b'{"id": "x"}\n')
        sig = openssl('pkeyutl', '-sign', '-inkey', str(key_pem), '-rawin', '-in', str(self.index))
        self.good_sig = {'alg': 'ed25519', 'key_id': sign_index.key_id(self.raw),
                         'sig': base64.b64encode(sig).decode()}
        self.write_sig(self.good_sig)
        self.scratch = d / 'scratch'
        self.scratch.mkdir()

    def tearDown(self):
        self.tmp.cleanup()

    def write_sig(self, obj):
        pathlib.Path(str(self.index) + '.sig').write_text(json.dumps(obj) + '\n')

    def verify_one(self):
        return sign_index.verify_one(str(self.index), self.raw, str(self.scratch))

    def test_a_good_signature_passes(self):
        self.assertIsNone(self.verify_one())

    def test_a_key_mismatch_is_a_reason(self):
        other = bytes([self.raw[0] ^ 1]) + self.raw[1:]
        why = sign_index.verify_one(str(self.index), other, str(self.scratch))
        self.assertIsNotNone(why)
        self.assertIn('signed by key', why)

    def test_bad_base64_is_a_reason_not_a_crash(self):
        # A .sig whose sig field is garbage must report bad, not raise binascii.Error.
        self.write_sig({'alg': 'ed25519', 'key_id': sign_index.key_id(self.raw), 'sig': 'not base64!'})
        why = self.verify_one()
        self.assertIsNotNone(why)
        self.assertIn('malformed signature', why)

    def test_a_non_string_sig_field_is_a_reason_not_a_crash(self):
        # A JSON number/null sig trips TypeError in b64decode, not ValueError: same verdict.
        for bad in (123, None, ['x'], {'y': 1}):
            self.write_sig({'alg': 'ed25519', 'key_id': sign_index.key_id(self.raw), 'sig': bad})
            why = self.verify_one()
            self.assertIsNotNone(why, bad)
            self.assertIn('malformed signature', why, bad)

    def test_wrong_signature_bytes_is_a_reason(self):
        self.write_sig({'alg': 'ed25519', 'key_id': sign_index.key_id(self.raw),
                        'sig': base64.b64encode(bytes(64)).decode()})
        why = self.verify_one()
        self.assertIsNotNone(why)
        self.assertIn('signature does not match', why)


class VerifyIndexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        d = pathlib.Path(self.tmp.name)
        key_pem = d / 'key.pem'
        openssl('genpkey', '-algorithm', 'ed25519', '-out', str(key_pem))
        der = openssl('pkey', '-in', str(key_pem), '-pubout', '-outform', 'DER')
        self.raw = der[len(sign_index.SPKI_PREFIX):]
        self.line = 'ed25519:' + base64.b64encode(self.raw).decode()
        self.pub = d / 'market.pub'
        self.pub.write_text(self.line + '\n')
        self.index = d / 'persona-extensions.json'
        self.index.write_text(json.dumps({'signing_key': self.line}) + '\n')
        sig = openssl('pkeyutl', '-sign', '-inkey', str(key_pem), '-rawin', '-in', str(self.index))
        pathlib.Path(str(self.index) + '.sig').write_text(json.dumps(
            {'alg': 'ed25519', 'key_id': sign_index.key_id(self.raw),
             'sig': base64.b64encode(sig).decode()}) + '\n')
        self.old_pub, self.old_indexes = sign_index.PUB, sign_index.INDEXES
        sign_index.PUB = str(self.pub)
        sign_index.INDEXES = ((str(self.index), 'extensions'),)

    def tearDown(self):
        sign_index.PUB, sign_index.INDEXES = self.old_pub, self.old_indexes
        self.tmp.cleanup()

    def test_a_good_index_verifies(self):
        self.assertTrue(sign_index.verify())

    def test_a_malformed_index_is_a_reason_not_a_crash(self):
        self.index.write_text('{not json\n')
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            ok = sign_index.verify()
        self.assertFalse(ok)
        self.assertIn('malformed index', err.getvalue())

    def test_unsigned_with_a_malformed_sig_warns_not_crashes(self):
        # sign() with no key walks verify_one for its stale-signature warning.
        pathlib.Path(str(self.index) + '.sig').write_text(json.dumps(
            {'alg': 'ed25519', 'key_id': sign_index.key_id(self.raw), 'sig': 'not base64!'}) + '\n')
        env = os.environ.pop('EXTENSION_MARKET_SIGNING_KEY', None)
        try:
            err = io.StringIO()
            with contextlib.redirect_stderr(err):
                ok = sign_index.sign(None)
        finally:
            if env is not None:
                os.environ['EXTENSION_MARKET_SIGNING_KEY'] = env
        self.assertTrue(ok)
        self.assertIn('malformed signature', err.getvalue())


if __name__ == '__main__':
    unittest.main()
