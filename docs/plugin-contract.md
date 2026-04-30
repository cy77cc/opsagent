# Plugin Contract

## 1. Protocol Overview

UDS JSON-RPC 2.0 over Unix socket. Newline-delimited JSON — one request per connection, one response per request.

## 2. Request Format

```json
{
    "jsonrpc": "2.0",
    "method": "execute_task",
    "id": "<task_id>",
    "params": {
        "task_id": "task-001",
        "type": "plugin_log_parse",
        "deadline_ms": 1714300000000,
        "payload": { ... },
        "chunking": {
            "enabled": true,
            "max_chunk_bytes": 262144,
            "max_total_bytes": 8388608
        }
    }
}
```

## 3. Response Format

**Success:**
```json
{
    "id": "task-001",
    "result": {
        "task_id": "task-001",
        "status": "ok",
        "summary": { ... },
        "chunks": [ ... ],
        "stats": { "duration_ms": 45 }
    }
}
```

**Error:**
```json
{
    "id": "task-001",
    "error": {
        "code": -32602,
        "message": "Configuration error: root_path is required"
    }
}
```

## 4. Chunking Protocol

When output is large, it's split into chunks:

```json
{
    "chunks": [
        {"seq": 1, "eof": false, "data_b64": "base64..."},
        {"seq": 2, "eof": false, "data_b64": "base64..."},
        {"seq": 3, "eof": true, "data_b64": "base64..."}
    ]
}
```

- `seq`: 1-based sequence number
- `eof`: true on the last chunk
- `data_b64`: base64-encoded chunk data

Client reassembles by concatenating `data_b64` from chunks in `seq` order.

## 5. Error Codes

| Code | Meaning | Maps to |
|------|---------|---------|
| -32700 | Parse error | Invalid JSON |
| -32600 | Invalid request | Missing required fields |
| -32601 | Method not found | Unknown method |
| -32602 | Invalid params | PluginError::Config |
| -32603 | Internal error | PluginError::Io |
| -32000 | Server error | PluginError::Execution |
| -32001 | Server error | PluginError::Resource |
| -32002 | Server error | PluginError::Unsupported |

## 6. Lifecycle

1. Client connects to Unix socket
2. Client sends one JSON-RPC request (newline-terminated)
3. Server processes request, sends one JSON-RPC response (newline-terminated)
4. Connection closes
5. Repeat from step 1 for next task

## 7. Health Check

Request:
```json
{"jsonrpc":"2.0","method":"ping","id":"health-1","params":{}}
```

Response:
```json
{"id":"health-1","result":"pong"}
```

## 8. Versioning

Current protocol version: 1.0

Changes are backward-compatible: new fields may be added to responses, new task types may be added. Clients must ignore unknown fields.

## 9. Available Task Types

| Task Type | Description |
|-----------|-------------|
| `plugin_log_parse` | Parse log text, count errors/warnings |
| `plugin_text_process` | Text operations: uppercase, lowercase, word_count |
| `plugin_fs_scan` | Recursive directory scan with file statistics |
| `plugin_conn_analyze` | Analyze network connections from /proc/net |
| `plugin_local_probe` | System health checks: disk, memory, OOM, zombies |
| `plugin_ebpf_collect` | eBPF syscall counting (requires --features ebpf) |
