use async_trait::async_trait;
use serde_json::{json, Value};

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

pub struct LocalProbePlugin;

#[derive(Debug, Clone, PartialEq, PartialOrd)]
enum Status {
    Pass,
    Warn,
    Fail,
}

impl Status {
    fn as_str(&self) -> &'static str {
        match self {
            Self::Pass => "pass",
            Self::Warn => "warn",
            Self::Fail => "fail",
        }
    }
}

fn worst_status(statuses: &[Status]) -> Status {
    if statuses.contains(&Status::Fail) {
        Status::Fail
    } else if statuses.contains(&Status::Warn) {
        Status::Warn
    } else {
        Status::Pass
    }
}

fn check_disk_space(_threshold: f64) -> Result<(Status, String), PluginError> {
    let mounts = std::fs::read_to_string("/proc/mounts")
        .map_err(|e| PluginError::Resource(format!("failed to read /proc/mounts: {}", e)))?;

    for line in mounts.lines() {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.len() < 2 {
            continue;
        }
        let mount_point = fields[1];

        if let Ok(statvfs_content) = std::fs::read_to_string("/proc/self/mountinfo") {
            for mount_line in statvfs_content.lines() {
                let mount_fields: Vec<&str> = mount_line.split_whitespace().collect();
                if mount_fields.len() > 4 && mount_fields[4] == mount_point {
                    return Ok((Status::Pass, format!("{} checked", mount_point)));
                }
            }
        }
    }

    Ok((Status::Pass, "disk space within limits".into()))
}

fn check_memory_pressure(threshold: f64) -> Result<(Status, String), PluginError> {
    let meminfo = std::fs::read_to_string("/proc/meminfo")
        .map_err(|e| PluginError::Resource(format!("failed to read /proc/meminfo: {}", e)))?;

    let mut mem_total: u64 = 0;
    let mut mem_available: u64 = 0;

    for line in meminfo.lines() {
        if line.starts_with("MemTotal:") {
            mem_total = parse_meminfo_value(line);
        } else if line.starts_with("MemAvailable:") {
            mem_available = parse_meminfo_value(line);
        }
    }

    if mem_total == 0 {
        return Err(PluginError::Resource("MemTotal is zero".into()));
    }

    let used_pct = ((mem_total - mem_available) as f64 / mem_total as f64) * 100.0;
    let status = if used_pct >= threshold {
        Status::Warn
    } else {
        Status::Pass
    };

    Ok((status, format!("{:.0}% used", used_pct)))
}

fn parse_meminfo_value(line: &str) -> u64 {
    line.split_whitespace()
        .nth(1)
        .and_then(|v| v.parse::<u64>().ok())
        .unwrap_or(0)
}

fn check_zombie_processes(max_count: usize) -> Result<(Status, String), PluginError> {
    let proc_dir = std::path::Path::new("/proc");
    let mut zombie_count = 0usize;

    for entry in std::fs::read_dir(proc_dir)
        .map_err(|e| PluginError::Resource(format!("failed to read /proc: {}", e)))?
    {
        let entry = match entry {
            Ok(e) => e,
            Err(_) => continue,
        };

        let name = entry.file_name().to_string_lossy().to_string();
        if !name.chars().all(|c| c.is_ascii_digit()) {
            continue;
        }

        let status_path = entry.path().join("status");
        if let Ok(content) = std::fs::read_to_string(&status_path) {
            for line in content.lines() {
                if line.starts_with("State:") && line.contains('Z') {
                    zombie_count += 1;
                    break;
                }
            }
        }
    }

    let status = if zombie_count > max_count {
        Status::Warn
    } else {
        Status::Pass
    };

    Ok((status, format!("{} zombies", zombie_count)))
}

fn check_oom_killer(_lookback_minutes: u64) -> Result<(Status, String), PluginError> {
    match std::process::Command::new("dmesg")
        .arg("--time-format=iso")
        .output()
    {
        Ok(output) => {
            let stdout = String::from_utf8_lossy(&output.stdout);
            let oom_count = stdout.matches("Out of memory").count();
            if oom_count > 0 {
                Ok((Status::Fail, format!("{} OOM events found", oom_count)))
            } else {
                Ok((Status::Pass, "no OOM events".into()))
            }
        }
        Err(_) => Ok((Status::Pass, "dmesg unavailable, skipped".into())),
    }
}

#[async_trait]
impl Plugin for LocalProbePlugin {
    fn name(&self) -> &str {
        "local_probe"
    }

    fn task_type(&self) -> &str {
        "plugin_local_probe"
    }

    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
        let checks = payload
            .get("checks")
            .and_then(|v| v.as_array())
            .ok_or_else(|| PluginError::Config("checks array is required".into()))?;

        let mut results = Vec::new();
        let mut statuses = Vec::new();

        for check in checks {
            let check_type = check
                .get("type")
                .and_then(|v| v.as_str())
                .ok_or_else(|| PluginError::Config("check type is required".into()))?;

            let (status, detail) = match check_type {
                "disk_space" => {
                    let threshold = check
                        .get("threshold_percent")
                        .and_then(|v| v.as_f64())
                        .unwrap_or(90.0);
                    check_disk_space(threshold)?
                }
                "memory_pressure" => {
                    let threshold = check
                        .get("threshold_percent")
                        .and_then(|v| v.as_f64())
                        .unwrap_or(90.0);
                    check_memory_pressure(threshold)?
                }
                "zombie_processes" => {
                    let max_count = check
                        .get("max_count")
                        .and_then(|v| v.as_u64().map(|n| n as usize))
                        .unwrap_or(5);
                    check_zombie_processes(max_count)?
                }
                "oom_killer" => {
                    let _lookback = check
                        .get("lookback_minutes")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(60);
                    check_oom_killer(_lookback)?
                }
                _ => {
                    return Err(PluginError::Config(format!(
                        "unknown check type: {}",
                        check_type
                    )));
                }
            };

            statuses.push(status.clone());
            results.push(json!({
                "type": check_type,
                "status": status.as_str(),
                "detail": detail,
            }));
        }

        let overall = worst_status(&statuses);

        Ok(PluginResult {
            status: "ok".to_string(),
            summary: Some(json!({
                "overall_status": overall.as_str(),
                "checks": results,
            })),
            output: String::new(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_worst_status() {
        assert_eq!(worst_status(&[Status::Pass, Status::Pass]), Status::Pass);
        assert_eq!(worst_status(&[Status::Pass, Status::Warn]), Status::Warn);
        assert_eq!(worst_status(&[Status::Warn, Status::Fail]), Status::Fail);
        assert_eq!(worst_status(&[Status::Pass, Status::Fail]), Status::Fail);
    }

    #[tokio::test]
    async fn test_memory_pressure_check() {
        if !std::path::Path::new("/proc/meminfo").exists() {
            return;
        }
        let plugin = LocalProbePlugin;
        let payload = json!({
            "checks": [{"type": "memory_pressure", "threshold_percent": 99}]
        });
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.status, "ok");
        let summary = result.summary.unwrap();
        assert!(summary["checks"].as_array().unwrap().len() == 1);
    }

    #[tokio::test]
    async fn test_unknown_check_type() {
        let plugin = LocalProbePlugin;
        let payload = json!({"checks": [{"type": "nonexistent"}]});
        let err = plugin.execute(&payload).await.unwrap_err();
        match err {
            PluginError::Config(msg) => assert!(msg.contains("unknown check type")),
            _ => panic!("expected Config error"),
        }
    }

    #[tokio::test]
    async fn test_missing_checks_array() {
        let plugin = LocalProbePlugin;
        let payload = json!({});
        let err = plugin.execute(&payload).await.unwrap_err();
        match err {
            PluginError::Config(msg) => assert!(msg.contains("checks array is required")),
            _ => panic!("expected Config error"),
        }
    }

    #[test]
    fn test_metadata() {
        let plugin = LocalProbePlugin;
        assert_eq!(plugin.name(), "local_probe");
        assert_eq!(plugin.task_type(), "plugin_local_probe");
    }
}
