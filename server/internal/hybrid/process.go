package hybrid

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"maccy-server/internal/store"
)

type Config struct {
	NodePath        string
	WorkerPath      string
	CollectionPath  string
	ModelCachePath  string
	FTSTokenizer    string
	RRFRankConstant int
}

type Process struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	closed  bool
}

type request struct {
	Command         string             `json:"command"`
	CollectionPath  string             `json:"collection_path,omitempty"`
	ModelCachePath  string             `json:"model_cache_path,omitempty"`
	FTSTokenizer    string             `json:"fts_tokenizer,omitempty"`
	RRFRankConstant int                `json:"rrf_rank_constant,omitempty"`
	Entries         []store.IndexEntry `json:"entries,omitempty"`
	Query           string             `json:"query,omitempty"`
	Limit           int                `json:"limit,omitempty"`
}

type response struct {
	OK      bool                `json:"ok"`
	Error   string              `json:"error,omitempty"`
	Results []store.RankedEntry `json:"results,omitempty"`
}

func Start(ctx context.Context, config Config) (*Process, error) {
	command := exec.CommandContext(ctx, config.NodePath, config.WorkerPath)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open zvec worker stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open zvec worker stdout: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start zvec worker: %w", err)
	}

	process := &Process{command: command, stdin: stdin, stdout: bufio.NewReader(stdout)}
	initRequest := request{
		Command:         "init",
		CollectionPath:  config.CollectionPath,
		ModelCachePath:  config.ModelCachePath,
		FTSTokenizer:    config.FTSTokenizer,
		RRFRankConstant: config.RRFRankConstant,
	}
	if err := process.call(ctx, initRequest, nil); err != nil {
		_ = process.Close()
		return nil, fmt.Errorf("initialize zvec worker: %w", err)
	}
	return process, nil
}

func (p *Process) Ping(ctx context.Context) error {
	return p.call(ctx, request{Command: "ping"}, nil)
}

func (p *Process) Upsert(ctx context.Context, entries []store.IndexEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return p.call(ctx, request{Command: "upsert", Entries: entries}, nil)
}

func (p *Process) Search(ctx context.Context, query string, limit int) ([]store.RankedEntry, error) {
	var result response
	if err := p.call(ctx, request{Command: "search", Query: query, Limit: limit}, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

func (p *Process) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	encoder := json.NewEncoder(p.stdin)
	_ = encoder.Encode(request{Command: "close"})
	_ = p.stdin.Close()
	if err := p.command.Wait(); err != nil {
		return fmt.Errorf("wait for zvec worker: %w", err)
	}
	return nil
}

func (p *Process) call(ctx context.Context, input request, output *response) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("zvec worker is closed")
	}
	if err := json.NewEncoder(p.stdin).Encode(input); err != nil {
		return fmt.Errorf("write zvec worker request: %w", err)
	}
	line, err := p.stdout.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read zvec worker response: %w", err)
	}
	var result response
	if err := json.Unmarshal(line, &result); err != nil {
		return fmt.Errorf("decode zvec worker response: %w", err)
	}
	if !result.OK {
		if result.Error == "" {
			result.Error = "unknown error"
		}
		return errors.New(result.Error)
	}
	if output != nil {
		*output = result
	}
	return nil
}
