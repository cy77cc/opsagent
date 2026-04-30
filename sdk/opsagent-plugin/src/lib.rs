pub mod error;
pub mod protocol;

use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixListener;
use tokio::signal;

use error::{PluginError, Result};
use protocol::{RpcRequest, RpcResponse, TaskRequest, TaskResponse};

/// Plugin is the trait that plugin authors implement to receive tasks from the
/// OpsAgent PluginGateway.
#[async_trait]
pub trait Plugin: Send + Sync {
    /// Returns the list of task type strings this plugin supports.
    fn task_types(&self) -> Vec<String>;

    /// Called once when the plugin starts. cfg may be Value::Null.
    async fn init(&self, cfg: Value) -> Result<()>;

    /// Processes a single task request and returns a response.
    async fn execute(&self, req: &TaskRequest) -> Result<TaskResponse>;

    /// Called when the plugin is being terminated gracefully.
    async fn shutdown(&self) -> Result<()>;

    /// Returns Ok(()) if the plugin is healthy.
    async fn health_check(&self) -> Result<()>;
}

/// ServeOptions holds configuration for the plugin server.
pub struct ServeOptions {
    /// Timeout for graceful shutdown.
    pub graceful_timeout: Duration,
}

impl Default for ServeOptions {
    fn default() -> Self {
        Self {
            graceful_timeout: Duration::from_secs(10),
        }
    }
}

/// serve is a convenience wrapper around serve_with_options with default options.
pub async fn serve(plugin: impl Plugin + 'static) -> Result<()> {
    serve_with_options(plugin, ServeOptions::default()).await
}

/// serve_with_options starts the plugin UDS server. It reads the socket path from
/// the OPSAGENT_PLUGIN_SOCKET environment variable, initialises the plugin, and
/// listens for JSON-RPC requests until a ctrl-c signal is received.
pub async fn serve_with_options(
    plugin: impl Plugin + 'static,
    options: ServeOptions,
) -> Result<()> {
    let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET").map_err(|_| {
        PluginError::Config("OPSAGENT_PLUGIN_SOCKET environment variable is not set".into())
    })?;

    let plugin = Arc::new(plugin);

    plugin.init(Value::Null).await?;

    // Remove stale socket if present.
    let _ = std::fs::remove_file(&socket_path);

    let listener = UnixListener::bind(&socket_path).map_err(|e| {
        PluginError::Io(std::io::Error::new(
            e.kind(),
            format!("listen {}: {}", socket_path, e),
        ))
    })?;

    tracing::info!(socket = %socket_path, "plugin listening");

    let shutdown_signal = signal::ctrl_c();

    tokio::select! {
        result = accept_loop(listener, plugin.clone()) => {
            if let Err(e) = result {
                tracing::error!(error = %e, "accept loop error");
            }
        }
        _ = shutdown_signal => {
            tracing::info!("received ctrl-c, shutting down");
        }
    }

    // Graceful shutdown with timeout.
    match tokio::time::timeout(options.graceful_timeout, plugin.shutdown()).await {
        Ok(Ok(())) => {}
        Ok(Err(e)) => {
            tracing::error!(error = %e, "plugin shutdown error");
        }
        Err(_) => {
            tracing::warn!("graceful timeout exceeded, forcing exit");
        }
    }

    let _ = std::fs::remove_file(&socket_path);
    Ok(())
}

async fn accept_loop(listener: UnixListener, plugin: Arc<impl Plugin + 'static>) -> Result<()> {
    loop {
        let (stream, _addr) = listener.accept().await?;
        let plugin = plugin.clone();
        tokio::spawn(async move {
            if let Err(e) = handle_connection(stream, plugin).await {
                tracing::error!(error = %e, "connection error");
            }
        });
    }
}

/// handle_connection reads newline-delimited JSON-RPC requests from the stream
/// and dispatches them to the plugin.
async fn handle_connection(stream: tokio::net::UnixStream, plugin: Arc<impl Plugin>) -> Result<()> {
    let (reader, mut writer) = stream.into_split();
    let mut reader = BufReader::new(reader);
    let mut line = String::new();

    loop {
        line.clear();
        let n = reader.read_line(&mut line).await?;
        if n == 0 {
            break; // EOF
        }

        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        let response = match serde_json::from_str::<RpcRequest>(trimmed) {
            Ok(req) => dispatch_request(req, &*plugin).await,
            Err(e) => RpcResponse::error(0, -32700, &format!("parse error: {}", e)),
        };

        let data = response.to_json_line();
        writer.write_all(&data).await?;
    }

    Ok(())
}

async fn dispatch_request(req: RpcRequest, plugin: &impl Plugin) -> RpcResponse {
    match req.method.as_str() {
        "ping" => RpcResponse::success(req.id, Value::String("pong".into())),
        "execute_task" => match plugin.execute(&req.params).await {
            Ok(resp) => match serde_json::to_value(resp) {
                Ok(val) => RpcResponse::success(req.id, val),
                Err(e) => RpcResponse::error(req.id, -32603, &format!("serialize error: {}", e)),
            },
            Err(PluginError::Config(msg)) => RpcResponse::error(req.id, -32602, &msg),
            Err(PluginError::Execution(msg)) => RpcResponse::error(req.id, -32000, &msg),
            Err(PluginError::Io(e)) => {
                RpcResponse::error(req.id, -32603, &format!("IO error: {}", e))
            }
            Err(PluginError::Json(e)) => {
                RpcResponse::error(req.id, -32700, &format!("JSON error: {}", e))
            }
        },
        _ => RpcResponse::error(req.id, -32601, &format!("method not found: {}", req.method)),
    }
}
