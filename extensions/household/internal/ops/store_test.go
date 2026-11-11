package ops

import (
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// A failing op inside a store Update must save nothing, not even the schema that Update's first step wrote.
func TestFailedOpInsideUpdateSavesNothing(t *testing.T) {
	st := store.Open(t.TempDir())
	err := st.Update(func(b *kernel.Base, at time.Time) error {
		if _, err := LoadSamples(b, at, shanghai); err != nil {
			return err
		}
		_, err := Use(b, "e999", 1, at, shanghai)
		return err
	})
	if err == nil {
		t.Fatal("Update with a failing op returned no error")
	}
	if err := st.View(func(b *kernel.Base) error {
		if _, err := b.Space(model.SpaceID); err == nil {
			t.Error("the household space was saved by a failed Update")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The same ops inside a successful Update are saved and read back by a new store on the same directory.
func TestSuccessfulOpsInsideUpdateAreSaved(t *testing.T) {
	dir := t.TempDir()
	st := store.Open(dir)
	if err := st.Update(func(b *kernel.Base, at time.Time) error {
		_, err := LoadSamples(b, at, shanghai)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reopened := store.Open(dir)
	if err := reopened.View(func(b *kernel.Base) error {
		sp, err := b.Space(model.SpaceID)
		if err != nil {
			return err
		}
		if n := len(sp.Entities); n != len(model.Samples()) {
			t.Errorf("saved %d records, want %d", n, len(model.Samples()))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
