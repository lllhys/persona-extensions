package telegram

import (
	"encoding/json"
	"fmt"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tgcli"
)

// cmdLine is the tg command line's own tool (spec.CLITool; docs/extensions/telegram.md#cli-reference): it plans a
// command line or shows an answer as text, from the words alone, so it answers when Telegram does not (help, a
// mistake in the words) and a flood wait does not hold it.
func cmdLine(args json.RawMessage) (string, error) {
	var a struct {
		Argv   []string `json:"argv"`
		Stdin  string   `json:"stdin"`
		Render string   `json:"render"`
		Result string   `json:"result"`
	}
	if len(args) > 0 {
		if err := decodeArgs(args, &a); err != nil {
			return "", fmt.Errorf("%s arguments: %w", spec.CLITool, err)
		}
	}
	if a.Render != "" {
		out, err := tgcli.Render(a.Render, a.Result)
		if err != nil {
			return "", err
		}
		return jsonOf(map[string]string{"stdout": out})
	}
	return jsonOf(tgcli.PlanOf(a.Argv, a.Stdin))
}
