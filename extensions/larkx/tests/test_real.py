"""The backend on the real LarkAgentX, with no network and no account: a push in LarkAgentX's own wire format through its
decoders, and credentials restored from the hub's store. It needs what the program downloads on first start, from the
data directory of an installed extension or a run of its own:
  LARKX_UPSTREAM=<its larkagentx-… directory> LARKX_DEPS=<its deps-… directory> python3 tests/test_real.py"""
import os
import sys
import tempfile
import unittest

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
sys.path.insert(0, os.path.join(ROOT, "src"))

UPSTREAM = os.environ.get("LARKX_UPSTREAM", "")
DEPS = os.environ.get("LARKX_DEPS", "")


@unittest.skipUnless(UPSTREAM and DEPS, "needs LARKX_UPSTREAM and LARKX_DEPS")
class RealTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        sys.path[:0] = [UPSTREAM, DEPS]
        cls.home = tempfile.TemporaryDirectory()
        os.environ["LARKX_HOME"] = cls.home.name
        import larkx
        from larkx import media
        from larkx.proto import decoders
        from larkx.proto import proto_pb2 as P
        cls.larkx, cls.media, cls.decoders, cls.P = larkx, media, decoders, P

    @classmethod
    def tearDownClass(cls):
        cls.home.cleanup()

    def frame(self, messages, at_me=()):
        P = self.P
        push = P.PushMessagesRequest()
        for m in messages:
            push.messages[m.id].CopyFrom(m)
        for key in at_me:
            push.messagesAtMe[key] = True
        packet = P.Packet()
        packet.cmd = 6
        packet.payload = push.SerializeToString()
        frame = P.Frame()
        frame.payload = packet.SerializeToString()
        return frame.SerializeToString()

    def message(self, msg_id, msg_type, content, chat_type=2, root=""):
        m = self.P.entities.Message()
        m.id, m.fromId, m.chatId = msg_id, "7001", "6001"
        m.type = msg_type
        m.chatType = chat_type
        m.createTime = 1793000000
        m.position = 42
        m.content = content
        if root:
            m.rootId = root
        return m

    def text(self, value):
        tc = self.P.TextContent()
        tc.text = value
        return tc.SerializeToString()

    def normalized(self, raw):
        from larkx_persona.real import normalize
        return [normalize(m, self.media.extract_resource_key) for m in self.decoders.decode_push_messages(raw)]

    def test_a_text_in_a_group_that_mentions_the_user(self):
        m = self.normalized(self.frame([self.message("9001", 4, self.text("周五开会"))], at_me=["9001"]))[0]
        self.assertEqual(m["id"], "9001")
        self.assertEqual(m["chat"], "6001")
        self.assertEqual(m["chat_kind"], "group")
        self.assertEqual(m["sender"], "7001")
        self.assertEqual(m["position"], 42)
        self.assertEqual(m["type"], "TEXT")
        self.assertEqual(m["text"], "周五开会")
        self.assertEqual(m["at"], 1793000000.0)
        self.assertTrue(m["at_me"])

    def test_a_direct_message(self):
        m = self.normalized(self.frame([self.message("9002", 4, self.text("在吗"), chat_type=1)]))[0]
        self.assertEqual(m["chat_kind"], "dm")
        self.assertFalse(m["at_me"])

    def test_a_topic_reply_keeps_its_root(self):
        m = self.normalized(self.frame([self.message("9003", 4, self.text("好"), root="9000")]))[0]
        self.assertEqual(m["root"], "9000")

    def test_a_picture_has_its_key(self):
        content = self.P.ImageContent()
        content.imageV2.imageKey = "img_key"
        m = self.normalized(self.frame([self.message("9004", 5, content.SerializeToString())]))[0]
        self.assertEqual(m["type"], "IMAGE")
        self.assertEqual(m["media"], [{"kind": "image", "key": "img_key"}])

    def test_credentials_go_through_the_hub_and_never_a_file(self):
        from larkx_persona.real import RealBackend
        saved = []
        backend = RealBackend(ROOT, self.home.name, "feishu")
        backend._lark = self.larkx
        backend.on_credentials(saved.append)
        creds = {"region": "feishu", "cookies": {"session": "s"}, "device_id": "d", "app_key": "a", "user_id": "u", "csrf_token": "c", "web_app_id": "w"}
        backend._auth = backend._hub_auth(creds)
        self.assertEqual(backend._auth.cookies, {"session": "s"})
        self.assertEqual(backend._auth.user_id, "u")
        backend._auth.save()
        self.assertEqual(saved[0]["cookies"], {"session": "s"})
        self.assertEqual(saved[0]["user_id"], "u")
        self.assertFalse(os.path.exists(os.path.join(self.home.name, "credentials.json")))


if __name__ == "__main__":
    unittest.main()
