use async_trait::async_trait;
use serde_json::{json, Value};

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

pub struct TextProcessPlugin;

#[async_trait]
impl Plugin for TextProcessPlugin {
    fn name(&self) -> &str {
        "text_process"
    }

    fn task_type(&self) -> &str {
        "plugin_text_process"
    }

    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
        let text = payload
            .get("text")
            .and_then(|v| v.as_str())
            .unwrap_or_default();
        let op = payload
            .get("operation")
            .and_then(|v| v.as_str())
            .unwrap_or("uppercase");

        match op {
            "uppercase" => Ok(PluginResult {
                status: "ok".to_string(),
                summary: Some(json!({"operation": op})),
                output: text.to_uppercase(),
            }),
            "lowercase" => Ok(PluginResult {
                status: "ok".to_string(),
                summary: Some(json!({"operation": op})),
                output: text.to_lowercase(),
            }),
            "word_count" => {
                let words = text.split_whitespace().count();
                Ok(PluginResult {
                    status: "ok".to_string(),
                    summary: Some(json!({"operation": op, "word_count": words})),
                    output: words.to_string(),
                })
            }
            _ => Err(PluginError::Execution(format!(
                "unsupported operation: {}",
                op
            ))),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_uppercase() {
        let plugin = TextProcessPlugin;
        let payload = json!({"text": "hello world", "operation": "uppercase"});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.status, "ok");
        assert_eq!(result.output, "HELLO WORLD");
    }

    #[tokio::test]
    async fn test_lowercase() {
        let plugin = TextProcessPlugin;
        let payload = json!({"text": "HELLO", "operation": "lowercase"});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.output, "hello");
    }

    #[tokio::test]
    async fn test_word_count() {
        let plugin = TextProcessPlugin;
        let payload = json!({"text": "one two three", "operation": "word_count"});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.output, "3");
        assert_eq!(result.summary.unwrap()["word_count"], 3);
    }

    #[tokio::test]
    async fn test_unsupported_operation() {
        let plugin = TextProcessPlugin;
        let payload = json!({"text": "hi", "operation": "reverse"});
        let err = plugin.execute(&payload).await.unwrap_err();
        match err {
            PluginError::Execution(msg) => assert!(msg.contains("unsupported operation")),
            _ => panic!("expected Execution error"),
        }
    }

    #[tokio::test]
    async fn test_missing_operation_defaults_uppercase() {
        let plugin = TextProcessPlugin;
        let payload = json!({"text": "hi"});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.output, "HI");
    }

    #[test]
    fn test_metadata() {
        let plugin = TextProcessPlugin;
        assert_eq!(plugin.name(), "text_process");
        assert_eq!(plugin.task_type(), "plugin_text_process");
    }
}
