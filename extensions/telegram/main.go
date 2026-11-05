// The Telegram connector as a Persona plugin (Persona #1704): the user's own Telegram account over MTProto (gotd),
// moved out of personad, where it ran as the built-in plugin jarvisd-telegram. The hub starts it and talks the
// process protocol to it over stdin / stdout through Persona's official Go SDK (internal/serve on sdk/go/ext, the
// connector protocol's connector.* methods); its sign-in stays the hub's sealed connector secret (secrets.*), so a
// hub that had the built-in one hands it over as it was.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/serve"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
	"github.com/nwverse/persona-extensions/pluginkit"
)

//go:embed persona-extension.json
var manifest []byte

// version is the manifest's: what the program answers to initialize, which the hub holds to the manifest.
var version = func() string {
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(manifest, &m)
	return m.Version
}()

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: telegram [version]: started by the Persona hub, which talks to it over stdin / stdout")
			os.Exit(2)
		}
	}
	log.SetFlags(0) // the hub prefixes each line it keeps
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := serve.MainWith(ctx, os.Stdin, os.Stdout, spec.ID, version, manifest, build)
	if err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) { // EOF: the hub went away
		log.Printf("stopped: %v", err)
		os.Exit(1)
	}
}

// build is the connector for what the hub started the program with.
func build(init serve.Init, secrets connector.Secrets) (connector.Connector, error) {
	return telegram.New(secrets, userZone(init.Timezone)).WithRules(pluginkit.TriageRules(init.Config)), nil
}

// userZone is the user's time zone by name, nil when there is none or it does not load (the tools then say UTC):
// never the machine's, which is the hub's (UTC), not the user's.
func userZone(name string) *time.Location {
	if name == "" {
		return nil
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("time zone %q: %v", name, err)
		return nil
	}
	return zone
}
