import { errorFrom, parseChats, parseMessages, parseSearch, parseStatus, query, ReadError, type Chat, type HostCaller, type Message, type SearchResult } from "./model.ts";
export class LarkBridge {
  constructor(private hub: HostCaller) {}
  private async call<T>(operation: string, params: Record<string, unknown> = {}): Promise<T> {
    try { return await this.hub.call<T>("cli.call", { operation, ...params }); }
    catch (error) { throw errorFrom(error); }
  }
  async status(): Promise<{ state: string; account: string; guide: string[] }> {
    try { return parseStatus(await this.call("auth_status")); }
    catch (error) {
      if (!(error instanceof ReadError)) throw error;
      return { state: error.code === "not_logged_in" ? "not_configured" : "error", account: "", guide: [] };
    }
  }
  async chats(): Promise<Chat[]> { return parseChats(await this.call("chats", { limit: 30 })); }
  async messages(chat: string, before = "", count = 20): Promise<Message[]> {
    return parseMessages(await this.call("messages", { chat_id: chat, ...(before ? { before } : {}), limit: count }));
  }
  async thread(thread: string, count = 20): Promise<Message[]> {
    return parseMessages(await this.call("messages", { thread, limit: count }));
  }
  async search(kind: "messages" | "chats" | "docs" | "people", value: string): Promise<SearchResult> {
    return parseSearch(await this.call("search", { kind, query: query(value), limit: 20 }), kind);
  }
}
