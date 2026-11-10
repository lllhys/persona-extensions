package main

import "strings"

// Config is the extension's configuration (persona-extension.json, configuration): which endpoints of Apple Music to
// show. The connector is one; a Mac and the iPhone are endpoints inside it.
type Config struct {
	// Macs are the computers whose local Music library this extension reads. The manifest limits the list to one.
	Macs []string `json:"macs"`
	// IPhone shows the snapshot of the Music app on the user's iPhone.
	IPhone bool `json:"iphone"`
}

// machine is the first selected Mac, or "".
func (c Config) machine() string {
	for _, m := range c.Macs {
		if m = strings.TrimSpace(m); m != "" {
			return m
		}
	}
	return ""
}
