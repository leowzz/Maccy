package hybrid

import (
	"context"
	"fmt"

	"maccy-server/internal/store"
)

type IndexSource interface {
	ListIndexEntries(context.Context, string, string, int) ([]store.IndexEntry, error)
}

type IndexWriter interface {
	Upsert(context.Context, []store.IndexEntry) error
}

func Sync(ctx context.Context, source IndexSource, writer IndexWriter, accountID string, batchSize int) (int, error) {
	afterID := ""
	total := 0
	for {
		entries, err := source.ListIndexEntries(ctx, accountID, afterID, batchSize)
		if err != nil {
			return total, err
		}
		if len(entries) == 0 {
			return total, nil
		}
		if err := writer.Upsert(ctx, entries); err != nil {
			return total, fmt.Errorf("upsert zvec sync batch: %w", err)
		}
		total += len(entries)
		afterID = entries[len(entries)-1].ID
		if len(entries) < batchSize {
			return total, nil
		}
	}
}
