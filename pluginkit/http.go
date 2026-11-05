package pluginkit

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"
)

// SocketEnv names the unix socket, under the name a hub sent it with before the rename (#1844).
const SocketEnv = "PERSONA_PLUGIN_SOCKET"

// socketEnvNames is the socket's name under each hub generation, newest first: a current hub sends
// PERSONA_EXTENSION_SOCKET (as sdk/go/ext's Env reads it), older ones PERSONA_PLUGIN_SOCKET, and Persona's
// JARVIS_PLUGIN_SOCKET.
var socketEnvNames = [3]string{"PERSONA_EXTENSION_SOCKET", SocketEnv, "JARVIS_PLUGIN_SOCKET"}

// Socket is where the hub wants the plugin's HTTP ("" when the manifest asks for none). It reads the name under
// whichever of the hub generations started the plugin, the current one first.
func Socket() string {
	for _, name := range socketEnvNames {
		if s := os.Getenv(name); s != "" {
			return s
		}
	}
	return ""
}

// ServeHTTP serves h on the unix socket path until ctx ends (a stale socket file from a crash is replaced).
func ServeHTTP(ctx context.Context, path string, h http.Handler) error {
	_ = os.Remove(path)
	ln, err := listenUnix(path)
	if err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
