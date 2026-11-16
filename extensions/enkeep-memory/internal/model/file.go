package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ErrPending: the request has been written for someone else to answer (File), and has no answer yet.
var ErrPending = errors.New("answer pending")

// File answers requests from files, for an offline rerun whose model is not a command line: a request with no answer
// is written to <Dir>/requests/<key>.json ({step, system, user, schema}) and fails with ErrPending; whoever answers
// writes the model's JSON answer to <Dir>/answers/<key>.json, and the next run takes it. The key is the hash of the
// request, so an answered request is never asked again. It is never used by the extension the hub runs.
type File struct{ Dir string }

// FileKey is the name a request's request and answer files go by.
func FileKey(r Request) string {
	h := sha256.New()
	for _, p := range []string{r.Step, r.System, r.User, string(r.Schema)} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:20]
}

// Complete returns the answer if it is there, otherwise writes the request.
func (f File) Complete(_ context.Context, r Request) (Response, error) {
	key := FileKey(r)
	if b, err := os.ReadFile(filepath.Join(f.Dir, "answers", key+".json")); err == nil {
		return Response{Text: string(b), Model: "file"}, nil
	}
	if err := os.MkdirAll(filepath.Join(f.Dir, "requests"), 0o700); err != nil {
		return Response{}, err
	}
	b, _ := json.Marshal(map[string]any{"key": key, "step": r.Step, "system": r.System, "user": r.User, "schema": json.RawMessage(r.Schema)})
	if err := os.WriteFile(filepath.Join(f.Dir, "requests", key+".json"), b, 0o600); err != nil {
		return Response{}, err
	}
	return Response{}, ErrPending
}
