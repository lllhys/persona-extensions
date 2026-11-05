#!/usr/bin/env python3
import asyncio
import datetime
import json
import logging
import math
import os
from pathlib import Path
import shutil
import sys
import tempfile
import threading


LIMIT = 1024 * 1024
TEXT_LIMIT = 4096
CODES = frozenset(('invalid_params', 'auth_expired', 'unavailable',
                   'unknown_result', 'unknown_method'))
FIELDS = frozenset(('region', 'cookies', 'saved_at', 'device_id', 'app_key',
                    'user_id', 'csrf_token', 'web_app_id'))


class Failure(Exception):
    def __init__(self, code):
        self.code = code if code in CODES else 'unavailable'
        super().__init__(self.code)


def string(value, maximum=4096):
    return isinstance(value, str) and bool(value.strip()) and len(value) <= maximum


def policy(params):
    if params.get('region') != 'feishu':
        raise Failure('invalid_params')
    values = []
    for key in ('allowed_chats', 'allowed_senders'):
        items = params.get(key)
        if (not isinstance(items, list) or not items or len(items) > 10000
                or any(not string(x, 256) or any(c in x for c in '*?[]') for x in items)):
            raise Failure('invalid_params')
        values.append(frozenset(items))
    return tuple(values)


def credentials(value):
    if not isinstance(value, dict) or set(value) - FIELDS:
        raise Failure('invalid_params')
    if value.get('region', 'feishu') != 'feishu':
        raise Failure('invalid_params')
    cookies = value.get('cookies')
    if (not isinstance(cookies, dict) or not cookies
            or any(not string(k, 4096) or not isinstance(v, str)
                   or len(v) > 65536 for k, v in cookies.items())):
        raise Failure('invalid_params')
    for key in FIELDS - {'cookies', 'region', 'saved_at'}:
        if key in value and (not isinstance(value[key], str) or len(value[key]) > 65536):
            raise Failure('invalid_params')
    if 'saved_at' in value:
        v = value['saved_at']
        if isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v) or v < 0:
            raise Failure('invalid_params')
    try:
        data = json.dumps(value, allow_nan=False)
    except (TypeError, ValueError):
        raise Failure('invalid_params') from None
    if len(data.encode('utf-8')) > LIMIT:
        raise Failure('invalid_params')
    return json.loads(data)


def timestamp(value):
    if not isinstance(value, str) or len(value) > 64 or 'T' not in value:
        return None
    try:
        dt = datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
        if dt.tzinfo is not None:
            return dt.astimezone(datetime.timezone.utc).isoformat().replace('+00:00', 'Z')
    except ValueError:
        pass
    return None


def normalize(msg, chats, senders, self_id):
    if not isinstance(msg, dict) or type(msg.get('msg_type')) is not int or msg['msg_type'] != 4:
        return None
    chat, sender = msg.get('chat_id'), msg.get('from_id')
    if (not string(chat, 256) or not string(sender, 256) or chat not in chats
            or sender not in senders or sender == self_id or not string(self_id, 256)):
        return None
    mid, text, anchor = msg.get('msg_id'), msg.get('content'), msg.get('anchor', '')
    if not string(mid, 256) or not string(text, TEXT_LIMIT) or not isinstance(anchor, str) or len(anchor) > 256:
        return None
    result = {'id': mid, 'chat': chat, 'sender': sender, 'text': text, 'anchor': anchor}
    at = timestamp(msg.get('create_time'))
    if at is not None:
        result['at'] = at
    return result


async def bounded(fn, *args, deadline=20, **kwargs):
    loop = asyncio.get_running_loop()
    future = loop.create_future()

    def resolve(value, failed):
        if not future.done():
            if failed:
                future.set_exception(Failure('unavailable'))
            else:
                future.set_result(value)

    def run():
        try:
            value, failed = fn(*args, **kwargs), False
        except BaseException:
            value, failed = None, True
        try:
            loop.call_soon_threadsafe(resolve, value, failed)
        except RuntimeError:
            pass

    threading.Thread(target=run, daemon=True).start()
    try:
        return await asyncio.wait_for(future, deadline)
    except asyncio.TimeoutError:
        raise Failure('unavailable') from None


async def websocket(client, callback):
    owner = asyncio.get_running_loop()
    done = owner.create_future()
    handles = {}

    def complete():
        if not done.done():
            done.set_result(None)

    async def run():
        loop = asyncio.get_running_loop()
        loop.set_exception_handler(lambda *args: None)
        handles['loop'] = loop
        handles['task'] = asyncio.current_task()
        try:
            await client.connect_websocket(callback)
        except BaseException:
            pass

    def worker():
        try:
            asyncio.run(run())
        except BaseException:
            pass
        finally:
            try:
                owner.call_soon_threadsafe(complete)
            except RuntimeError:
                pass

    threading.Thread(target=worker, daemon=True).start()
    try:
        await done
    finally:
        loop, task = handles.get('loop'), handles.get('task')
        if loop is not None and task is not None:
            try:
                loop.call_soon_threadsafe(task.cancel)
            except RuntimeError:
                pass


class Vault:
    def __init__(self, auth_type, data=None):
        self.lock = threading.Lock()
        self.closed = False
        self.directory = Path(tempfile.mkdtemp(prefix='persona-larkx-'))
        os.chmod(self.directory, 0o700)
        self.path = self.directory / 'credentials.json'
        self.auth = None
        try:
            self.write(data if data is not None else {'region': 'feishu', 'cookies': {}})
            self.auth = auth_type(path=self.path)
            self.auth.save = self.save
        except BaseException:
            self.close()
            raise Failure('unavailable') from None

    def write(self, data):
        with self.lock:
            if self.closed:
                raise Failure('unavailable')
            fd = os.open(str(self.path), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            with os.fdopen(fd, 'w', encoding='utf-8') as out:
                json.dump(data, out, ensure_ascii=False, allow_nan=False)

    def save(self):
        auth = self.auth
        data = {'region': 'feishu', 'cookies': auth.cookies}
        for key in FIELDS - {'region', 'cookies'}:
            if hasattr(auth, key):
                data[key] = getattr(auth, key)
        self.write(credentials(data))

    def export(self):
        with self.lock:
            if self.closed:
                raise Failure('unavailable')
            return credentials(json.loads(self.path.read_text(encoding='utf-8')))

    def close(self):
        with self.lock:
            self.closed = True
            if self.auth is not None:
                self.auth.cookies = {}
                self.auth.user_id = ''
            shutil.rmtree(self.directory, ignore_errors=True)


class Bridge:
    def __init__(self, auth_type, client_type, qr_type, get_region, emit, timeout=20, qr_timeout=200):
        self.auth_type, self.client_type = auth_type, client_type
        self.qr_type, self.get_region = qr_type, get_region
        self.emit = emit
        self.timeout, self.qr_timeout = timeout, qr_timeout
        self.vault = self.client = self.ws = self.login = None
        self.chats = self.senders = frozenset()
        self.self_id = ''
        self.state = 'not_signed_in'
        self.requests = set()

    async def call(self, fn, *args, **kwargs):
        return await bounded(fn, *args, deadline=self.timeout, **kwargs)

    async def valid(self, auth):
        auth.user_id = ''
        value = await self.call(auth.validate, fetch_profile=True)
        return (isinstance(value, tuple) and len(value) == 2 and value[0] is True
                and string(getattr(auth, 'user_id', None), 256))

    async def stop_session(self):
        task, self.ws = self.ws, None
        client = self.client
        self.state = 'not_signed_in'
        self.client = None
        self.chats = self.senders = frozenset()
        self.self_id = ''
        self.requests.clear()
        if task is not None:
            task.cancel()
            try:
                await asyncio.wait_for(task, self.timeout)
            except (asyncio.CancelledError, asyncio.TimeoutError, Failure):
                pass
        upstream_loop = getattr(client, 'loop', None)
        if isinstance(upstream_loop, asyncio.AbstractEventLoop):
            try:
                upstream_loop.call_soon_threadsafe(upstream_loop.stop)
            except RuntimeError:
                pass
        if self.vault is not None:
            self.vault.close()
            self.vault = None

    async def stop_login(self):
        login, self.login = self.login, None
        if login is not None:
            login['active'] = False
            task = login.get('task')
            if task is not None:
                task.cancel()
                try:
                    await asyncio.wait_for(task, self.timeout)
                except (asyncio.CancelledError, asyncio.TimeoutError, Failure):
                    pass
            login['vault'].close()
            login['credentials'] = None
            login['qr'] = None

    async def close(self):
        await self.stop_session()
        await self.stop_login()

    async def listen(self, client, vault):
        async def on_message(msg):
            if self.vault is vault and self.state == 'ok':
                # The self ID checked at open; valid() clears auth.user_id while a check runs.
                item = normalize(msg, self.chats, self.senders, self.self_id)
                if item is not None:
                    self.emit({'event': 'message', 'message': item})
        try:
            await websocket(client, on_message)
        except asyncio.CancelledError:
            raise
        except BaseException:
            pass
        if self.vault is vault:
            state = 'error'
            try:
                if not await self.valid(vault.auth):
                    state = 'not_signed_in'
            except Failure:
                pass
            self.state = state
            self.emit({'event': 'status', 'state': state})

    async def login_worker(self, login):
        try:
            result = await bounded(login['qr'].wait, deadline=self.qr_timeout,
                                   timeout=180, interval=2)
            if not login['active']:
                return
            if not isinstance(result, tuple) or len(result) != 2 or result[0] is not True:
                login['state'] = 'expired'
                return
            await self.call(login['qr'].finish, login['vault'].auth)
            if login['active']:
                login['credentials'] = login['vault'].export()
                login['state'] = 'ready'
        except Failure:
            if login['active']:
                login['state'] = 'expired'
        finally:
            login['vault'].close()

    async def dispatch(self, method, params):
        if not isinstance(params, dict):
            raise Failure('invalid_params')
        if method == 'session.open':
            chats, senders = policy(params)
            data = credentials(params.get('credentials'))
            await self.close()
            vault = Vault(self.auth_type, data)
            self.vault = vault
            try:
                if await self.call(vault.auth.load) is not True or not await self.valid(vault.auth):
                    raise Failure('auth_expired')
                client = await self.call(self.client_type, vault.auth)
                self.client, self.chats, self.senders = client, chats, senders
                self.self_id = vault.auth.user_id
                self.state = 'ok'
                self.ws = asyncio.create_task(self.listen(client, vault))
                return {'state': 'ok'}
            except BaseException:
                await self.stop_session()
                raise
        if method == 'session.send':
            chat, text = params.get('chat'), params.get('text')
            anchor, rid = params.get('anchor'), params.get('request_id')
            if (not string(chat, 256) or not string(text, TEXT_LIMIT)
                    or not string(rid, 256) or (anchor is not None and not string(anchor, 256))):
                raise Failure('invalid_params')
            if self.state != 'ok' or self.client is None:
                raise Failure('auth_expired')
            if chat not in self.chats:
                raise Failure('invalid_params')
            if rid in self.requests or len(self.requests) >= 10000:
                raise Failure('unknown_result')
            self.requests.add(rid)
            try:
                mid = await self.call(self.client.send_msg, text, chat, root_id=anchor)
            except Failure:
                self.state = 'error'
                raise Failure('unknown_result') from None
            if not string(mid, 256):
                raise Failure('unknown_result')
            return {'message_id': mid}
        if method == 'session.check':
            if self.vault is None or self.state != 'ok':
                return {'state': 'not_signed_in' if self.state == 'not_signed_in' else 'error'}
            auth = self.vault.auth
            try:
                valid = await self.valid(auth) and auth.user_id == self.self_id
            except Failure:
                self.state = 'error'
                return {'state': 'error'}
            if not valid:
                self.state = 'not_signed_in'
                if self.ws is not None:
                    self.ws.cancel()
            return {'state': self.state}
        if method == 'session.close':
            await self.close()
            return {}
        if method == 'login.begin':
            policy(params)
            await self.close()
            vault = Vault(self.auth_type)
            try:
                region = await self.call(self.get_region, 'feishu')
                qr = await self.call(self.qr_type, region)
                content = qr.qr_content
                if not string(content, LIMIT // 2):
                    raise Failure('unavailable')
                login = {'vault': vault, 'qr': qr, 'active': True,
                         'state': 'pending', 'credentials': None}
                self.login = login
                login['task'] = asyncio.create_task(self.login_worker(login))
                return {'state': 'pending', 'qr': content}
            except BaseException:
                vault.close()
                raise
        if method == 'login.poll':
            if self.login is None:
                return {'state': 'expired'}
            result = {'state': self.login['state']}
            if result['state'] == 'ready':
                result['credentials'] = self.login['credentials']
            return result
        if method == 'login.cancel':
            await self.stop_login()
            return {}
        raise Failure('unknown_method')

    async def request(self, request):
        rid = request.get('id') if isinstance(request, dict) else None
        if type(rid) is not int:
            return {'id': None, 'error': {'code': 'invalid_params'}}
        try:
            method = request.get('method')
            if not isinstance(method, str):
                raise Failure('invalid_params')
            result = await self.dispatch(method, request.get('params', {}))
            return {'id': rid, 'result': result}
        except Failure as error:
            return {'id': rid, 'error': {'code': error.code}}
        except BaseException:
            return {'id': rid, 'error': {'code': 'unavailable'}}


async def serve(bridge, reader):
    try:
        while True:
            raw = await reader.readline()
            if not raw:
                break
            # readline keeps the trailing newline; size the JSON frame itself
            # so that a payload of exactly LIMIT bytes is accepted, matching
            # the Go transport.
            frame = raw[:-1] if raw.endswith(b'\n') else raw
            if len(frame) > LIMIT:
                bridge.emit({'id': None, 'error': {'code': 'invalid_params'}})
                break
            try:
                request = json.loads(frame.decode('utf-8'), parse_constant=lambda _: None)
            except (ValueError, UnicodeError, RecursionError):
                bridge.emit({'id': None, 'error': {'code': 'invalid_params'}})
                continue
            bridge.emit(await bridge.request(request))
    except (ValueError, asyncio.LimitOverrunError):
        bridge.emit({'id': None, 'error': {'code': 'invalid_params'}})
    finally:
        await bridge.close()


def main():
    protocol = os.fdopen(os.dup(sys.stdout.fileno()), 'w', encoding='utf-8', buffering=1)
    null = os.open(os.devnull, os.O_RDWR)
    os.dup2(null, 1)
    os.dup2(null, 2)
    os.close(null)
    sys.stdout = open(os.devnull, 'w')
    sys.stderr = open(os.devnull, 'w')
    logging.disable(sys.maxsize)
    sys.excepthook = lambda *args: None
    threading.excepthook = lambda args: None
    lock = threading.Lock()

    def emit(value):
        with lock:
            protocol.write(json.dumps(value, ensure_ascii=False, allow_nan=False) + '\n')
            protocol.flush()

    async def run():
        loop = asyncio.get_running_loop()
        loop.set_exception_handler(lambda *args: None)
        home = tempfile.TemporaryDirectory(prefix='persona-larkx-home-')
        os.chmod(home.name, 0o700)
        os.environ.update({'LARKX_HOME': home.name, 'LARKX_REGION': 'feishu',
                           'LARKX_AGENT_BACKEND': 'none', 'LARKX_MARK_READ': 'false'})
        os.chdir(home.name)
        try:
            from larkx.auth import LarkAuth, QrLogin
            from larkx.client import LarkClient
            from larkx.region import get_region
        except BaseException:
            class Missing:
                def __init__(self, *args, **kwargs):
                    raise Failure('unavailable')
            LarkAuth = QrLogin = LarkClient = Missing
            get_region = Missing
        bridge = Bridge(LarkAuth, LarkClient, QrLogin, get_region, emit)
        reader = asyncio.StreamReader(limit=LIMIT)
        await loop.connect_read_pipe(lambda: asyncio.StreamReaderProtocol(reader), sys.stdin.buffer)
        try:
            await serve(bridge, reader)
        finally:
            home.cleanup()
    try:
        asyncio.run(run())
    except BaseException:
        pass
    finally:
        protocol.close()


if __name__ == '__main__':
    main()
