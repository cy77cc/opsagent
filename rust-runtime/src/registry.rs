use std::collections::HashMap;
use serde_json::Value;

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

pub struct PluginRegistry {
    plugins: HashMap<String, Box<dyn Plugin>>,
}

impl PluginRegistry {
    pub fn new() -> Self {
        Self {
            plugins: HashMap::new(),
        }
    }

    pub fn register(&mut self, plugin: Box<dyn Plugin>) {
        let task_type = plugin.task_type().to_string();
        self.plugins.insert(task_type, plugin);
    }

    pub async fn dispatch(
        &self,
        task_type: &str,
        payload: &Value,
    ) -> Result<PluginResult, PluginError> {
        match self.plugins.get(task_type) {
            Some(plugin) => plugin.execute(payload).await,
            None => Err(PluginError::Execution(format!(
                "unsupported task type: {}",
                task_type
            ))),
        }
    }

    pub fn registered_types(&self) -> Vec<&str> {
        self.plugins.keys().map(|s| s.as_str()).collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use async_trait::async_trait;

    struct MockPlugin {
        task_type_str: String,
        result_status: String,
    }

    impl MockPlugin {
        fn new(task_type: &str, status: &str) -> Self {
            Self {
                task_type_str: task_type.to_string(),
                result_status: status.to_string(),
            }
        }
    }

    #[async_trait]
    impl Plugin for MockPlugin {
        fn name(&self) -> &str {
            "mock"
        }

        fn task_type(&self) -> &str {
            &self.task_type_str
        }

        async fn execute(&self, _payload: &Value) -> Result<PluginResult, PluginError> {
            Ok(PluginResult {
                status: self.result_status.clone(),
                summary: None,
                output: String::new(),
            })
        }
    }

    #[tokio::test]
    async fn test_register_and_dispatch() {
        let mut reg = PluginRegistry::new();
        reg.register(Box::new(MockPlugin::new("test_task", "ok")));

        let result = reg.dispatch("test_task", &Value::Null).await.unwrap();
        assert_eq!(result.status, "ok");
    }

    #[tokio::test]
    async fn test_dispatch_unknown_type() {
        let reg = PluginRegistry::new();
        let err = reg.dispatch("unknown", &Value::Null).await.unwrap_err();
        match err {
            PluginError::Execution(msg) => assert!(msg.contains("unsupported task type")),
            _ => panic!("expected Execution error"),
        }
    }

    #[tokio::test]
    async fn test_registered_types() {
        let mut reg = PluginRegistry::new();
        reg.register(Box::new(MockPlugin::new("task_a", "ok")));
        reg.register(Box::new(MockPlugin::new("task_b", "ok")));

        let mut types = reg.registered_types();
        types.sort();
        assert_eq!(types, vec!["task_a", "task_b"]);
    }

    #[tokio::test]
    async fn test_concurrent_dispatch() {
        let mut reg = PluginRegistry::new();
        reg.register(Box::new(MockPlugin::new("task_c", "ok")));

        let reg = std::sync::Arc::new(reg);
        let mut handles = vec![];
        for _ in 0..10 {
            let r = reg.clone();
            handles.push(tokio::spawn(async move {
                r.dispatch("task_c", &Value::Null).await
            }));
        }
        for h in handles {
            let result = h.await.unwrap().unwrap();
            assert_eq!(result.status, "ok");
        }
    }
}
