// Command chatgpt-apps is the ChatGPT apps extension for Persona: after the user signs in to ChatGPT (a device code,
// manifest auth), the connector "chatgpt" gives the assistant the apps connected to that ChatGPT account and the
// ChatGPT plugin marketplace. Reads are connector tools; every change is a connector action the hub runs only after
// the user approves it on the phone (confirm_always). The tokens stay in this process and the hub's encrypted
// connector secrets.
//
// Everything goes through chatgpt.com's own web backend (not a public API) with the OAuth client the Codex CLI uses.
package main

import "github.com/nwverse/persona/sdk/go/ext"

func main() {
	ext.Main(newApp(liveEndpoints()).program())
}
