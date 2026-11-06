module github.com/nwverse/persona-extensions/extensions/linuxdo

go 1.27.1

require github.com/nwverse/persona/sdk/go v0.0.0 // indirect

require github.com/nwverse/persona-extensions/pluginkit v0.0.0

replace github.com/nwverse/persona-extensions/pluginkit => ../../pluginkit

replace github.com/nwverse/persona/sdk/go => ../../sdk/go
