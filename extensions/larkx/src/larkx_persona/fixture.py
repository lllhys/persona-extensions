"""A backend with made-up data and no network, for the conformance test: no real account, name or address."""
import time

ME = "u_me"


class Expired(Exception):
    pass


def _message(msg_id, chat, kind, position, sender, text, at_me=False, msg_type="TEXT", media=None):
    return {"id": msg_id, "chat": chat, "chat_kind": kind, "position": position, "sender": sender, "type": msg_type,
            "text": text, "at": time.time() - 3600 + position, "root": "", "parent": "", "at_me": at_me, "media": media or []}


class FixtureBackend:
    Expired = Expired

    def __init__(self):
        self._polls = 0
        self.sent = []

    def prepare(self):
        pass

    def restore(self, saved):
        return {"id": ME, "name": "示例账号"}

    def on_credentials(self, save):
        pass

    def login_start(self):
        self._polls = 0
        return '{"qrlogin":{"token":"example"}}'

    def login_qr(self):
        return '{"qrlogin":{"token":"example"}}'

    def login_poll(self):
        self._polls += 1
        return "done" if self._polls >= 2 else "waiting"

    def login_finish(self):
        return {"id": ME, "name": "示例账号"}, {"cookies": {"session": "example"}}

    def logout(self):
        pass

    def listen(self, on_message, on_link, stop):
        for m in self._messages():
            on_message(m)
        on_link("up")
        stop.wait()

    def _messages(self):
        return [
            _message("m1", "c_dm", "dm", 1, "u_a", "早"),
            _message("m2", "c_dm", "dm", 2, ME, "早上好"),
            _message("m3", "c_dm", "dm", 3, "u_a", "[图片]", msg_type="IMAGE", media=[{"kind": "image", "key": "img_1"}]),
            _message("m4", "c_dm", "dm", 4, "u_a", "在吗"),
            _message("m5", "c_group", "group", 7, "u_b", "@示例账号 周五开会", at_me=True),
            _message("m6", "c_noisy", "group", 9, "u_b", "大家好"),
            _message("m7", "c_done", "dm", 3, "u_b", "收到了吗"),
            _message("m8", "c_done", "dm", 4, ME, "收到"),
        ]

    def send(self, chat, text, root):
        self.sent.append((chat, text, root))
        return "m_sent_%d" % len(self.sent)

    def create_chat(self, user):
        return "c_" + user

    def search(self, query):
        return [{"type": "user", "id": "u_a", "title": "示例联系人"}]

    def history(self, chat, positions):
        return []

    def chat_info(self, chat):
        return {"name": {"c_group": "示例群", "c_noisy": "示例大群"}.get(chat, "示例联系人"), "last_position": 0}

    def user_name(self, user, chat):
        return {"u_a": "示例联系人", "u_b": "示例同事"}.get(user)

    def download(self, msg_id, chat, key, most):
        return "image/png", b"\x89PNG\r\n\x1a\n"
