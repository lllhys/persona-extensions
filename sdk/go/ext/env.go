package ext

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

// Environment is what the hub gives a program in its environment (docs/extensions.md "How configuration and secrets reach an extension").
type Environment struct {
	ID, Dir, Data, Socket string
	// Config and Secrets are for a program that does not speak the protocol (rpc: none): the protocol hands them
	// over with initialize instead.
	Config  json.RawMessage
	Secrets map[string]string
	// HubURL and HubToken: only with the hub_api permission accepted.
	HubURL, HubToken string
}

// Env reads the environment, under the names of any hub: PERSONA_EXTENSION_*, before the rename (#1844)
// PERSONA_PLUGIN_*, before Persona JARVIS_PLUGIN_*.
func Env() Environment {
	e := Environment{
		ID: getenv("ID"), Dir: getenv("DIR"), Data: getenv("DATA"), Socket: getenv("SOCKET"),
		HubURL: first("PERSONA_HUB_URL", "JARVIS_HUB_URL"), HubToken: first("PERSONA_HUB_TOKEN", "JARVIS_HUB_TOKEN"),
	}
	if c := getenv("CONFIG"); c != "" {
		e.Config = json.RawMessage(c)
	}
	if s := getenv("SECRETS"); s != "" {
		_ = json.Unmarshal([]byte(s), &e.Secrets)
	}
	return e
}

func getenv(name string) string {
	return first("PERSONA_EXTENSION_"+name, "PERSONA_PLUGIN_"+name, "JARVIS_PLUGIN_"+name)
}

func first(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// ServeHTTP serves h on the unix socket at path (the hub forwards /api/v1/extensions/<id>/… and an api
// contribution's prefix to it) until ctx ends. GET /health, which the hub polls, is answered here.
func ServeHTTP(ctx context.Context, path string, h http.Handler) error {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
