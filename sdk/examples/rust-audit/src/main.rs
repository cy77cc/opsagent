use async_trait::async_trait;
use opsagent_plugin::error::Result;
use opsagent_plugin::protocol::{TaskRequest, TaskResponse};
use opsagent_plugin::Plugin;
use serde_json::{json, Value};

struct AuditPlugin;

#[async_trait]
impl Plugin for AuditPlugin {
    fn task_types(&self) -> Vec<String> {
        vec!["audit".into()]
    }

    async fn init(&self, _config: Value) -> Result<()> {
        tracing_subscriber::fmt::init();
        tracing::info!("audit plugin initialized");
        Ok(())
    }

    async fn execute(&self, request: &TaskRequest) -> Result<TaskResponse> {
        tracing::info!(task_id = %request.task_id, "executing audit");

        let disk_usage = get_disk_usage();
        let memory_info = get_memory_info();

        Ok(TaskResponse {
            task_id: request.task_id.clone(),
            status: "ok".into(),
            data: Some(json!({
                "disk": disk_usage,
                "memory": memory_info,
            })),
            error: None,
        })
    }

    async fn shutdown(&self) -> Result<()> {
        tracing::info!("audit plugin shutting down");
        Ok(())
    }

    async fn health_check(&self) -> Result<()> {
        Ok(())
    }
}

fn get_disk_usage() -> Value {
    json!({"status": "ok", "note": "disk check placeholder"})
}

fn get_memory_info() -> Value {
    match std::fs::read_to_string("/proc/meminfo") {
        Ok(content) => {
            let total = parse_meminfo_field(&content, "MemTotal");
            let available = parse_meminfo_field(&content, "MemAvailable");
            json!({
                "total_kb": total,
                "available_kb": available,
            })
        }
        Err(e) => json!({"error": e.to_string()}),
    }
}

fn parse_meminfo_field(content: &str, field: &str) -> Option<u64> {
    for line in content.lines() {
        if line.starts_with(field) {
            return line
                .split_whitespace()
                .nth(1)
                .and_then(|s| s.parse().ok());
        }
    }
    None
}

#[tokio::main]
async fn main() -> Result<()> {
    opsagent_plugin::serve(AuditPlugin).await
}
