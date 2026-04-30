use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Deserialize)]
pub struct RpcRequest {
    pub id: String,
    pub method: String,
    pub params: TaskRequest,
}

#[derive(Debug, Serialize)]
pub struct RpcError {
    pub code: i32,
    pub message: String,
}

#[derive(Debug, Serialize)]
pub struct RpcResponse {
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<TaskResponse>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<RpcError>,
}

#[derive(Debug, Deserialize)]
pub struct ChunkingConfig {
    pub enabled: bool,
    pub max_chunk_bytes: usize,
    pub max_total_bytes: usize,
}

#[derive(Debug, Deserialize)]
pub struct TaskRequest {
    pub task_id: String,
    pub r#type: String,
    #[allow(dead_code)]
    pub deadline_ms: i64,
    pub payload: Value,
    pub chunking: ChunkingConfig,
}

#[derive(Debug, Serialize)]
pub struct Chunk {
    pub seq: usize,
    #[serde(rename = "eof")]
    pub eof_flag: bool,
    pub data_b64: String,
}

#[derive(Debug, Serialize)]
pub struct TaskStats {
    pub duration_ms: i64,
}

#[derive(Debug, Serialize)]
pub struct TaskResponse {
    pub task_id: String,
    pub status: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub summary: Option<Value>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub chunks: Vec<Chunk>,
    pub stats: TaskStats,
}

impl RpcResponse {
    pub fn success(id: String, result: TaskResponse) -> Self {
        Self {
            id,
            result: Some(result),
            error: None,
        }
    }

    pub fn error(id: String, code: i32, message: &str) -> Self {
        Self {
            id,
            result: None,
            error: Some(RpcError {
                code,
                message: message.to_string(),
            }),
        }
    }

    pub fn to_json_line(&self) -> String {
        let body = serde_json::to_string(self).unwrap_or_default();
        format!("{}\n", body)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rpc_response_success_serializes() {
        let resp = RpcResponse::success("t1".into(), TaskResponse {
            task_id: "t1".into(),
            status: "ok".into(),
            error: String::new(),
            summary: None,
            chunks: vec![],
            stats: TaskStats { duration_ms: 10 },
        });
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("\"id\":\"t1\""));
        assert!(json.contains("\"status\":\"ok\""));
        assert!(!json.contains("\"error\""));
    }

    #[test]
    fn test_rpc_response_error_serializes() {
        let resp = RpcResponse::error("t2".into(), -32601, "method not found");
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("\"code\":-32601"));
        assert!(json.contains("method not found"));
        assert!(!json.contains("\"result\""));
    }

    #[test]
    fn test_to_json_line_ends_with_newline() {
        let resp = RpcResponse::error("t3".into(), -32600, "bad");
        let line = resp.to_json_line();
        assert!(line.ends_with('\n'));
    }

    #[test]
    fn test_deserialize_request() {
        let json = r#"{"id":"t1","method":"execute_task","params":{"task_id":"t1","type":"plugin_log_parse","deadline_ms":0,"payload":{},"chunking":{"enabled":true,"max_chunk_bytes":256,"max_total_bytes":1024}}}"#;
        let req: RpcRequest = serde_json::from_str(json).unwrap();
        assert_eq!(req.id, "t1");
        assert_eq!(req.method, "execute_task");
        assert_eq!(req.params.task_id, "t1");
        assert_eq!(req.params.r#type, "plugin_log_parse");
    }
}
