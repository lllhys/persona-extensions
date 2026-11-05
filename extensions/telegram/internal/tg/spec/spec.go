// Package spec is what the Telegram connector declares: the program answers it in initialize, and persona-plugin.json,
// which the hub reads before the program ever runs (a switched-off connector can still be signed in to), says the same
// (plugin_test.go holds the two together).
package spec

import (
	"slices"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/prompts"
)

// ID and Name of the connector; CLI is its command line on the assistant's PATH, planned by CLITool.
const (
	ID      = "telegram"
	Name    = "Telegram"
	CLI     = "tg"
	CLITool = "telegram_cli"
)

// CLITools and CLIActions are what the tg CLI calls (its reads, its writes): unlisted, so the models' tool lists keep
// only the assistant's three read tools; the telegram skill tells the assistant how to use the CLI.
var (
	CLITools = []string{"telegram_list_chats", "telegram_read_messages", "telegram_search_messages", "telegram_get_message",
		"telegram_get_chat", "telegram_list_members"}
	CLIActions = []string{"telegram_send", "telegram_forward", "telegram_react", "telegram_mark_read"}
)

// Manifest: the assistant's read tools, the CLI's reads and its writes (actions: each through the hub's gate) and
// the tool that plans its command lines, and the inbox's conversations as the app's conversation page (threads).
func Manifest() connector.Manifest {
	return connector.Manifest{
		Capabilities: []connector.Capability{connector.CapStatus, connector.CapTools, connector.CapActions, connector.CapEvents,
			connector.CapInbox, connector.CapArchive, connector.CapLogin, connector.CapThreads, connector.CapFallback},
		Unlisted:         append(append(slices.Clone(CLITools), CLIActions...), CLITool),
		Kinds:            []string{"dm", "mention"},
		KindDescriptions: prompts.KindDescriptions,
		CLI:              CLI,
	}
}
