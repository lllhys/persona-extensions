package pluginkit

import "testing"

func TestSocket(t *testing.T) {
	const (
		ext    = "PERSONA_EXTENSION_SOCKET"
		plugin = "PERSONA_PLUGIN_SOCKET"
		legacy = "JARVIS_PLUGIN_SOCKET"
	)
	cases := []struct {
		name             string
		ext, plugin, old string
		want             string
	}{
		{name: "none", want: ""},
		{name: "current hub", ext: "/run/current.sock", want: "/run/current.sock"},
		{name: "renamed hub", plugin: "/run/plugin.sock", want: "/run/plugin.sock"},
		{name: "legacy hub", old: "/run/jarvis.sock", want: "/run/jarvis.sock"},
		{name: "precedence", ext: "/run/current.sock", plugin: "/run/plugin.sock", old: "/run/jarvis.sock", want: "/run/current.sock"},
		{name: "plugin over legacy", plugin: "/run/plugin.sock", old: "/run/jarvis.sock", want: "/run/plugin.sock"},
		// An empty value is no answer, as os.Getenv treats it: the next name is tried.
		{name: "empty current falls back", ext: "", plugin: "/run/plugin.sock", want: "/run/plugin.sock"},
		{name: "empty middle falls back", plugin: "", old: "/run/jarvis.sock", want: "/run/jarvis.sock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ext, tc.ext)
			t.Setenv(plugin, tc.plugin)
			t.Setenv(legacy, tc.old)
			if got := Socket(); got != tc.want {
				t.Errorf("Socket() = %q, want %q", got, tc.want)
			}
		})
	}
}
