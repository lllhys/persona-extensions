module github.com/nwverse/persona-extensions/extensions/freshrss

go 1.27.1

require (
	github.com/nwverse/persona-extensions/pluginkit v0.0.0
	github.com/nwverse/persona/sdk/go v0.0.0
	golang.org/x/net v0.62.0
)

replace github.com/nwverse/persona/sdk/go => ../../sdk/go

replace github.com/nwverse/persona-extensions/pluginkit => ../../pluginkit
