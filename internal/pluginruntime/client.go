package pluginruntime

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// ExecuteTask sends a task to runtime via local RPC.
func (r *Runtime) ExecuteTask(ctx context.Context, req TaskRequest) (*TaskResponse, error) {
	if !r.cfg.Enabled {
		return nil, fmt.Errorf("plugin runtime is disabled")
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if req.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if req.Type == "" {
		return nil, fmt.Errorf("task type is required")
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
	if req.DeadlineMS <= 0 {
		req.DeadlineMS = time.Now().Add(r.cfg.RequestTimeout).UnixMilli()
	}
	if req.Chunking.MaxChunkBytes <= 0 {
		req.Chunking.MaxChunkBytes = r.cfg.ChunkSizeBytes
	}
	if req.Chunking.MaxTotalBytes <= 0 {
		req.Chunking.MaxTotalBytes = r.cfg.MaxResultBytes
	}
	if !req.Chunking.Enabled {
		req.Chunking.Enabled = true
	}

	rpcReq := rpcRequest{
		ID:     req.TaskID,
		Method: "execute_task",
		Params: req,
	}

	rpcCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		rpcCtx, cancel = context.WithTimeout(ctx, r.cfg.RequestTimeout)
		defer cancel()
	}

	conn, err := r.dial(rpcCtx, "unix", r.cfg.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("dial plugin runtime: %w", err)
	}
	defer conn.Close()

	if deadline, ok := rpcCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := json.NewEncoder(conn).Encode(rpcReq); err != nil {
		return nil, fmt.Errorf("encode plugin request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read plugin response: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode plugin response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("plugin rpc error (%d): %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("empty plugin response")
	}
	if err := validateChunkSize(resp.Result, r.cfg.MaxResultBytes); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func validateChunkSize(resp *TaskResponse, maxBytes int) error {
	if maxBytes <= 0 {
		return nil
	}
	var total int
	for _, c := range resp.Chunks {
		decodedLen := base64.StdEncoding.DecodedLen(len(c.DataB64))
		total += decodedLen
		if total > maxBytes {
			return fmt.Errorf("plugin response exceeded max result bytes: %d > %d", total, maxBytes)
		}
	}
	return nil
}
