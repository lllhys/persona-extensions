// WeChat runs independently of personad and speaks the official extension protocol.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/nwverse/persona-extensions/extensions/wechat/internal/wechat"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

var version = func() string {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		panic(err)
	}
	return m.Version
}()

func program() ext.Program {
	p := wechat.Program(version)
	p.Manifest = manifest
	return p
}

func main() {
	if len(os.Args) > 1 && os.Args[1] != "manifest" {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: wechat [version|manifest]: started by the Persona hub over stdin / stdout")
			os.Exit(2)
		}
	}
	log.SetFlags(0)
	ext.Main(program())
}
