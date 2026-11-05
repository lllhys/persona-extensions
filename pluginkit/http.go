package pluginkit

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"
)

// SocketEnv names the unix socket a plugin with runtime.http serves its HTTP on.
const SocketEnv = "PERSONA_PLUGIN_SOCKET"

// Socket is where the hub wants the plugin's HTTP ("" when the manifest asks for none).
func Socket() string {
	if s := os.Getenv(SocketEnv); s != "" {
		return s
	}
	return os.Getenv("JARVIS_PLUGIN_SOCKET")
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
