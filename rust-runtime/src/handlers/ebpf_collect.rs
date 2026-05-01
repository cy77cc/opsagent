use async_trait::async_trait;
use serde_json::Value;

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

pub struct EbpfCollectPlugin;

impl EbpfCollectPlugin {
    pub fn new() -> Self {
        Self
    }
}

#[async_trait]
impl Plugin for EbpfCollectPlugin {
    fn name(&self) -> &str {
        "ebpf_collect"
    }

    fn task_type(&self) -> &str {
        "plugin_ebpf_collect"
    }

    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
        #[cfg(feature = "ebpf")]
        {
            execute_ebpf(payload).await
        }

        #[cfg(not(feature = "ebpf"))]
        {
            let _ = payload;
            Err(PluginError::Unsupported("eBPF support not compiled".into()))
        }
    }
}

#[cfg(feature = "ebpf")]
async fn execute_ebpf(payload: &Value) -> Result<PluginResult, PluginError> {
    use serde_json::json;

    // Check kernel version (>=5.4 required for BPF ring buffers)
    let uname = std::process::Command::new("uname")
        .arg("-r")
        .output()
        .map_err(|e| PluginError::Execution(format!("failed to run uname: {}", e)))?;
    let kernel_version = String::from_utf8_lossy(&uname.stdout);
    let major_minor: Vec<&str> = kernel_version.trim().split('.').collect();
    if major_minor.len() < 2 {
        return Err(PluginError::Execution("cannot parse kernel version".into()));
    }
    let major: u32 = major_minor[0].parse().unwrap_or(0);
    let minor: u32 = major_minor[1].parse().unwrap_or(0);
    if major < 5 || (major == 5 && minor < 4) {
        return Err(PluginError::Unsupported(format!(
            "eBPF requires kernel >= 5.4, got {}.{}",
            major, minor
        )));
    }

    // Check permissions (must be root or have CAP_BPF)
    let id_output = std::process::Command::new("id")
        .arg("-u")
        .output()
        .map_err(|e| PluginError::Execution(format!("failed to run id: {}", e)))?;
    let uid = String::from_utf8_lossy(&id_output.stdout)
        .trim()
        .parse::<u32>()
        .unwrap_or(1);
    if uid != 0 {
        return Err(PluginError::Resource(
            "eBPF requires root or CAP_BPF".into(),
        ));
    }

    let _duration = payload
        .get("duration_seconds")
        .and_then(|v| v.as_u64())
        .unwrap_or(10);

    // TODO: Implement real eBPF syscall counting with aya
    Ok(PluginResult {
        status: "ok".to_string(),
        summary: Some(json!({
            "syscall_counts": {},
            "collection_duration_ms": 0,
            "note": "eBPF handler compiled but not yet implemented"
        })),
        output: String::new(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    async fn test_ebpf_unsupported_without_feature() {
        #[cfg(not(feature = "ebpf"))]
        {
            let plugin = EbpfCollectPlugin::new();
            let payload = json!({"programs": ["syscall_count"], "duration_seconds": 1});
            let err = plugin.execute(&payload).await.unwrap_err();
            match err {
                PluginError::Unsupported(msg) => assert!(msg.contains("not compiled")),
                _ => panic!("expected Unsupported error"),
            }
        }
    }

    #[test]
    fn test_metadata() {
        let plugin = EbpfCollectPlugin::new();
        assert_eq!(plugin.name(), "ebpf_collect");
        assert_eq!(plugin.task_type(), "plugin_ebpf_collect");
    }
}
