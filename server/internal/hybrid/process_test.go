package hybrid

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"maccy-server/internal/store"
)

func TestProcessProtocol(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	workerPath := filepath.Join(t.TempDir(), "worker.mjs")
	worker := `
import readline from "node:readline";
const lines = readline.createInterface({ input: process.stdin });
for await (const line of lines) {
  const request = JSON.parse(line);
  if (request.command === "close") break;
  const response = request.command === "search"
    ? { ok: true, results: [{ id: "entry-1", score: 0.75 }] }
    : { ok: true };
  process.stdout.write(JSON.stringify(response) + "\n");
}
`
	if err := os.WriteFile(workerPath, []byte(worker), 0o600); err != nil {
		t.Fatal(err)
	}

	process, err := Start(context.Background(), Config{NodePath: node, WorkerPath: workerPath})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.Upsert(context.Background(), []store.IndexEntry{{ID: "entry-1", PlainText: "secret"}}); err != nil {
		t.Fatal(err)
	}
	results, err := process.Search(context.Background(), "query", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "entry-1" || results[0].Score != 0.75 {
		t.Fatalf("unexpected results: %#v", results)
	}
}
