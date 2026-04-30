use async_trait::async_trait;
use serde_json::{json, Value};

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

pub struct LogParsePlugin;

#[async_trait]
impl Plugin for LogParsePlugin {
    fn name(&self) -> &str {
        "log_parse"
    }

    fn task_type(&self) -> &str {
        "plugin_log_parse"
    }

    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
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

        Ok(PluginResult {
            status: "ok".to_string(),
            summary: Some(json!({
                "line_count": lines,
                "error_count": errors,
                "warning_count": warnings,
            })),
            output: String::new(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_log_parse_counts() {
        let plugin = LogParsePlugin;
        let payload = json!({"text": "info: ok\nerror: bad\nwarning: hmm\nerror: again"});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.status, "ok");
        let summary = result.summary.unwrap();
        assert_eq!(summary["line_count"], 4);
        assert_eq!(summary["error_count"], 2);
        assert_eq!(summary["warning_count"], 1);
    }

    #[tokio::test]
    async fn test_log_parse_empty() {
        let plugin = LogParsePlugin;
        let payload = json!({"text": ""});
        let result = plugin.execute(&payload).await.unwrap();
        let summary = result.summary.unwrap();
        assert_eq!(summary["line_count"], 0);
    }

    #[tokio::test]
    async fn test_log_parse_missing_text() {
        let plugin = LogParsePlugin;
        let payload = json!({});
        let result = plugin.execute(&payload).await.unwrap();
        let summary = result.summary.unwrap();
        assert_eq!(summary["line_count"], 0);
    }

    #[test]
    fn test_metadata() {
        let plugin = LogParsePlugin;
        assert_eq!(plugin.name(), "log_parse");
        assert_eq!(plugin.task_type(), "plugin_log_parse");
    }
}
