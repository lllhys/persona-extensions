"""The connector: every connector.* method of the protocol (docs/connector-protocol.md), over a backend. It knows nothing of
LarkAgentX, so the whole of it runs against a fixture backend in the tests."""
import base64
import hashlib
import json
import threading
import time
from datetime import datetime, timezone

from .wire import RpcError

ID = "larkx"
SECRETS = "connector.secrets."

# A chat is an inbox thing while it moved in the last few days; how much of it the thing carries for triage.
INBOX_DAYS = 3
INBOX_MOST = 200
CONTEXT_LINES = 8
# An archive call reads this many messages at most, whatever the number of chats it asks for.
ARCHIVE_ROWS = 500
THREAD_MOST = 50
THREAD_MEDIA_MOST = 4 << 20
ARCHIVE_MEDIA_MOST = 20 << 20
# Messages the archive has passed are kept this long, then deleted: the knowledge base has them, and a thread page reads
# older ones from the server again. Looked at no more often than every PRUNE_SECONDS.
KEEP_DAYS = 30
PRUNE_SECONDS = 3600
# Catching up after a reconnect: the chats that moved last, and how far back in each.
CATCH_UP_CHATS = 30
CATCH_UP_MESSAGES = 50
# The hub is told at most this often that something moved.
CHANGED_SECONDS = 3
# Trying a saved sign-in again when Feishu cannot be reached; how long sign-in waits for the connection.
RESTORE_RETRY_SECONDS = 30
LINK_WAIT_SECONDS = 5
CHANNEL = "channel."
CANNOT_SEND = "cannot_send"
# /agent: the user's own message starting with it goes to the assistant; the answer comes back in that chat.
AGENT_PREFIX = "/agent"
AGENT_SUFFIX = "🤖 AI 代答，仅供参考"
AGENT_EMPTY = "（只发了 /agent：看上面的上下文和引用的消息，判断要做什么。）"
AGENT_TYPES = ("TEXT", "POST")
AGENT_FRESH_SECONDS = 600
AGENT_CONTEXT_MESSAGES = 20
AGENT_RETRY_SECONDS = [2, 5, 15]
SIGNED_OUT_ERROR = "还没登录飞书"
SIGN_IN_GUIDE = ["在连接器设置里打开「飞书（LarkAgentX）」，用飞书 App 扫码登录。"]
SEND_SCHEMA = {
    "type": "object",
    "properties": {
        "chat": {"type": "string", "description": "The chat's id, from larkx_chats or an inbox item."},
        "text": {"type": "string", "description": "The message, plain text."},
        "reply_to": {"type": "string", "description": "A message's id, to answer in its topic."},
    },
    "required": ["chat", "text"],
}
SEND_DM_SCHEMA = {
    "type": "object",
    "properties": {
        "user": {"type": "string", "description": "The person's id, from larkx_search."},
        "text": {"type": "string", "description": "The message, plain text."},
    },
    "required": ["user", "text"],
}
ACTIONS = [
    {"name": "larkx_send", "input_schema": SEND_SCHEMA,
     "description": "Send a Feishu message to a chat as the user themself, not a bot. The user confirms the full text on the phone first."},
    {"name": "larkx_send_dm", "input_schema": SEND_DM_SCHEMA,
     "description": "Send a Feishu direct message to a person as the user themself, not a bot. The user confirms the full text on the phone first."},
]
TOOLS = [
    {"name": "larkx_status", "description": "Say whether the user's Feishu account is signed in and its messages are being received.",
     "input_schema": {"type": "object", "properties": {}}},
    {"name": "larkx_chats", "description": "List the Feishu chats messages were seen in since sign-in, newest first, with their ids.",
     "input_schema": {"type": "object", "properties": {
         "limit": {"type": "integer", "minimum": 1, "maximum": 50},
         "query": {"type": "string", "description": "Only chats whose name contains this."}}}},
    {"name": "larkx_messages", "description": "Read the latest messages of one Feishu chat, oldest first. For a full-text search use kb_search.",
     "input_schema": {"type": "object", "properties": {
         "chat": {"type": "string"},
         "limit": {"type": "integer", "minimum": 1, "maximum": 50}}, "required": ["chat"]}},
    {"name": "larkx_search", "description": "Find Feishu people and groups by name; gives the ids larkx_send_dm and larkx_messages take.",
     "input_schema": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}},
]


def rfc3339(at):
    return datetime.fromtimestamp(at, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def local_time(at):
    return datetime.fromtimestamp(at).strftime("%m-%d %H:%M")


def media_ref(msg_id, key):
    return "m:%s:%s" % (msg_id, key)


class Connector:
    def __init__(self, backend, store, hub, rules=""):
        self.backend = backend
        # The user's own triage rules (setting triage_rules): handed over with every inbox item, the hub uses them
        # instead of its global ones. The hub reads at most 4000 characters.
        self.rules = (rules or "").strip()[:4000]
        self.store = store
        self.hub = hub
        self._ready = threading.Event()
        self._boot_error = ""
        self._lock = threading.Lock()
        # Serialize backend authentication changes; logout also cancels startup restoration before taking this lock.
        self._session_lock = threading.RLock()
        self._restore_cancel = threading.Event()
        self._account_id = ""
        self._account = ""
        self._signed_in = False
        self._expired = False
        self._link_up = False
        self._linked = threading.Event()
        self._unreachable = False
        self._last_event = 0.0
        self._last_sync = 0.0
        self._last_prune = 0.0
        self._stop = threading.Event()
        self._dirty = threading.Event()
        self.methods = {
            "status": self.status, "tools.list": self.tools_list, "tools.call": self.tools_call,
            "actions.list": self.actions_list, "actions.describe": self.actions_describe, "actions.do": self.actions_do,
            "inbox.collect": self.inbox_collect, "inbox.reply": self.inbox_reply,
            "thread.page": self.thread_page, "thread.media": self.thread_media,
            "archive.changes": self.archive_changes, "archive.media": self.archive_media,
            "login.step": self.login_step, "logout": self.logout,
        }
        self.channel_methods = {"send": self.channel_send, "typing": self.channel_typing}

    def start(self):
        """Gets the program going behind the handshake: the hub does not wait for the dependencies or the sign-in."""
        threading.Thread(target=self._boot, daemon=True).start()
        threading.Thread(target=self._announce, daemon=True).start()

    def _boot(self):
        try:
            self.backend.prepare()
        except Exception as e:
            self._boot_error = str(e) or type(e).__name__
            self._ready.set()
            return
        with self._session_lock:
            if self._restore_cancel.is_set():
                self._ready.set()
                return
            self.backend.on_credentials(self._save_credentials)
            try:
                saved = self._load_credentials()
            except Exception as e:
                self._log("reading the sign-in failed: %s" % type(e).__name__)
                saved = None
        while saved and not self._restore_cancel.is_set():
            try:
                with self._session_lock:
                    if self._restore_cancel.is_set():
                        break
                    account = self.backend.restore(saved)
                    # A logout can cancel an in-flight restore while waiting for the backend operation to finish.
                    if self._restore_cancel.is_set():
                        self.backend.logout()
                        break
                    self._begin(account)
                break
            except self.backend.Expired:
                if not self._restore_cancel.is_set():
                    self._expired = True
                break
            except Exception as e:
                if self._restore_cancel.is_set():
                    break
                # Feishu cannot be reached, which says nothing about the session: keep it and try again.
                self._log("restoring the sign-in failed: %s" % type(e).__name__)
                self._unreachable = True
                self._ready.set()
                self._restore_cancel.wait(RESTORE_RETRY_SECONDS)
        self._unreachable = False
        self._ready.set()

    def _load_credentials(self):
        data = self.hub.call(SECRETS + "load", {"contribution": ID}).get("data")
        return json.loads(base64.b64decode(data)) if data else None

    def _save_credentials(self, saved):
        data = base64.b64encode(json.dumps(saved).encode()).decode()
        self.hub.call(SECRETS + "save", {"contribution": ID, "data": data})

    def _begin(self, account):
        """Signed in as account: starts listening."""
        with self._lock:
            self._account_id = account["id"]
            self._account = account["name"]
            self._signed_in = True
            self._expired = False
        self._stop.set()
        self._stop = threading.Event()
        self._linked.clear()
        self._dirty.set()
        threading.Thread(target=self._listen, args=(self._stop,), daemon=True).start()
        self._linked.wait(LINK_WAIT_SECONDS)

    def _listen(self, stop):
        self.backend.listen(self._on_live, self._on_link, stop)

    def _on_live(self, m):
        """A message pushed on the long connection: kept like any other, and the user's own /agent goes to the assistant.
        Fetched history never does (catch_up calls on_message), so a restart answers nothing twice."""
        if self.on_message(m):
            self._maybe_agent(m)

    def _on_link(self, state):
        """The long connection went up, went down, or the session is over."""
        with self._lock:
            self._link_up = state == "up"
            if state == "expired":
                self._signed_in = False
                self._expired = True
        self._linked.set()
        if state == "up":
            threading.Thread(target=self.catch_up, daemon=True).start()

    def catch_up(self):
        """Fetches what came in while the connection was down, for the chats that moved last."""
        for chat in self.store.chats(CATCH_UP_CHATS):
            try:
                newest = self.backend.chat_info(chat["id"]).get("last_position") or 0
                known = self.store.newest_position(chat["id"])
                if known and newest > known:
                    first = max(known + 1, newest - CATCH_UP_MESSAGES + 1)
                    for m in self.backend.history(chat["id"], list(range(first, newest + 1))):
                        self.on_message(m)
            except Exception as e:
                self._log("catching up failed: %s" % type(e).__name__)

    def on_message(self, m):
        """One message, live or fetched. True when it is new."""
        mine = bool(self._account_id) and m.get("sender") == self._account_id
        name = "" if m.get("type") == "SYSTEM" else self._sender_name(m, mine)
        self.store.touch_chat(m["chat"], m.get("chat_kind") or "group", m.get("at") or 0)
        if not self.store.add_message(m, name, mine):
            return False
        self._name_chat(m, name, mine)
        with self._lock:
            self._last_event = time.time()
        self._dirty.set()
        return True

    # /agent: the channel (docs/extensions/channels.md "Contributing a channel" in Persona)

    def _maybe_agent(self, m):
        """Hands the user's own fresh "/agent …" to the hub, which puts it in the assistant's chat and answers through
        channel.send. Someone else's /agent, an old one, and our own answers never count."""
        with self._lock:
            me = self._account_id
        if not me or m.get("sender") != me or m.get("type") not in AGENT_TYPES:
            return
        if time.time() - (m.get("at") or 0) > AGENT_FRESH_SECONDS:
            return
        text = m.get("text") or ""
        if text.rstrip().endswith(AGENT_SUFFIX):
            return
        request = agent_request(text)
        if request is None:
            return
        threading.Thread(target=self._hand_over, args=(m, request), daemon=True).start()

    def _hand_over(self, m, request):
        message = {"id": m["id"], "text": request or AGENT_EMPTY}
        context = self._agent_context(m)
        if context:
            message["forward"] = context
        parent = self.store.message(m.get("parent") or "") if m.get("parent") else None
        if parent:
            message["quote"] = _line(parent)
        for wait in AGENT_RETRY_SECONDS + [None]:
            try:
                # The hub keeps each message id once: handing it over again after a cut-off call answers it once.
                self.hub.call(CHANNEL + "receive", {"contribution": ID, "message": message})
                return
            except Exception as e:
                if wait is None:
                    self._log("handing /agent to the hub failed: %s" % type(e).__name__)
                    return
                time.sleep(wait)

    def _agent_context(self, m):
        """Where the request was written and what was said there just before it, for the assistant."""
        chat = self.store.chat(m["chat"]) or {}
        before = [x for x in self.store.messages(m["chat"], m.get("position") or 0, AGENT_CONTEXT_MESSAGES) if x["id"] != m["id"]]
        where = "飞书%s「%s」" % (_kind_label(chat.get("kind") or "group"), chat.get("name") or "未命名")
        if not before:
            return "这条 /agent 写在%s里。" % where
        return "这条 /agent 写在%s里，之前的 %d 条消息：\n%s" % (where, len(before), "\n".join(_line(x) for x in before))

    def channel_send(self, p):
        """An answer for one /agent: in the chat it was written in, under it, as the user, marked as written by AI.
        A ref is sent once; a send whose result is unknown is never tried again."""
        ref = p.get("ref") or ""
        if not ref:
            raise RpcError("channel.send without a ref", "invalid_params")
        with self._lock:
            signed_in, me = self._signed_in, self._account_id
        request = self.store.message(p.get("reply_to") or "") if p.get("reply_to") else None
        if not signed_in or request is None or request["sender"] != me or agent_request(request["text"]) is None or request["text"].rstrip().endswith(AGENT_SUFFIX):
            # Only an answer to one of the user's own /agent goes out: nothing in a chat they did not ask in.
            raise RpcError("没有可以回复的 /agent 消息", CANNOT_SEND)
        key = "channel:" + ref
        claimed, result = self.store.claim_send(key)
        if not claimed:
            if result:
                return {"message_id": result}
            raise RpcError(_UNKNOWN_SEND)
        text = (p.get("text") or "").rstrip() + "\n\n" + AGENT_SUFFIX
        try:
            msg_id = self.backend.send(request["chat"], text, request.get("root") or request["id"])
        except Exception:
            msg_id = None
        if not msg_id:
            raise RpcError(_UNKNOWN_SEND)
        self.store.finish_send(key, str(msg_id))
        return {"message_id": str(msg_id)}

    def channel_typing(self, _):
        return {}

    def _sender_name(self, m, mine):
        if mine:
            return "我"
        sender = m.get("sender") or ""
        known = self.store.name(sender)
        if known:
            return known
        try:
            found = self.backend.user_name(sender, m["chat"])
        except Exception:
            found = None
        if found:
            self.store.set_name(sender, found)
            return found
        return UNKNOWN_SENDER

    def _name_chat(self, m, sender_name, mine):
        chat = self.store.chat(m["chat"])
        if chat is None or chat["name"]:
            return
        if chat["kind"] == "dm" and not mine and sender_name and sender_name != UNKNOWN_SENDER:
            self.store.name_chat(m["chat"], sender_name)
            return
        try:
            name = self.backend.chat_info(m["chat"]).get("name")
        except Exception:
            name = ""
        # A direct chat seen only through the user's own messages (the chat with oneself among them) keeps no name: a
        # placeholder would stay for good, and a confirmation must not name a recipient it does not know.
        if name or chat["kind"] != "dm":
            self.store.name_chat(m["chat"], name or "飞书群")

    def _announce(self):
        """Tells the hub something moved, at most every CHANGED_SECONDS."""
        while True:
            self._dirty.wait()
            self._dirty.clear()
            for scope in ("inbox", "chats"):
                try:
                    self.hub.emit("connector.changed", {"contribution": ID, "scope": scope})
                except Exception:
                    return
            time.sleep(CHANGED_SECONDS)

    def _log(self, line):
        self.hub.log(line)

    # status

    def status(self, _):
        with self._lock:
            if self._boot_error:
                return {"state": "error", "error": "运行环境没装好：" + self._boot_error[:200],
                        "guide": ["hub 所在的机器要有 Python 3.9 以上，能运行 python3 -m pip，并能访问 PyPI（pypi.org、files.pythonhosted.org）"
                                  "和 GitHub（codeload.github.com）：第一次启动要从这几处下载 LarkAgentX 和它的依赖。"]}
            if not self._ready.is_set():
                return {"state": "error", "error": "正在准备运行环境，第一次要一两分钟。"}
            if self._unreachable:
                return {"state": "error", "error": "连不上飞书，正在重试。"}
            if not self._signed_in:
                error = "飞书登录失效了" if self._expired else SIGNED_OUT_ERROR
                return {"state": "not_signed_in", "error": error, "guide": SIGN_IN_GUIDE}
            out = {"state": "ok", "account": self._account, "via": "LarkAgentX 长连接"}
            if not self._link_up:
                out["guide"] = ["长连接断了，正在重连。"]
            if self._last_sync:
                out["last_sync"] = rfc3339(self._last_sync)
            if self._last_event:
                out["last_event"] = rfc3339(self._last_event)
            return out

    # tools

    def tools_list(self, _):
        return {"tools": TOOLS}

    def tools_call(self, p):
        name, args = p.get("name"), p.get("args") or {}
        if name == "larkx_status":
            s = self.status({})
            return {"text": "%s: %s" % (s.get("account") or "飞书", "在收消息" if s["state"] == "ok" else s.get("error", s["state"]))}
        if name == "larkx_chats":
            rows = self.store.chats(_limit(args, 20), args.get("query") or "")
            lines = ["%s  %s〔%s〕 %s" % (r["id"], _title(r), _kind_label(r["kind"]), local_time(r["last_at"])) for r in rows]
            return {"text": "\n".join(lines) or "还没有收到过消息。"}
        if name == "larkx_messages":
            chat = _required(args, "chat")
            rows = self.store.messages(chat, 0, _limit(args, 20))
            return {"text": "\n".join(_line(m) for m in rows) or "这个会话还没有消息。"}
        if name == "larkx_search":
            self._need_signed_in()
            found = self.backend.search(_required(args, "query"))
            return {"text": "\n".join("%s  %s〔%s〕" % (r["id"], r["title"], r["type"]) for r in found) or "没有找到。"}
        raise RpcError("没有这个工具：%s" % name)

    def _need_signed_in(self):
        if not self._signed_in:
            raise RpcError(SIGNED_OUT_ERROR)

    # actions: only actions.do writes, and only after the hub's gate

    def actions_list(self, _):
        return {"tools": ACTIONS}

    def actions_describe(self, p):
        name, args = p.get("name"), p.get("args") or {}
        text = _required(args, "text")
        quoted = ""
        if name == "larkx_send":
            chat = _required(args, "chat")
            row = self.store.chat(chat) or {}
            kind = row.get("kind") or "group"
            title = row.get("name") or (DM_UNKNOWN if kind == "dm" and row else chat)
            if kind == "dm" and not row.get("name"):
                where = "这个私聊里的对方（还不知道是谁）"
            else:
                where = "『%s』一个人" % title if kind == "dm" else "群『%s』里的所有人" % title
            target = "%s〔%s〕" % (title, _kind_label(kind))
            if args.get("reply_to"):
                quoted = self._quoted(args["reply_to"])
        elif name == "larkx_send_dm":
            user = _required(args, "user")
            title = self.store.name(user) or user
            where = "『%s』一个人" % title
            target = "%s〔私聊〕" % title
        else:
            raise RpcError("没有这个操作：%s" % name)
        summary = "以你本人的身份发飞书消息\n发给：%s\n%s内容（全文）：\n%s" % (target, quoted, text)
        return {"action": {"name": name, "visible": where}, "summary": summary}

    def _quoted(self, reply_to):
        """The confirmation's line for reply_to: the reply goes into that message's topic, so it says which one."""
        m = self.store.message(reply_to)
        if m is None:
            return "回复：话题里的一条消息（这里没有它的内容）\n"
        text = (m["text"] or "[消息]").replace("\n", " ")
        if len(text) > 200:
            text = text[:200] + "…"
        return "回复：%s 的消息「%s」（发在这条消息的话题里）\n" % (m["sender_name"] or "系统", text)

    def actions_do(self, p):
        name, args = p.get("name"), p.get("args") or {}
        # Each request's send runs at most once, keyed on its id: without one, every later send would read as that one.
        request_id = _required(p, "request_id")
        if name == "larkx_send":
            return {"text": self._send_once("action:" + request_id, _required(args, "chat"), _required(args, "text"), args.get("reply_to"))}
        if name == "larkx_send_dm":
            self._need_signed_in()
            user, text = _required(args, "user"), _required(args, "text")
            done = self._claim("action:" + request_id)
            if done:
                return {"text": done}
            return {"text": self._send("action:" + request_id, self.backend.create_chat(user), text, None)}
        raise RpcError("没有这个操作：%s" % name)

    def _send_once(self, key, chat, text, reply_to):
        self._need_signed_in()
        return self._claim(key) or self._send(key, chat, text, reply_to)

    def _claim(self, key):
        """Takes key for one send: "" when it is this one's to do, else what the first one ended with."""
        claimed, result = self.store.claim_send(key)
        if claimed:
            return ""
        if result:
            return result
        raise RpcError(_UNKNOWN_SEND)

    def _send(self, key, chat, text, reply_to):
        root = None
        if reply_to:
            quoted = self.store.message(reply_to) or {}
            root = quoted.get("root") or reply_to
        try:
            msg_id = self.backend.send(chat, text, root)
        except Exception:
            msg_id = None
        if not msg_id:
            # Left without a result on purpose: this key is never tried again.
            raise RpcError(_UNKNOWN_SEND)
        result = "已发出（%s）。" % msg_id
        self.store.finish_send(key, result)
        return result

    # inbox

    def inbox_collect(self, _):
        since = time.time() - INBOX_DAYS * 86400
        things = []
        for chat in self.store.chats_since(since, INBOX_MOST):
            thing = self._thing(chat)
            if thing:
                things.append(thing)
        return {"things": things}

    def _thing(self, chat):
        recent = self.store.messages(chat["id"], 0, CONTEXT_LINES)
        recent = [m for m in recent if m["type"] != "SYSTEM"]
        if not recent:
            return None
        last = recent[-1]
        dm = chat["kind"] == "dm"
        unanswered = []
        for m in reversed(recent):
            if m["mine"]:
                break
            unanswered.append(m)
        mentioned = any(m["at_me"] for m in unanswered)
        kind = "dm" if dm else ("mention" if mentioned else "group")
        if last["mine"]:
            return self._ruled({"thread": chat["id"], "kind": kind, "done": "replied", "why": "你后来回复了"}, "fyi")
        title = _title(chat)
        thing = {
            "thread": chat["id"], "kind": kind, "title": title, "sender": last["sender_name"],
            "label": {"dm": "私聊", "mention": "群里 @ 了你", "group": "群消息"}[kind],
            "preview": last["text"][:200], "context": "\n".join(_line(m) for m in recent),
            "at": rfc3339(last["at"]), "last": str(last["position"] or last["id"]),
            "can_reply": True,
            "reply_to": ("回复会以你本人的身份发给『%s』" % title) if dm else ("回复会以你本人的身份发到群『%s』，群里所有人都能看到" % title),
            "tags": [{"key": "chat", "value": chat["id"], "name": title}],
        }
        if last["sender"]:
            thing["tags"].append({"key": "sender", "value": last["sender"], "name": last["sender_name"]})
        if kind == "group":
            # Every group message comes in here: folded by rule, so a busy group costs no model call.
            thing["tier"], thing["why"] = "noise", "规则：群消息，没有 @ 你"
        # The fallback tier (capability fallback): used only when the triage model can't be called.
        return self._ruled(thing, "noise" if kind == "group" else "action")

    def _ruled(self, thing, rule_tier):
        thing.setdefault("extra", {})["rule_tier"] = rule_tier
        if self.rules:
            thing["rules"] = self.rules
        return thing

    def inbox_reply(self, p):
        thread, text = _required(p, "thread"), _required(p, "text")
        key = p.get("client_id") or hashlib.sha256(("%s|%s|%s" % (thread, p.get("last"), text)).encode()).hexdigest()
        self._send_once("reply:" + key, thread, text, None)
        return {}

    # threads

    def thread_page(self, p):
        thread = _required(p, "thread")
        limit = min(int(p.get("limit") or 20), THREAD_MOST)
        before = int(p.get("before") or 0)
        chat = self.store.chat(thread)
        if chat is None:
            raise RpcError("没有这个会话")
        rows = self.store.messages(thread, before, limit)
        if len(rows) < limit and self._signed_in:
            rows = self._fetch_older(thread, before, limit, rows)
        out = {
            "chat": {"id": thread, "name": _title(chat), "kind": chat["kind"], "reply": "larkx_send"},
            "messages": [self._thread_message(m) for m in rows],
        }
        if rows and rows[0]["position"] > 1:
            out["before"] = str(rows[0]["position"])
        return out

    def _fetch_older(self, thread, before, limit, rows):
        """Fills a page from the server when this program has not seen that far back."""
        end = rows[0]["position"] if rows else before
        if not end:
            # Nothing kept (older messages are pruned): the newest page, up to the chat's last message.
            try:
                end = (self.backend.chat_info(thread).get("last_position") or 0) + 1
            except Exception as e:
                self._log("reading the chat failed: %s" % type(e).__name__)
                return rows
        if end <= 1:
            return rows
        first = max(1, end - (limit - len(rows)))
        try:
            for m in self.backend.history(thread, list(range(first, end))):
                self.on_message(m)
        except Exception as e:
            self._log("reading older messages failed: %s" % type(e).__name__)
            return rows
        return self.store.messages(thread, before, limit)

    def _thread_message(self, m):
        if m["type"] == "SYSTEM":
            return {"id": m["id"], "at": rfc3339(m["at"]), "sender": {"name": ""}, "event": True, "text": m["text"] or "系统消息"}
        out = {"id": m["id"], "at": rfc3339(m["at"]), "sender": {"id": m["sender"], "name": m["sender_name"]}}
        if m["mine"]:
            out["mine"] = True
        media = [_thread_media(m["id"], x) for x in m["media"]]
        if media:
            out["media"] = media
        if m["text"] and not (media and m["text"].startswith("[")):
            out["text"] = m["text"]
        elif not media:
            out["text"] = "[消息]"
        if m["parent"]:
            quoted = self.store.message(m["parent"])
            if quoted:
                out["reply_to"] = {"id": quoted["id"], "from": quoted["sender_name"], "text": quoted["text"][:200]}
        return out

    def thread_media(self, p):
        return self._media(_required(p, "ref"), THREAD_MEDIA_MOST)

    def archive_media(self, p):
        return self._media(_required(p, "ref"), ARCHIVE_MEDIA_MOST)

    def _media(self, ref, most):
        self._need_signed_in()
        parts = ref.split(":", 2)
        if len(parts) != 3 or parts[0] != "m":
            raise RpcError("没有这个附件")
        m = self.store.message(parts[1])
        if m is None:
            raise RpcError("没有这个附件")
        mime, data = self.backend.download(m["id"], m["chat"], parts[2], most)
        return {"mime": mime, "data": base64.b64encode(data).decode()}

    # archive

    def archive_changes(self, p):
        cursor = p.get("cursor") or ""
        limit = int(p.get("limit") or 20)
        seq = int(cursor) if cursor else 0
        self._prune(seq)
        rows = self.store.after(seq, ARCHIVE_ROWS)
        batches, order, more = {}, [], len(rows) == ARCHIVE_ROWS
        for m in rows:
            if m["chat"] not in batches:
                if len(batches) == limit:
                    more = True
                    break
                batches[m["chat"]] = []
                order.append(m["chat"])
            seq = m["seq"]
            if m["type"] != "SYSTEM" and m["at"] and (m["text"] or m["media"]):
                batches[m["chat"]].append(m)
        with self._lock:
            self._last_sync = time.time()
        return {"batches": [self._batch(c, batches[c]) for c in order if batches[c]], "next": str(seq) if seq else "", "more": more}

    def _prune(self, archived):
        """Deletes messages older than KEEP_DAYS that the archive has passed (row archived and before): a cursor the
        hub hands back is one whose rows it has kept."""
        now = time.time()
        if not archived or now - self._last_prune < PRUNE_SECONDS:
            return
        self._last_prune = now
        try:
            gone = self.store.prune(now - KEEP_DAYS * 86400, archived)
        except Exception as e:
            self._log("pruning old messages failed: %s" % type(e).__name__)
            return
        if gone:
            self._log("pruned %d archived messages older than %d days" % (gone, KEEP_DAYS))

    def _batch(self, chat, rows):
        row = self.store.chat(chat) or {}
        rows = sorted(rows, key=lambda m: (m["at"], m["position"]))
        return {
            "chat": {"id": chat, "name": _title(row), "kind": row.get("kind") or "group", "updated_at": rfc3339(rows[-1]["at"])},
            "messages": [_archive_message(m) for m in rows],
        }

    # login

    def login_step(self, p):
        if not self._ready.wait(180):
            raise RpcError("运行环境还没准备好，等一两分钟再试。")
        if self._boot_error:
            raise RpcError("运行环境没装好：" + self._boot_error[:200])
        # An explicit QR sign-in supersedes the saved startup session, even while its restore is retrying.
        self._restore_cancel.set()
        with self._session_lock:
            return self._login_step(p)

    def _login_step(self, p):
        if not p.get("step"):
            return self._new_qr()
        try:
            state = self.backend.login_poll()
        except Exception:
            # One look that did not get through: the app asks again in a moment.
            state = "waiting"
        if state == "waiting":
            return self._qr_prompt(self.backend.login_qr())
        if state == "expired":
            return self._new_qr()
        try:
            account, saved = self.backend.login_finish()
        except self.backend.Expired:
            return self._new_qr("没有登录成功，重新扫一次。")
        self.backend.on_credentials(self._save_credentials)
        self._save_credentials(saved)
        self._begin(account)
        return {"done": True, "account": account["name"]}

    def _new_qr(self, error=""):
        try:
            return self._qr_prompt(self.backend.login_start(), error)
        except Exception as e:
            self._log("starting the sign-in failed: %s" % type(e).__name__)
            raise RpcError("连不上飞书，等一下再试。")

    def _qr_prompt(self, content, error=""):
        prompt = {"step": "scan", "kind": "qr", "url": content, "interval": 2,
                  "hint": "用飞书 App 扫码，在手机上确认登录。这是一个新的网页版登录，能在飞书的登录设备里看到和下线。"}
        if error:
            prompt["error"] = error
        return prompt

    def logout(self, _):
        self._restore_cancel.set()
        with self._session_lock:
            self._stop.set()
            self.backend.logout()
            self.hub.call(SECRETS + "delete", {"contribution": ID})
            with self._lock:
                self._signed_in = False
                self._expired = False
                self._link_up = False
                self._account = ""
                self._account_id = ""
        return {}


_UNKNOWN_SEND = "这条消息可能已经发出，没法确认，不要重发，先去飞书里看一下。"
UNKNOWN_SENDER = "飞书用户"
# A direct chat whose other person is not known yet: only the user's own messages were seen in it.
DM_UNKNOWN = "私聊（还不知道对方是谁）"


def _title(chat):
    """A chat's name for people: a direct chat without one says its other person is not known."""
    if chat.get("name"):
        return chat["name"]
    return DM_UNKNOWN if chat.get("kind") == "dm" else "飞书会话"


def _required(args, name):
    value = args.get(name)
    if not isinstance(value, str) or not value.strip():
        raise RpcError("缺少 %s" % name)
    return value


def _limit(args, default):
    try:
        return max(1, min(int(args.get("limit") or default), THREAD_MOST))
    except (TypeError, ValueError):
        return default


def agent_request(text):
    """What the user asks with "/agent …" (case aside, then a space, a colon, a line or nothing), or None when the text
    is not one: "/agents" is not."""
    s = (text or "").lstrip()
    if s[:len(AGENT_PREFIX)].lower() != AGENT_PREFIX:
        return None
    rest = s[len(AGENT_PREFIX):]
    if rest and not (rest[0].isspace() or rest[0] in ":："):
        return None
    return rest.lstrip(" \t\r\n:：").strip()


def _kind_label(kind):
    return "私聊" if kind == "dm" else "群"


def _line(m):
    return "%s（%s）：%s" % (m["sender_name"] or "系统", local_time(m["at"]), m["text"])


def _thread_media(msg_id, media):
    out = {"kind": media.get("kind") or "file"}
    if media.get("key"):
        out["ref"] = media_ref(msg_id, media["key"])
    if media.get("name"):
        out["name"] = media["name"]
    return out


def _archive_message(m):
    out = {"id": m["id"], "position": str(m["position"]), "sender_id": m["sender"], "sender_name": m["sender_name"],
           "type": m["type"], "text": m["text"], "at": rfc3339(m["at"])}
    if m["mine"]:
        out["mine"] = True
    elif m["sender"]:
        out["sender_handles"] = [{"kind": "lark", "value": m["sender"]}]
    refs = [media_ref(m["id"], x["key"]) for x in m["media"] if x.get("key")]
    if refs:
        out["media"] = refs
    return out
