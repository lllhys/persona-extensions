import { withExactParams, type Program } from "../index.ts";

// A real source Program: the host and program may bundle different SDK copies.
export default {
  id: "example",
  version: "1.0.0",
  namespaces: {
    connector: withExactParams((_method, _legacy, call) => {
      if (!call) return {};
      const args = call.params?.field("args");
      const values = args?.int64Fields(["topic", "replies"]) ?? { topic: 0n, replies: 0n };
      // Exact input is deliberately rendered as text, not promised as exact JSON output.
      return { topic: values.topic.toString(), replies: values.replies.toString(), raw: args?.raw };
    }),
  },
} satisfies Program;
