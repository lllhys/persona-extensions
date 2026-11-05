import asyncio
import importlib.util
import json
from pathlib import Path
import stat
import sys
import threading
import time
import unittest


spec = importlib.util.spec_from_file_location('companion_bridge', Path(__file__).with_name('bridge.py'))
bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bridge)

SECRET = 'SYNTHETIC_PRIVATE_REASON'
CREDS = {'region': 'feishu', 'cookies': {'session': 'SYNTHETIC_COOKIE'},
         'app_key': 'SYNTHETIC_APP_KEY', 'device_id': 'synthetic-device'}
POLICY = {'region': 'feishu', 'allowed_chats': ['chat-a'], 'allowed_senders': ['sender-a']}


class FakeAuth:
    valid = True
    user = 'self-a'
    failure = False
    loaded = []

    def __init__(self, path):
        self.path = path
        self.cookies = {}
        self.user_id = ''

    def load(self):
        data = json.loads(self.path.read_text())
        self.cookies = data['cookies']
        self.loaded.append(data)
        return bool(self.cookies)

    def validate(self, fetch_profile=False):
        assert fetch_profile is True
        if self.failure:
            raise RuntimeError(SECRET)
        self.user_id = self.user
        return self.valid, SECRET


class FakeClient:
    result = 'message-sent'
    failure = False
    disconnect = False
    messages = []
    sent = []

    def __init__(self, auth):
        self.auth = auth

    async def connect_websocket(self, on_message):
        for msg in self.messages:
            await on_message(msg)
        if self.disconnect:
            raise RuntimeError(SECRET)
        await asyncio.Future()

    def send_msg(self, text, chat_id, root_id=None):
        self.sent.append((text, chat_id, root_id))
        if self.failure:
            raise RuntimeError(SECRET)
        return self.result


class FakeQR:
    success = True
    failure = False
    gate = None
    waits = []

    def __init__(self, region):
        assert region == 'feishu'
        self.qr_content = 'SYNTHETIC_OPAQUE_QR'

    def wait(self, timeout=180, interval=2):
        self.waits.append((timeout, interval))
        if self.gate is not None:
            self.gate.wait(1)
        if self.failure:
            raise RuntimeError(SECRET)
        return self.success, {'flow_key': SECRET}

    def finish(self, auth):
        auth.cookies = {'session': 'SYNTHETIC_QR_COOKIE'}
        auth.user_id = 'self-a'
        auth.save()
        return SECRET


def message(**kwargs):
    result = {'msg_id': 'message-a', 'chat_id': 'chat-a', 'from_id': 'sender-a',
              'msg_type': 4, 'content': 'synthetic text', 'anchor': 'root-a',
              'create_time': '2026-01-01T10:00:00Z', 'content_data': SECRET}
    result.update(kwargs)
    return result


class CompanionTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        FakeAuth.valid, FakeAuth.user, FakeAuth.failure = True, 'self-a', False
        FakeAuth.loaded = []
        FakeClient.result, FakeClient.failure, FakeClient.disconnect = 'message-sent', False, False
        FakeClient.sent, FakeClient.messages = [], []
        FakeQR.success, FakeQR.failure, FakeQR.gate = True, False, None
        FakeQR.waits = []
        self.events = []
        self.bridge = bridge.Bridge(FakeAuth, FakeClient, FakeQR, lambda name: name,
                                    self.events.append, timeout=0.2, qr_timeout=0.3)

    async def asyncTearDown(self):
        await self.bridge.close()

    async def request(self, method, params=None):
        return await self.bridge.request({'id': 7, 'method': method, 'params': params or {}})

    async def opened(self):
        response = await self.request('session.open', dict(POLICY, credentials=CREDS))
        self.assertEqual(response, {'id': 7, 'result': {'state': 'ok'}})
        return self.bridge.vault

    async def wait_state(self, state):
        for _ in range(100):
            result = await self.request('login.poll')
            if result['result']['state'] == state:
                return result['result']
            await asyncio.sleep(0.005)
        self.fail('synthetic worker did not settle')

    async def test_normalization_and_authorization_and(self):
        item = bridge.normalize(message(), {'chat-a'}, {'sender-a'}, 'self-a')
        self.assertEqual(item, {'id': 'message-a', 'chat': 'chat-a', 'sender': 'sender-a',
                                'text': 'synthetic text', 'anchor': 'root-a',
                                'at': '2026-01-01T10:00:00Z'})
        for change in ({'chat_id': 'chat-b'}, {'from_id': 'sender-b'},
                       {'from_id': 'self-a'}, {'from_id': ''}, {'msg_type': 2},
                       {'msg_type': True}, {'msg_id': None}, {'content': {'text': SECRET}},
                       {'content': 'x' * 4097}, {'anchor': {}}, {'content': ''}):
            with self.subTest(change=change):
                self.assertIsNone(bridge.normalize(message(**change), {'chat-a'},
                                                   {'sender-a', 'self-a'}, 'self-a'))
        self.assertIsNone(bridge.normalize(message(), {'chat-a'}, {'sender-a'}, ''))
        self.assertNotIn('at', bridge.normalize(message(create_time=SECRET),
                                               {'chat-a'}, {'sender-a'}, 'self-a'))

    async def test_listener_emits_only_safe_text(self):
        FakeClient.messages = [message(), message(from_id='self-a'), message(msg_type=1),
                               message(chat_id='chat-b'), message(from_id='sender-b')]
        await self.opened()
        for _ in range(100):
            if self.events:
                break
            await asyncio.sleep(0.005)
        self.assertEqual(len(self.events), 1)
        self.assertEqual(self.events[0]['event'], 'message')
        self.assertNotIn(SECRET, json.dumps(self.events))

    async def test_permissions_and_cleanup(self):
        vault = await self.opened()
        self.assertEqual(stat.S_IMODE(vault.directory.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(vault.path.stat().st_mode), 0o600)
        self.assertEqual(FakeAuth.loaded[-1], CREDS)
        auth = vault.auth
        self.assertEqual(await self.request('session.close'), {'id': 7, 'result': {}})
        self.assertFalse(vault.directory.exists())
        self.assertEqual(auth.cookies, {})
        self.assertEqual(auth.user_id, '')
        with self.assertRaises(bridge.Failure):
            vault.save()
        self.assertFalse(vault.directory.exists())

    async def test_invalid_credentials_and_policy(self):
        invalid = [None, {}, {'cookies': []}, {'cookies': {}},
                   {'cookies': {'session': 3}}, dict(CREDS, app_secret=SECRET),
                   dict(CREDS, region='lark'), dict(CREDS, saved_at=float('nan')),
                   dict(CREDS, saved_at=True), dict(CREDS, user_id=[])]
        for data in invalid:
            result = await self.request('session.open', dict(POLICY, credentials=data))
            self.assertEqual(result['error']['code'], 'invalid_params')
            self.assertNotIn(SECRET, json.dumps(result))
        for change in ({'region': 'lark'}, {'allowed_chats': []}, {'allowed_senders': []},
                       {'allowed_chats': ['*']}, {'allowed_senders': ['sender?']},
                       {'allowed_senders': 'sender-a'}, {'allowed_chats': [True]}):
            result = await self.request('session.open', dict(POLICY, credentials=CREDS, **change))
            self.assertEqual(result['error']['code'], 'invalid_params')
        self.assertIsNone(self.bridge.vault)

    async def test_auth_false_and_missing_self_id(self):
        for valid, user in ((False, 'self-a'), (True, '')):
            FakeAuth.valid, FakeAuth.user = valid, user
            result = await self.request('session.open', dict(POLICY, credentials=CREDS))
            self.assertEqual(result['error']['code'], 'auth_expired')
            self.assertNotIn(SECRET, json.dumps(result))
            self.assertIsNone(self.bridge.vault)

    async def test_upstream_exception_is_redacted(self):
        FakeAuth.failure = True
        result = await self.request('session.open', dict(POLICY, credentials=CREDS))
        self.assertEqual(result['error'], {'code': 'unavailable'})
        self.assertNotIn(SECRET, json.dumps(result))

    async def test_check_conservative_false(self):
        await self.opened()
        FakeAuth.valid = False
        result = await self.request('session.check')
        self.assertEqual(result['result'], {'state': 'not_signed_in'})
        self.assertNotIn(SECRET, json.dumps(result))

    async def test_check_exception(self):
        await self.opened()
        FakeAuth.failure = True
        self.assertEqual((await self.request('session.check'))['result'], {'state': 'error'})

    async def test_message_during_check_is_delivered(self):
        probe, ready, delivered = threading.Event(), threading.Event(), threading.Event()
        class SlowAuth(FakeAuth):
            def validate(self, fetch_profile=False):
                if ready.is_set():
                    probe.wait(1)
                return FakeAuth.validate(self, fetch_profile)
        class LateClient(FakeClient):
            async def connect_websocket(self, on_message):
                while not ready.is_set():
                    await asyncio.sleep(0.005)
                await asyncio.sleep(0.02)
                await on_message(message())
                delivered.set()
                await asyncio.Future()
        self.bridge.auth_type, self.bridge.client_type, self.bridge.timeout = SlowAuth, LateClient, 2
        await self.opened()
        ready.set()
        check = asyncio.create_task(self.request('session.check'))
        for _ in range(200):
            if delivered.is_set():
                break
            await asyncio.sleep(0.005)
        probe.set()
        self.assertEqual((await check)['result'], {'state': 'ok'})
        self.assertEqual([e['event'] for e in self.events], ['message'])

    async def test_check_rejects_changed_self_id(self):
        await self.opened()
        FakeAuth.user = 'self-b'
        self.assertEqual((await self.request('session.check'))['result'], {'state': 'not_signed_in'})

    async def test_disconnect_status_and_no_reconnect(self):
        FakeClient.disconnect = True
        await self.opened()
        for _ in range(100):
            if self.events:
                break
            await asyncio.sleep(0.005)
        self.assertEqual(self.events, [{'event': 'status', 'state': 'error'}])
        await asyncio.sleep(0.02)
        self.assertEqual(len(self.events), 1)

    async def test_disconnect_auth_expired(self):
        await self.opened()
        FakeAuth.valid = False
        self.bridge.ws.cancel()
        await asyncio.sleep(0)
        self.bridge.ws = asyncio.create_task(self.bridge.listen(FakeClient(FakeAuth(None)), self.bridge.vault))
        FakeClient.disconnect = True
        for _ in range(100):
            if self.events:
                break
            await asyncio.sleep(0.005)
        self.assertEqual(self.events[-1], {'event': 'status', 'state': 'not_signed_in'})

    async def test_send_validation_and_success(self):
        await self.opened()
        base = {'chat': 'chat-a', 'text': 'x' * 4096, 'anchor': 'root-a', 'request_id': 'request-a'}
        for change in ({'chat': 'chat-b'}, {'text': 'x' * 4097}, {'text': ''},
                       {'request_id': ''}, {'request_id': None}, {'anchor': {}}, {'anchor': ''}):
            result = await self.request('session.send', dict(base, **change))
            self.assertEqual(result['error']['code'], 'invalid_params')
        self.assertEqual(FakeClient.sent, [])
        result = await self.request('session.send', base)
        self.assertEqual(result['result'], {'message_id': 'message-sent'})
        self.assertEqual(FakeClient.sent, [(base['text'], 'chat-a', 'root-a')])

    async def test_unknown_send_no_retry(self):
        await self.opened()
        FakeClient.result = None
        params = {'chat': 'chat-a', 'text': 'synthetic send', 'request_id': 'request-a'}
        for _ in range(2):
            result = await self.request('session.send', params)
            self.assertEqual(result['error']['code'], 'unknown_result')
        self.assertEqual(len(FakeClient.sent), 1)

    async def test_send_exception_unknown(self):
        await self.opened()
        FakeClient.failure = True
        result = await self.request('session.send', {'chat': 'chat-a', 'text': 'synthetic', 'request_id': 'request-a'})
        self.assertEqual(result['error']['code'], 'unknown_result')
        self.assertNotIn(SECRET, json.dumps(result))
        self.assertEqual(len(FakeClient.sent), 1)

    async def test_qr_pending_success_export_cleanup(self):
        FakeQR.gate = threading.Event()
        result = await self.request('login.begin', POLICY)
        vault = self.bridge.login['vault']
        self.assertEqual(result['result'], {'state': 'pending', 'qr': 'SYNTHETIC_OPAQUE_QR'})
        self.assertEqual((await self.request('login.poll'))['result'], {'state': 'pending'})
        FakeQR.gate.set()
        ready = await self.wait_state('ready')
        self.assertEqual(ready['credentials']['cookies'], {'session': 'SYNTHETIC_QR_COOKIE'})
        self.assertNotIn(SECRET, json.dumps(ready))
        self.assertEqual(FakeQR.waits, [(180, 2)])
        self.assertFalse(vault.directory.exists())
        self.assertEqual((await self.request('login.cancel'))['result'], {})
        self.assertIsNone(self.bridge.login)

    async def test_qr_expiry_and_exception(self):
        for success, failure in ((False, False), (True, True)):
            FakeQR.success, FakeQR.failure = success, failure
            await self.request('login.begin', POLICY)
            vault = self.bridge.login['vault']
            expired = await self.wait_state('expired')
            self.assertEqual(expired, {'state': 'expired'})
            self.assertFalse(vault.directory.exists())

    async def test_qr_outer_timeout_and_cancel_no_late_file(self):
        FakeQR.gate = threading.Event()
        self.bridge.qr_timeout = 0.01
        await self.request('login.begin', POLICY)
        vault = self.bridge.login['vault']
        await self.wait_state('expired')
        self.assertFalse(vault.directory.exists())
        await self.request('login.cancel')
        FakeQR.gate.set()
        await asyncio.sleep(0.03)
        self.assertFalse(vault.directory.exists())

    async def test_send_timeout_is_unknown_and_never_repeated(self):
        await self.opened()
        self.bridge.timeout = 0.01
        calls = []
        def slow_send(*args, **kwargs):
            calls.append(True)
            time.sleep(0.06)
            return 'message-late'
        self.bridge.client.send_msg = slow_send
        params = {'chat': 'chat-a', 'text': 'synthetic', 'request_id': 'request-slow'}
        result = await self.request('session.send', params)
        self.assertEqual(result['error']['code'], 'unknown_result')
        await self.request('session.send', params)
        self.assertEqual(len(calls), 1)
        await asyncio.sleep(0.07)
        self.assertEqual(len(calls), 1)

    async def test_stored_self_id_cannot_replace_profile_validation(self):
        def no_profile(auth, fetch_profile=False):
            return True, SECRET
        class NoProfileAuth(FakeAuth):
            validate = no_profile
        self.bridge.auth_type = NoProfileAuth
        data = dict(CREDS, user_id='synthetic-stale-self')
        result = await self.request('session.open', dict(POLICY, credentials=data))
        self.assertEqual(result['error']['code'], 'auth_expired')

    async def test_qr_constructor_and_finish_failures_are_redacted(self):
        class ConstructorFails(FakeQR):
            def __init__(self, region):
                raise RuntimeError(SECRET)
        self.bridge.qr_type = ConstructorFails
        result = await self.request('login.begin', POLICY)
        self.assertEqual(result['error']['code'], 'unavailable')
        self.assertIsNone(self.bridge.login)
        class FinishFails(FakeQR):
            def finish(self, auth):
                raise RuntimeError(SECRET)
        self.bridge.qr_type = FinishFails
        await self.request('login.begin', POLICY)
        vault = self.bridge.login['vault']
        result = await self.wait_state('expired')
        self.assertEqual(result, {'state': 'expired'})
        self.assertNotIn(SECRET, json.dumps(result))
        self.assertFalse(vault.directory.exists())

    async def test_cancel_pending_qr_cannot_publish_credentials(self):
        FakeQR.gate = threading.Event()
        await self.request('login.begin', POLICY)
        vault = self.bridge.login['vault']
        self.assertEqual((await self.request('login.cancel'))['result'], {})
        FakeQR.gate.set()
        await asyncio.sleep(0.03)
        self.assertFalse(vault.directory.exists())
        self.assertEqual((await self.request('login.poll'))['result'], {'state': 'expired'})

    async def test_bounded_sync_timeout(self):
        with self.assertRaises(bridge.Failure) as error:
            await bridge.bounded(lambda: time.sleep(0.05), deadline=0.005)
        self.assertEqual(error.exception.code, 'unavailable')
        await asyncio.sleep(0.06)

    async def test_eof_closes_session(self):
        vault = await self.opened()
        reader = asyncio.StreamReader()
        reader.feed_eof()
        await bridge.serve(self.bridge, reader)
        self.assertFalse(vault.directory.exists())

    async def test_protocol_parse_limits_and_error_enum(self):
        reader = asyncio.StreamReader(limit=bridge.LIMIT)
        reader.feed_data(b'{bad json}\n' + b'{"id":true,"method":"session.close"}\n'
                         + b'{"id":2,"method":"synthetic.unknown","params":{}}\n')
        reader.feed_eof()
        await bridge.serve(self.bridge, reader)
        self.assertEqual([e['error']['code'] for e in self.events],
                         ['invalid_params', 'invalid_params', 'unknown_method'])
        self.events.clear()
        reader = asyncio.StreamReader(limit=bridge.LIMIT)
        prefix = b'{"id":3,"method":"synthetic.unknown","params":{"z":"'
        frame = prefix + b'x' * (bridge.LIMIT - len(prefix) - len(b'"}}')) + b'"}}'
        self.assertEqual(len(frame), bridge.LIMIT)
        reader.feed_data(frame + b'\n')
        reader.feed_eof()
        await bridge.serve(self.bridge, reader)
        self.assertEqual(self.events, [{'id': 3, 'error': {'code': 'unknown_method'}}])
        self.events.clear()
        reader = asyncio.StreamReader(limit=bridge.LIMIT)
        reader.feed_data(b'x' * (bridge.LIMIT + 1) + b'\n')
        reader.feed_eof()
        await bridge.serve(self.bridge, reader)
        self.assertEqual(self.events, [{'id': None, 'error': {'code': 'invalid_params'}}])

    async def test_cli_stdout_protocol_stderr_empty(self):
        code = '''
import logging, os, runpy, sys, traceback, types
class NoisyAuth:
    def __init__(self, *args, **kwargs):
        print('SYNTHETIC_PRIVATE_REASON')
        print('SYNTHETIC_PRIVATE_REASON', file=sys.stderr)
        os.write(1, b'SYNTHETIC_PRIVATE_REASON')
        logging.warning('SYNTHETIC_PRIVATE_REASON')
        try:
            raise RuntimeError('SYNTHETIC_PRIVATE_REASON')
        except RuntimeError:
            traceback.print_exc()
            raise
for name in ('larkx', 'larkx.auth', 'larkx.client', 'larkx.region'):
    sys.modules[name] = types.ModuleType(name)
sys.modules['larkx.auth'].LarkAuth = NoisyAuth
sys.modules['larkx.auth'].QrLogin = NoisyAuth
sys.modules['larkx.client'].LarkClient = NoisyAuth
sys.modules['larkx.region'].get_region = lambda name: name
runpy.run_path(sys.argv[1], run_name='__main__')
'''
        proc = await asyncio.create_subprocess_exec(sys.executable, '-I', '-B', '-c', code,
                     str(Path(__file__).with_name('bridge.py')),
                     stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
                     stderr=asyncio.subprocess.PIPE)
        request = {'id': 2, 'method': 'session.open', 'params': dict(POLICY, credentials=CREDS)}
        data = b'{"id":1,"method":"session.close","params":{}}\n' + json.dumps(request).encode() + b'\n'
        out, err = await asyncio.wait_for(proc.communicate(data), 5)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual([json.loads(line) for line in out.splitlines()],
                         [{'id': 1, 'result': {}}, {'id': 2, 'error': {'code': 'unavailable'}}])
        self.assertEqual(err, b'')
        self.assertNotIn(SECRET.encode(), out)


if __name__ == '__main__':
    unittest.main()
