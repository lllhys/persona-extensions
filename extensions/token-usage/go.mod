module github.com/nwverse/persona-extensions/extensions/token-usage

go 1.27.3

require (
	github.com/nwverse/persona-extensions/pluginkit v0.0.0
	github.com/nwverse/persona/sdk/go v0.0.0
)

replace github.com/nwverse/persona/sdk/go => ../../sdk/go

replace github.com/nwverse/persona-extensions/pluginkit => ../../pluginkit
