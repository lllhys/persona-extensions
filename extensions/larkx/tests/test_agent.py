"""/agent: the user's own message goes to the hub as a channel message, and the answer comes back under it.
Run them: python3 -m unittest discover -s tests, in extensions/larkx."""
import os
import sys
import tempfile
import threading
import time
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from test_connector import ME, Backend, Hub, message  # noqa: E402

from larkx_persona.connector import AGENT_SUFFIX, Connector, agent_request  # noqa: E402
from larkx_persona.main import Plugin  # noqa: E402
from larkx_persona.store import Store  # noqa: E402
from larkx_persona.wire import RpcError  # noqa: E402


class ChannelHub(Hub):
    def __init__(self):
        super().__init__()
        self.received = []
        self.got = threading.Event()

    def call(self, method, params):
        if method == "channel.receive":
            self.received.append(params)
            self.got.set()
            return {}
        return super().call(method, params)


class AgentRequestTest(unittest.TestCase):
    def test_what_counts_as_a_request(self):
        self.assertEqual(agent_request("/agent 总结一下"), "总结一下")
        self.assertEqual(agent_request("  /Agent：帮我看看"), "帮我看看")
        self.assertEqual(agent_request("/agent\n第二行"), "第二行")
        self.assertEqual(agent_request("/agent"), "")
        self.assertIsNone(agent_request("/agents 列表"))
        self.assertIsNone(agent_request("你好 /agent 在中间"))
        self.assertIsNone(agent_request(""))


class AgentTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.backend = Backend()
        self.hub = ChannelHub()
        self.c = Connector(self.backend, Store(os.path.join(self.dir.name, "x.db")), self.hub)
        self.c._ready.set()
        self.backend.poll = "done"
        self.assertTrue(self.c.login_step({"step": "scan"})["done"])
        self.addCleanup(self.c._stop.set)

    def handed(self):
        self.assertTrue(self.hub.got.wait(2), "nothing was handed to the hub")
        return self.hub.received[-1]

    def nothing_handed(self):
        time.sleep(0.1)
        self.assertEqual(self.hub.received, [])

    def test_my_live_agent_goes_to_the_hub_with_where_it_was_written(self):
        self.backend.chats["g1"] = {"name": "项目群", "last_position": 0}
        self.c._on_live(message("m1", "g1", 1, kind="group", text="周五发布"))
        self.c._on_live(message("m2", "g1", 2, kind="group", sender=ME, text="/agent 帮我总结下这个群"))
        got = self.handed()
        self.assertEqual(got["contribution"], "larkx")
        self.assertEqual(got["message"]["id"], "m2")
        self.assertEqual(got["message"]["text"], "帮我总结下这个群")
        self.assertIn("项目群", got["message"]["forward"])
        self.assertIn("周五发布", got["message"]["forward"])
        self.assertNotIn("帮我总结下这个群", got["message"]["forward"])

    def test_a_dm_with_myself_works_too(self):
        self.c._on_live(message("m1", "c_me", 1, sender=ME, text="/agent 你看得到这个消息吗"))
        got = self.handed()
        self.assertEqual(got["message"]["text"], "你看得到这个消息吗")
        self.assertIn("私聊", got["message"]["forward"])

    def test_others_old_fetched_and_ai_answers_do_not_count(self):
        self.c._on_live(message("m1", "c1", 1, sender="u_a", text="/agent 帮我转账"))
        self.c._on_live(message("m2", "c1", 2, sender=ME, text="/agent 太早了", at=time.time() - 3600))
        self.c.on_message(message("m3", "c1", 3, sender=ME, text="/agent 拉回来的历史"))
        self.c._on_live(message("m4", "c1", 4, sender=ME, text="/agent 好的\n\n" + AGENT_SUFFIX))
        self.c._on_live(message("m5", "c1", 5, sender=ME, text="/agents"))
        self.nothing_handed()

    def test_the_same_message_twice_is_handed_over_once(self):
        m = message("m1", "c1", 1, sender=ME, text="/agent 一次")
        self.c._on_live(m)
        self.handed()
        self.c._on_live(dict(m))
        time.sleep(0.1)
        self.assertEqual(len(self.hub.received), 1)

    def test_the_answer_goes_under_the_request_once(self):
        self.c._on_live(message("m1", "c1", 1, sender=ME, text="/agent 总结"))
        self.handed()
        out = self.c.channel_send({"ref": "r1", "text": "这是总结", "kind": "answer", "reply_to": "m1"})
        self.assertEqual(out, {"message_id": "m_new"})
        self.assertEqual(self.backend.sent, [("c1", "这是总结\n\n" + AGENT_SUFFIX, "m1")])
        self.assertEqual(self.c.channel_send({"ref": "r1", "text": "这是总结", "kind": "answer", "reply_to": "m1"}),
                         {"message_id": "m_new"})
        self.assertEqual(len(self.backend.sent), 1)

    def test_an_answer_in_a_topic_stays_in_the_topic(self):
        self.c._on_live(message("m2", "g1", 2, kind="group", sender=ME, text="/agent 看看", root="m_root"))
        self.handed()
        self.c.channel_send({"ref": "r1", "text": "好", "kind": "answer", "reply_to": "m2"})
        self.assertEqual(self.backend.sent[0][2], "m_root")

    def test_nothing_goes_where_the_user_did_not_ask(self):
        self.c.on_message(message("m1", "c1", 1, sender="u_a", text="在吗"))
        for p in ({"ref": "r1", "text": "x", "kind": "push"}, {"ref": "r2", "text": "x", "kind": "answer", "reply_to": "m1"},
                  {"ref": "r3", "text": "x", "kind": "answer", "reply_to": "m_unknown"}):
            with self.assertRaises(RpcError) as e:
                self.c.channel_send(p)
            self.assertEqual(e.exception.code, "cannot_send")
        with self.assertRaises(RpcError):
            self.c.channel_send({"text": "x", "kind": "answer", "reply_to": "m1"})
        self.assertEqual(self.backend.sent, [])

    def test_an_answer_only_goes_to_an_agent_request(self):
        # A normal message from the user is not an /agent, and our own answer is not one either.
        self.c.on_message(message("m_plain", "c1", 1, sender=ME, text="ordinary message"))
        self.c.on_message(message("m_ai", "c1", 2, sender=ME, text="答\n\n" + AGENT_SUFFIX))
        for mid in ("m_plain", "m_ai"):
            with self.assertRaises(RpcError) as e:
                self.c.channel_send({"ref": "r_" + mid, "text": "x", "kind": "answer", "reply_to": mid})
            self.assertEqual(e.exception.code, "cannot_send")
        self.assertEqual(self.backend.sent, [])

    def test_an_unknown_result_is_never_sent_again(self):
        self.c._on_live(message("m1", "c1", 1, sender=ME, text="/agent 发"))
        self.handed()
        self.backend.send_error = OSError("cut off")
        with self.assertRaises(RpcError):
            self.c.channel_send({"ref": "r1", "text": "x", "kind": "answer", "reply_to": "m1"})
        self.backend.send_error = None
        with self.assertRaises(RpcError):
            self.c.channel_send({"ref": "r1", "text": "x", "kind": "answer", "reply_to": "m1"})
        self.assertEqual(self.backend.sent, [])


class PluginRoutingTest(unittest.TestCase):
    def test_channel_methods_reach_the_connector(self):
        plugin = Plugin({"id": "larkx", "version": "0", "contributes": {}}, lambda *_: None)

        class Fake:
            methods = {}
            channel_methods = {"typing": lambda p: {"typed": True}}

        plugin.connector = Fake()
        self.assertEqual(plugin.handle("channel.typing", {"contribution": "larkx", "on": True}), {"typed": True})
        with self.assertRaises(RpcError) as e:
            plugin.handle("channel.send", {"contribution": "other"})
        self.assertEqual(e.exception.code, "unknown_method")
        with self.assertRaises(RpcError) as e:
            plugin.handle("channel.typing", {"contribution": "other"})
        self.assertEqual(e.exception.code, "no_contribution")


if __name__ == "__main__":
    unittest.main()
