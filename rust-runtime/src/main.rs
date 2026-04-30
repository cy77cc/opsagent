mod chunking;
mod error;
mod handlers;
mod plugin;
mod protocol;
mod registry;

use protocol::{RpcRequest, RpcResponse, RpcError, TaskRequest, TaskResponse, TaskStats};
use serde_json::{json, Value};
use std::env;
use std::fs;
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::time::Instant;

fn main() {
    let socket = parse_socket_path();
    if fs::metadata(&socket).is_ok() {
        let _ = fs::remove_file(&socket);
    }

    let listener = match UnixListener::bind(&socket) {
        Ok(v) => v,
        Err(e) => {
            eprintln!("failed to bind socket {}: {}", socket, e);
            std::process::exit(1);
        }
    };

    eprintln!("rust runtime listening on {}", socket);

    for stream in listener.incoming() {
        match stream {
            Ok(stream) => {
                if let Err(e) = handle_connection(stream) {
                    eprintln!("handle connection failed: {}", e);
                }
            }
            Err(e) => eprintln!("accept failed: {}", e),
        }
    }
}

fn parse_socket_path() -> String {
    let mut args = env::args().skip(1);
    while let Some(arg) = args.next() {
        if arg == "--socket" {
            if let Some(path) = args.next() {
                return path;
            }
        }
    }
    "/tmp/opsagent/plugin.sock".to_string()
}

fn handle_connection(mut stream: UnixStream) -> Result<(), String> {
    let mut reader = BufReader::new(stream.try_clone().map_err(|e| e.to_string())?);
    let mut line = String::new();
    reader.read_line(&mut line).map_err(|e| e.to_string())?;

    let req: RpcRequest = serde_json::from_str(&line).map_err(|e| e.to_string())?;
    if req.method != "execute_task" {
        let resp = RpcResponse {
            id: req.id,
            result: None,
            error: Some(RpcError {
                code: -32601,
                message: "method not found".to_string(),
            }),
        };
        write_response(&mut stream, &resp)?;
        return Ok(());
    }

    let result = execute_task(req.params);
    let resp = RpcResponse {
        id: result.task_id.clone(),
        result: Some(result),
        error: None,
    };
    write_response(&mut stream, &resp)
}

fn write_response(stream: &mut UnixStream, resp: &RpcResponse) -> Result<(), String> {
    let body = serde_json::to_string(resp).map_err(|e| e.to_string())?;
    stream
        .write_all(format!("{}\n", body).as_bytes())
        .map_err(|e| e.to_string())
}

fn execute_task(req: TaskRequest) -> TaskResponse {
    let started = Instant::now();

    let (status, summary, output) = match req.r#type.as_str() {
        "plugin_log_parse" => handle_log_parse(&req.payload),
        "plugin_text_process" => handle_text_process(&req.payload),
        "plugin_ebpf_collect" => (
            "degraded".to_string(),
            Some(json!({"message": "ebpf unavailable, degraded fallback"})),
            String::new(),
        ),
        "plugin_fs_scan" | "plugin_conn_analyze" | "plugin_local_probe" => (
            "ok".to_string(),
            Some(json!({"message": "stub handler ready", "task_type": req.r#type})),
            String::new(),
        ),
        _ => (
            "error".to_string(),
            None,
            format!("unsupported task type: {}", req.r#type),
        ),
    };

    let chunks = chunking::chunk_output(&output, &req.chunking);

    TaskResponse {
        task_id: req.task_id,
        status,
        error: String::new(),
        summary,
        chunks,
        stats: TaskStats {
            duration_ms: started.elapsed().as_millis() as i64,
        },
    }
}

fn handle_log_parse(payload: &Value) -> (String, Option<Value>, String) {
    let text = payload
        .get("text")
        .and_then(|v| v.as_str())
        .unwrap_or_default();

    let mut lines = 0usize;
    let mut errors = 0usize;
    let mut warnings = 0usize;
    for line in text.lines() {
        lines += 1;
        let lower = line.to_lowercase();
        if lower.contains("error") {
            errors += 1;
        }
        if lower.contains("warn") {
            warnings += 1;
        }
    }

    (
        "ok".to_string(),
        Some(json!({"line_count": lines, "error_count": errors, "warning_count": warnings})),
        String::new(),
    )
}

fn handle_text_process(payload: &Value) -> (String, Option<Value>, String) {
    let text = payload
        .get("text")
        .and_then(|v| v.as_str())
        .unwrap_or_default();
    let op = payload
        .get("operation")
        .and_then(|v| v.as_str())
        .unwrap_or("uppercase");

    match op {
        "uppercase" => (
            "ok".to_string(),
            Some(json!({"operation": op})),
            text.to_uppercase(),
        ),
        "lowercase" => (
            "ok".to_string(),
            Some(json!({"operation": op})),
            text.to_lowercase(),
        ),
        "word_count" => {
            let words = text.split_whitespace().count();
            (
                "ok".to_string(),
                Some(json!({"operation": op, "word_count": words})),
                words.to_string(),
            )
        }
        _ => (
            "error".to_string(),
            Some(json!({"operation": op})),
            format!("unsupported operation: {}", op),
        ),
    }
}
