use std::cmp::Reverse;
use std::collections::{BinaryHeap, HashMap};
use std::path::Path;
use std::time::Instant;

use async_trait::async_trait;
use serde_json::{json, Value};
use walkdir::WalkDir;

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

const MAX_FILES: usize = 1_000_000;
const ALLOWED_ROOTS: &[&str] = &["/var/log", "/opt", "/srv", "/tmp"];

pub struct FsScanPlugin {
    default_max_depth: usize,
}

impl FsScanPlugin {
    pub fn new() -> Self {
        Self {
            default_max_depth: 5,
        }
    }
}

#[async_trait]
impl Plugin for FsScanPlugin {
    fn name(&self) -> &str {
        "fs_scan"
    }

    fn task_type(&self) -> &str {
        "plugin_fs_scan"
    }

    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
        let root_path = payload
            .get("root_path")
            .and_then(|v| v.as_str())
            .ok_or_else(|| PluginError::Config("root_path is required".into()))?;

        if !Path::new(root_path).exists() {
            return Err(PluginError::Config(format!(
                "root_path does not exist: {}",
                root_path
            )));
        }

        let canonical = std::fs::canonicalize(root_path)
            .map_err(|_| PluginError::Config(format!("root_path does not exist or is not accessible: {}", root_path)))?;
        let is_allowed = ALLOWED_ROOTS.iter().any(|allowed| canonical.starts_with(allowed));
        if !is_allowed {
            return Err(PluginError::Config(format!(
                "root_path {} is not under allowed roots: {:?}", root_path, ALLOWED_ROOTS
            )));
        }

        let max_depth = payload
            .get("max_depth")
            .and_then(|v| v.as_u64())
            .map(|n| n as usize)
            .unwrap_or(self.default_max_depth);

        let min_size = payload
            .get("min_size_bytes")
            .and_then(|v| v.as_u64())
            .unwrap_or(0);

        let include_patterns: Vec<String> = payload
            .get("include_patterns")
            .and_then(|v| v.as_array())
            .map(|arr| {
                arr.iter()
                    .filter_map(|v| v.as_str().map(|s| s.to_string()))
                    .collect()
            })
            .unwrap_or_default();

        let compiled_patterns: Vec<glob::Pattern> = include_patterns
            .iter()
            .map(|pat| {
                glob::Pattern::new(pat)
                    .map_err(|_| PluginError::Config(format!("invalid glob pattern: {}", pat)))
            })
            .collect::<Result<Vec<_>, _>>()?;

        let started = Instant::now();
        let mut total_files: u64 = 0;
        let mut total_size: u64 = 0;
        let mut by_extension: HashMap<String, u64> = HashMap::new();
        let mut largest: BinaryHeap<Reverse<(u64, String)>> = BinaryHeap::new();

        for entry in WalkDir::new(root_path)
            .max_depth(max_depth)
            .follow_links(false)
            .into_iter()
            .filter_entry(|e| e.file_type().is_file() || e.file_type().is_dir())
        {
            let entry = match entry {
                Ok(e) => e,
                Err(_) => continue,
            };

            if !entry.file_type().is_file() {
                continue;
            }

            let path = entry.path();
            let metadata = match entry.metadata() {
                Ok(m) => m,
                Err(_) => continue,
            };

            let size = metadata.len();

            if !compiled_patterns.is_empty() {
                let file_name = entry.file_name().to_string_lossy();
                if !compiled_patterns.iter().any(|p| p.matches(&file_name)) {
                    continue;
                }
            }

            if size < min_size {
                continue;
            }

            total_files += 1;
            total_size += size;

            if total_files > MAX_FILES as u64 {
                return Err(PluginError::Resource(
                    "too many files: safety limit exceeded".into(),
                ));
            }

            if let Some(ext) = path.extension() {
                let ext_str = format!(".{}", ext.to_string_lossy());
                *by_extension.entry(ext_str).or_insert(0) += 1;
            }

            let path_str = path.to_string_lossy().to_string();
            if largest.len() < 10 {
                largest.push(Reverse((size, path_str)));
            } else if let Some(Reverse((min_size, _))) = largest.peek() {
                if size > *min_size {
                    largest.pop();
                    largest.push(Reverse((size, path_str)));
                }
            }
        }

        let scan_duration_ms = started.elapsed().as_millis() as u64;

        let mut largest: Vec<(String, u64)> = largest
            .into_sorted_vec()
            .into_iter()
            .map(|Reverse((size, path))| (path, size))
            .collect();
        largest.reverse(); // largest first

        let largest_files: Vec<Value> = largest
            .iter()
            .map(|(path, size)| json!({"path": path, "size_bytes": size}))
            .collect();

        Ok(PluginResult {
            status: "ok".to_string(),
            summary: Some(json!({
                "total_files": total_files,
                "total_size_bytes": total_size,
                "by_extension": by_extension,
                "largest_files": largest_files,
                "scan_duration_ms": scan_duration_ms,
            })),
            output: String::new(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use tempfile::TempDir;

    fn setup_test_dir() -> TempDir {
        let dir = TempDir::new().unwrap();
        fs::write(dir.path().join("a.log"), "hello world").unwrap();
        fs::write(dir.path().join("b.txt"), "short").unwrap();
        fs::write(dir.path().join("c.log"), "another log").unwrap();
        fs::create_dir(dir.path().join("sub")).unwrap();
        fs::write(dir.path().join("sub/d.log"), "deep log").unwrap();
        dir
    }

    #[tokio::test]
    async fn test_fs_scan_basic() {
        let dir = setup_test_dir();
        let plugin = FsScanPlugin::new();
        let payload = json!({"root_path": dir.path().to_str().unwrap()});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.status, "ok");
        let summary = result.summary.unwrap();
        assert_eq!(summary["total_files"], 4);
        assert!(summary["total_size_bytes"].as_u64().unwrap() > 0);
    }

    #[tokio::test]
    async fn test_fs_scan_with_pattern() {
        let dir = setup_test_dir();
        let plugin = FsScanPlugin::new();
        let payload = json!({
            "root_path": dir.path().to_str().unwrap(),
            "include_patterns": ["*.log"]
        });
        let result = plugin.execute(&payload).await.unwrap();
        let summary = result.summary.unwrap();
        assert_eq!(summary["total_files"], 3);
    }

    #[tokio::test]
    async fn test_fs_scan_with_max_depth() {
        let dir = setup_test_dir();
        let plugin = FsScanPlugin::new();
        let payload = json!({
            "root_path": dir.path().to_str().unwrap(),
            "max_depth": 1
        });
        let result = plugin.execute(&payload).await.unwrap();
        let summary = result.summary.unwrap();
        assert_eq!(summary["total_files"], 3);
    }

    #[tokio::test]
    async fn test_fs_scan_nonexistent_path() {
        let plugin = FsScanPlugin::new();
        let payload = json!({"root_path": "/nonexistent/path/xyz"});
        let err = plugin.execute(&payload).await.unwrap_err();
        match err {
            PluginError::Config(msg) => assert!(msg.contains("does not exist")),
            _ => panic!("expected Config error"),
        }
    }

    #[tokio::test]
    async fn test_fs_scan_missing_root_path() {
        let plugin = FsScanPlugin::new();
        let payload = json!({});
        let err = plugin.execute(&payload).await.unwrap_err();
        match err {
            PluginError::Config(msg) => assert!(msg.contains("root_path is required")),
            _ => panic!("expected Config error"),
        }
    }

    #[test]
    fn test_metadata() {
        let plugin = FsScanPlugin::new();
        assert_eq!(plugin.name(), "fs_scan");
        assert_eq!(plugin.task_type(), "plugin_fs_scan");
    }
}
