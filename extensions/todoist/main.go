package main

import (
	_ "embed"

	"github.com/nwverse/persona-extensions/pluginkit"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)
