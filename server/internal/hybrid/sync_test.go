package hybrid

import (
	"context"
	"testing"

	"maccy-server/internal/store"
)

type fakeIndexSource struct {
	entries []store.IndexEntry
}

func (f fakeIndexSource) ListIndexEntries(_ context.Context, _ string, afterID string, limit int) ([]store.IndexEntry, error) {
	result := make([]store.IndexEntry, 0, limit)
	for _, entry := range f.entries {
		if entry.ID > afterID && len(result) < limit {
			result = append(result, entry)
		}
	}
	return result, nil
}

type fakeIndexWriter struct {
	batches [][]store.IndexEntry
}

func (f *fakeIndexWriter) Upsert(_ context.Context, entries []store.IndexEntry) error {
	f.batches = append(f.batches, entries)
	return nil
}

func TestSyncPagesThroughAllEntries(t *testing.T) {
	source := fakeIndexSource{entries: []store.IndexEntry{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	writer := &fakeIndexWriter{}

	total, err := Sync(context.Background(), source, writer, "account", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(writer.batches) != 2 {
		t.Fatalf("unexpected sync result: total=%d batches=%#v", total, writer.batches)
	}
}
