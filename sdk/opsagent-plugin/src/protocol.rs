use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Deserialize)]
pub struct RpcRequest {
    pub id: i64,
    pub method: String,
    #[serde(default)]
    pub params: TaskRequest,
}

#[derive(Debug, Serialize)]
pub struct RpcResponse {
    pub id: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<RpcError>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RpcError {
    pub code: i32,
    pub message: String,
}

#[derive(Debug, Deserialize, Default)]
pub struct TaskRequest {
    #[serde(default)]
    pub task_id: String,
    #[serde(default)]
    pub task_type: String,
    #[serde(default)]
    pub params: Value,
    #[serde(default)]
    pub deadline_ms: i64,
}

#[derive(Debug, Serialize)]
pub struct TaskResponse {
    pub task_id: String,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

impl RpcResponse {
    pub fn success(id: i64, result: Value) -> Self {
        Self {
            id,
            result: Some(result),
            error: None,
        }
    }

    pub fn error(id: i64, code: i32, message: &str) -> Self {
        Self {
            id,
            result: None,
            error: Some(RpcError {
                code,
                message: message.to_string(),
            }),
        }
    }

    pub fn to_json_line(&self) -> Vec<u8> {
        let mut body = serde_json::to_vec(self).unwrap_or_default();
        body.push(b'\n');
        body
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rpc_response_success_serializes() {
        let resp = RpcResponse::success(1, serde_json::json!({"status": "ok"}));
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("\"id\":1"));
        assert!(json.contains("\"status\":\"ok\""));
        assert!(!json.contains("\"error\""));
    }

    #[test]
    fn test_rpc_response_error_serializes() {
        let resp = RpcResponse::error(2, -32601, "method not found");
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("\"code\":-32601"));
        assert!(json.contains("method not found"));
        assert!(!json.contains("\"result\""));
    }

    #[test]
    fn test_to_json_line_ends_with_newline() {
        let resp = RpcResponse::error(3, -32600, "bad");
        let line = resp.to_json_line();
        assert_eq!(*line.last().unwrap(), b'\n');
    }

    #[test]
    fn test_deserialize_request_with_defaults() {
        let json = r#"{"id":1,"method":"ping"}"#;
        let req: RpcRequest = serde_json::from_str(json).unwrap();
        assert_eq!(req.id, 1);
        assert_eq!(req.method, "ping");
        assert!(req.params.task_id.is_empty());
    }

    #[test]
    fn test_deserialize_request_with_params() {
        let json = r#"{"id":42,"method":"execute_task","params":{"task_id":"t1","task_type":"echo","params":{"msg":"hello"},"deadline_ms":5000}}"#;
        let req: RpcRequest = serde_json::from_str(json).unwrap();
        assert_eq!(req.id, 42);
        assert_eq!(req.method, "execute_task");
        assert_eq!(req.params.task_id, "t1");
        assert_eq!(req.params.task_type, "echo");
        assert_eq!(req.params.deadline_ms, 5000);
    }

    #[test]
    fn test_task_response_serializes_with_optional_fields() {
        let resp = TaskResponse {
            task_id: "t1".into(),
            status: "ok".into(),
            data: Some(serde_json::json!({"key": "value"})),
            error: None,
        };
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("\"data\""));
        assert!(!json.contains("\"error\""));
    }

    #[test]
    fn test_task_response_error_omits_data() {
        let resp = TaskResponse {
            task_id: "t2".into(),
            status: "error".into(),
            data: None,
            error: Some("something failed".into()),
        };
        let json = serde_json::to_string(&resp).unwrap();
        assert!(!json.contains("\"data\""));
        assert!(json.contains("\"error\":\"something failed\""));
    }
}
