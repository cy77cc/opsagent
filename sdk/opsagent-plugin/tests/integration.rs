use async_trait::async_trait;
use opsagent_plugin::error::Result;
use opsagent_plugin::protocol::{TaskRequest, TaskResponse};
use opsagent_plugin::Plugin;
use serde_json::Value;

struct EchoPlugin;

#[async_trait]
impl Plugin for EchoPlugin {
    fn task_types(&self) -> Vec<String> {
        vec!["echo".into()]
    }

    async fn init(&self, _cfg: Value) -> Result<()> {
        Ok(())
    }

    async fn execute(&self, req: &TaskRequest) -> Result<TaskResponse> {
        Ok(TaskResponse {
            task_id: req.task_id.clone(),
            status: "ok".into(),
            data: Some(req.params.clone()),
            error: None,
        })
    }

    async fn shutdown(&self) -> Result<()> {
        Ok(())
    }

    async fn health_check(&self) -> Result<()> {
        Ok(())
    }
}

#[tokio::test]
async fn test_echo_plugin_execute() {
    let plugin = EchoPlugin;

    let req = TaskRequest {
        task_id: "task-1".into(),
        task_type: "echo".into(),
        params: serde_json::json!({"msg": "hello"}),
        deadline_ms: 5000,
    };

    let resp = plugin.execute(&req).await.unwrap();
    assert_eq!(resp.task_id, "task-1");
    assert_eq!(resp.status, "ok");
    assert!(resp.error.is_none());

    let data = resp.data.unwrap();
    assert_eq!(data["msg"], "hello");
}

#[tokio::test]
async fn test_echo_plugin_task_types() {
    let plugin = EchoPlugin;
    let types = plugin.task_types();
    assert_eq!(types, vec!["echo"]);
}

#[tokio::test]
async fn test_echo_plugin_init_and_shutdown() {
    let plugin = EchoPlugin;
    plugin.init(serde_json::Value::Null).await.unwrap();
    plugin.shutdown().await.unwrap();
}

#[tokio::test]
async fn test_echo_plugin_health_check() {
    let plugin = EchoPlugin;
    plugin.health_check().await.unwrap();
}
