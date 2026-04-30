use async_trait::async_trait;
use serde_json::Value;

use crate::error::PluginError;

pub struct PluginResult {
    pub status: String,
    pub summary: Option<Value>,
    pub output: String,
}

#[async_trait]
pub trait Plugin: Send + Sync {
    fn name(&self) -> &str;
    fn task_type(&self) -> &str;
    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError>;
}
