"""The plugin protocol's transport: one JSON message a line (Persona's docs/plugins.md, the process plugin protocol: 进程插件协议)."""
import json
import threading
from concurrent.futures import ThreadPoolExecutor


class RpcError(Exception):
    """An answer of the program's that is an error: message is for people and never carries a secret or a body."""

    def __init__(self, message, code=""):
        super().__init__(message)
        self.message = message
        self.code = code


class Peer:
    """Reads requests and answers from reader, writes to writer. A request runs on a thread of its own, so a handler
    can call the hub and wait while the reader goes on."""

    def __init__(self, reader, writer, handle):
        self._reader = reader
        self._writer = writer
        self._handle = handle
        self._write_lock = threading.Lock()
        self._calls_lock = threading.Lock()
        self._calls = {}
        self._next = 0
        self._pool = ThreadPoolExecutor(max_workers=8)

    def serve(self):
        """Runs until the hub closes the stream."""
        for line in self._reader:
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            if msg.get("method"):
                self._pool.submit(self._answer, msg)
            elif msg.get("id") and not msg.get("event"):
                self._resolve(msg)

    def _answer(self, msg):
        out = {"id": msg.get("id")}
        try:
            out["result"] = self._handle(msg["method"], msg.get("params") or {})
        except RpcError as e:
            out["error"] = {"message": e.message}
            if e.code:
                out["error"]["code"] = e.code
        except Exception as e:  # a bug here must not leave the hub waiting; the type alone says nothing private
            out["error"] = {"message": "内部错误（%s）" % type(e).__name__}
        self._write(out)

    def _resolve(self, msg):
        with self._calls_lock:
            call = self._calls.pop(msg["id"], None)
        if call is not None:
            call["answer"] = msg
            call["done"].set()

    def _write(self, msg):
        line = json.dumps(msg, ensure_ascii=False, separators=(",", ":")) + "\n"
        with self._write_lock:
            self._writer.write(line)
            self._writer.flush()

    def call(self, method, params, timeout=30):
        """Asks the hub and waits for its answer."""
        with self._calls_lock:
            self._next += 1
            key = "p%d" % self._next
            call = {"done": threading.Event(), "answer": None}
            self._calls[key] = call
        self._write({"id": key, "method": method, "params": params})
        if not call["done"].wait(timeout):
            with self._calls_lock:
                self._calls.pop(key, None)
            raise RpcError("hub 没有回应 %s" % method, "timeout")
        answer = call["answer"]
        if answer.get("error"):
            raise RpcError(answer["error"].get("message", ""), answer["error"].get("code", ""))
        return answer.get("result") or {}

    def emit(self, event, params):
        """Tells the hub something, without an answer."""
        self._write({"event": event, "params": params})
