import asyncio
import importlib.util
import json
from pathlib import Path
import sys
import threading

spec = importlib.util.spec_from_file_location('synthetic_bridge_tests', Path(__file__).with_name('bridge_test.py'))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
lock = threading.Lock()


def emit(value):
    with lock:
        sys.stdout.write(json.dumps(value) + '\n')
        sys.stdout.flush()


async def main():
    fixture.FakeClient.messages = [fixture.message(), fixture.message(),
                                   fixture.message(from_id='sender-denied')]
    companion = fixture.bridge.Bridge(fixture.FakeAuth, fixture.FakeClient,
                                     fixture.FakeQR, lambda name: name, emit)
    reader = asyncio.StreamReader(limit=fixture.bridge.LIMIT)
    await asyncio.get_running_loop().connect_read_pipe(
        lambda: asyncio.StreamReaderProtocol(reader), sys.stdin.buffer)
    await fixture.bridge.serve(companion, reader)


asyncio.run(main())
