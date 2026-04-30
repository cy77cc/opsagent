mod chunking;
mod error;
mod handlers;
mod plugin;
mod protocol;
mod registry;

use std::sync::Arc;

use anyhow::Result;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixListener;
use tracing::{error, info};

use crate::chunking::chunk_output;
use crate::handlers::log_parse::LogParsePlugin;
use crate::handlers::text_process::TextProcessPlugin;
use crate::protocol::{RpcRequest, RpcResponse, TaskResponse, TaskStats};
use crate::registry::PluginRegistry;

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt().with_target(false).init();

    let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
        .unwrap_or_else(|_| "/tmp/opsagent/plugin.sock".to_string());

    if std::fs::metadata(&socket_path).is_ok() {
        let _ = std::fs::remove_file(&socket_path);
    }

    let listener = UnixListener::bind(&socket_path)?;
    info!(socket = %socket_path, "rust runtime listening");

    let registry = Arc::new(build_registry());

    loop {
        let (stream, _) = listener.accept().await?;
        let reg = registry.clone();

        tokio::spawn(async move {
            if let Err(e) = handle_connection(stream, reg).await {
                error!("handle connection failed: {}", e);
            }
        });
    }
}

fn build_registry() -> PluginRegistry {
    let mut reg = PluginRegistry::new();
    reg.register(Box::new(LogParsePlugin));
    reg.register(Box::new(TextProcessPlugin));
    reg
}

async fn handle_connection(
    mut stream: tokio::net::UnixStream,
    registry: Arc<PluginRegistry>,
) -> Result<()> {
    let (reader, mut writer) = stream.split();
    let mut reader = BufReader::new(reader);
    let mut line = String::new();
    reader.read_line(&mut line).await?;

    let req: RpcRequest = serde_json::from_str(&line)?;

    if req.method != "execute_task" {
        let resp = RpcResponse::error(req.id, -32601, "method not found");
        writer.write_all(resp.to_json_line().as_bytes()).await?;
        return Ok(());
    }

    let result = registry
        .dispatch(&req.params.r#type, &req.params.payload)
        .await;

    let resp = match result {
        Ok(plugin_result) => {
            let chunks = chunk_output(&plugin_result.output, &req.params.chunking);
            RpcResponse::success(
                req.id,
                TaskResponse {
                    task_id: req.params.task_id,
                    status: plugin_result.status,
                    error: String::new(),
                    summary: plugin_result.summary,
                    chunks,
                    stats: TaskStats { duration_ms: 0 },
                },
            )
        }
        Err(e) => RpcResponse::error(req.id, e.rpc_code(), &e.to_string()),
    };

    writer.write_all(resp.to_json_line().as_bytes()).await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::UnixStream;

    async fn start_test_runtime(socket_path: &str) -> tokio::task::JoinHandle<()> {
        let _ = std::fs::remove_file(socket_path);
        let listener = UnixListener::bind(socket_path).unwrap();
        let registry = Arc::new(build_registry());

        tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let reg = registry.clone();
            handle_connection(stream, reg).await.unwrap();
        })
    }

    #[tokio::test]
    async fn test_log_parse_via_uds() {
        let socket = "/tmp/opsagent-test-log-parse.sock";
        let handle = start_test_runtime(socket).await;

        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        let mut stream = UnixStream::connect(socket).await.unwrap();
        let req = json!({
            "id": "t1",
            "method": "execute_task",
            "params": {
                "task_id": "t1",
                "type": "plugin_log_parse",
                "deadline_ms": 0,
                "payload": {"text": "error line\ninfo line"},
                "chunking": {"enabled": true, "max_chunk_bytes": 256, "max_total_bytes": 1024}
            }
        });
        stream
            .write_all(format!("{}\n", req).as_bytes())
            .await
            .unwrap();

        let mut buf = vec![0u8; 4096];
        let n = stream.read(&mut buf).await.unwrap();
        let resp: serde_json::Value = serde_json::from_slice(&buf[..n]).unwrap();

        assert_eq!(resp["id"], "t1");
        assert_eq!(resp["result"]["status"], "ok");
        assert_eq!(resp["result"]["summary"]["error_count"], 1);

        handle.abort();
        let _ = std::fs::remove_file(socket);
    }

    #[tokio::test]
    async fn test_text_process_via_uds() {
        let socket = "/tmp/opsagent-test-text-proc.sock";
        let handle = start_test_runtime(socket).await;

        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        let mut stream = UnixStream::connect(socket).await.unwrap();
        let req = json!({
            "id": "t2",
            "method": "execute_task",
            "params": {
                "task_id": "t2",
                "type": "plugin_text_process",
                "deadline_ms": 0,
                "payload": {"text": "hello", "operation": "uppercase"},
                "chunking": {"enabled": false, "max_chunk_bytes": 0, "max_total_bytes": 0}
            }
        });
        stream
            .write_all(format!("{}\n", req).as_bytes())
            .await
            .unwrap();

        let mut buf = vec![0u8; 4096];
        let n = stream.read(&mut buf).await.unwrap();
        let resp: serde_json::Value = serde_json::from_slice(&buf[..n]).unwrap();

        assert_eq!(resp["result"]["status"], "ok");
        let b64 = resp["result"]["chunks"][0]["data_b64"].as_str().unwrap();
        use base64::Engine;
        let decoded = base64::engine::general_purpose::STANDARD.decode(b64).unwrap();
        assert_eq!(String::from_utf8(decoded).unwrap(), "HELLO");

        handle.abort();
        let _ = std::fs::remove_file(socket);
    }

    #[tokio::test]
    async fn test_unknown_method() {
        let socket = "/tmp/opsagent-test-unknown-method.sock";
        let handle = start_test_runtime(socket).await;

        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        let mut stream = UnixStream::connect(socket).await.unwrap();
        let req = json!({
            "id": "t3",
            "method": "ping",
            "params": {
                "task_id": "t3",
                "type": "x",
                "deadline_ms": 0,
                "payload": {},
                "chunking": {"enabled": false, "max_chunk_bytes": 0, "max_total_bytes": 0}
            }
        });
        stream
            .write_all(format!("{}\n", req).as_bytes())
            .await
            .unwrap();

        let mut buf = vec![0u8; 4096];
        let n = stream.read(&mut buf).await.unwrap();
        let resp: serde_json::Value = serde_json::from_slice(&buf[..n]).unwrap();

        assert_eq!(resp["error"]["code"], -32601);

        handle.abort();
        let _ = std::fs::remove_file(socket);
    }

    #[tokio::test]
    async fn test_unknown_task_type() {
        let socket = "/tmp/opsagent-test-unknown-task.sock";
        let handle = start_test_runtime(socket).await;

        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        let mut stream = UnixStream::connect(socket).await.unwrap();
        let req = json!({
            "id": "t4",
            "method": "execute_task",
            "params": {
                "task_id": "t4",
                "type": "nonexistent_handler",
                "deadline_ms": 0,
                "payload": {},
                "chunking": {"enabled": false, "max_chunk_bytes": 0, "max_total_bytes": 0}
            }
        });
        stream
            .write_all(format!("{}\n", req).as_bytes())
            .await
            .unwrap();

        let mut buf = vec![0u8; 4096];
        let n = stream.read(&mut buf).await.unwrap();
        let resp: serde_json::Value = serde_json::from_slice(&buf[..n]).unwrap();

        assert_eq!(resp["error"]["code"], -32000);

        handle.abort();
        let _ = std::fs::remove_file(socket);
    }
}
