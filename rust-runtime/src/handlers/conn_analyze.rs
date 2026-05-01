use std::collections::HashMap;

use async_trait::async_trait;
use serde_json::{json, Value};
use tracing::warn;

use crate::error::PluginError;
use crate::plugin::{Plugin, PluginResult};

pub struct ConnAnalyzePlugin;

const TCP_STATE_MAP: &[(u8, &str)] = &[
    (0x01, "ESTABLISHED"),
    (0x02, "SYN_SENT"),
    (0x03, "SYN_RECV"),
    (0x04, "FIN_WAIT1"),
    (0x05, "FIN_WAIT2"),
    (0x06, "TIME_WAIT"),
    (0x07, "CLOSE"),
    (0x08, "CLOSE_WAIT"),
    (0x09, "LAST_ACK"),
    (0x0A, "LISTEN"),
    (0x0B, "CLOSING"),
];

fn tcp_state_name(code: u8) -> &'static str {
    TCP_STATE_MAP
        .iter()
        .find(|(c, _)| *c == code)
        .map(|(_, name)| *name)
        .unwrap_or("UNKNOWN")
}

fn parse_hex_ip(hex: &str) -> Option<String> {
    if hex.len() != 8 {
        return None;
    }
    // /proc/net/tcp stores IPs in little-endian byte order
    let b0 = u8::from_str_radix(&hex[6..8], 16).ok()?;
    let b1 = u8::from_str_radix(&hex[4..6], 16).ok()?;
    let b2 = u8::from_str_radix(&hex[2..4], 16).ok()?;
    let b3 = u8::from_str_radix(&hex[0..2], 16).ok()?;
    Some(format!("{}.{}.{}.{}", b0, b1, b2, b3))
}

fn parse_hex_port(hex: &str) -> Option<u16> {
    u16::from_str_radix(hex, 16).ok()
}

#[derive(Debug)]
struct Connection {
    protocol: String,
    state: String,
    _local_addr: String,
    local_port: u16,
    remote_addr: String,
    _remote_port: u16,
}

fn parse_proc_net(content: &str, protocol: &str) -> Vec<Connection> {
    let mut conns = Vec::new();
    for (i, line) in content.lines().enumerate() {
        if i == 0 {
            continue; // Skip header
        }
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.len() < 4 {
            continue;
        }

        let local_parts: Vec<&str> = fields[1].split(':').collect();
        let remote_parts: Vec<&str> = fields[2].split(':').collect();
        if local_parts.len() != 2 || remote_parts.len() != 2 {
            warn!(line = i, "skipping malformed line");
            continue;
        }

        let state_code = u8::from_str_radix(fields[3], 16).unwrap_or(0);
        let state = tcp_state_name(state_code).to_string();

        let local_addr = parse_hex_ip(local_parts[0]).unwrap_or_default();
        let local_port = parse_hex_port(local_parts[1]).unwrap_or(0);
        let remote_addr = parse_hex_ip(remote_parts[0]).unwrap_or_default();
        let remote_port = parse_hex_port(remote_parts[1]).unwrap_or(0);

        conns.push(Connection {
            protocol: protocol.to_string(),
            state,
            _local_addr: local_addr,
            local_port,
            remote_addr,
            _remote_port: remote_port,
        });
    }
    conns
}

#[async_trait]
impl Plugin for ConnAnalyzePlugin {
    fn name(&self) -> &str {
        "conn_analyze"
    }

    fn task_type(&self) -> &str {
        "plugin_conn_analyze"
    }

    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
        let include_states: Vec<String> = payload
            .get("include_states")
            .and_then(|v| v.as_array())
            .map(|arr| {
                arr.iter()
                    .filter_map(|v| v.as_str().map(|s| s.to_uppercase()))
                    .collect()
            })
            .unwrap_or_default();

        let top_n = payload
            .get("top_n")
            .and_then(|v| v.as_u64().map(|n| n as usize))
            .unwrap_or(10);

        let mut all_conns = Vec::new();

        for (file, proto) in &[
            ("/proc/net/tcp", "tcp"),
            ("/proc/net/tcp6", "tcp"),
            ("/proc/net/udp", "udp"),
            ("/proc/net/udp6", "udp"),
        ] {
            match std::fs::read_to_string(file) {
                Ok(content) => all_conns.extend(parse_proc_net(&content, proto)),
                Err(e) => {
                    return Err(PluginError::Resource(format!(
                        "failed to read {}: {}",
                        file, e
                    )));
                }
            }
        }

        // Filter by states if specified
        if !include_states.is_empty() {
            all_conns.retain(|c| {
                include_states
                    .iter()
                    .any(|s| s.eq_ignore_ascii_case(&c.state))
            });
        }

        let total = all_conns.len();

        // Group by state
        let mut by_state: HashMap<String, u64> = HashMap::new();
        for c in &all_conns {
            *by_state.entry(c.state.clone()).or_insert(0) += 1;
        }

        // Group by protocol
        let mut by_protocol: HashMap<String, u64> = HashMap::new();
        for c in &all_conns {
            *by_protocol.entry(c.protocol.clone()).or_insert(0) += 1;
        }

        // Top remote addresses
        let mut remote_counts: HashMap<String, u64> = HashMap::new();
        for c in &all_conns {
            if !c.remote_addr.is_empty() && c.remote_addr != "0.0.0.0" {
                *remote_counts.entry(c.remote_addr.clone()).or_insert(0) += 1;
            }
        }
        let mut top_remote: Vec<(String, u64)> = remote_counts.into_iter().collect();
        top_remote.sort_by_key(|b| std::cmp::Reverse(b.1));
        top_remote.truncate(top_n);

        // Top local ports
        let mut port_counts: HashMap<u16, u64> = HashMap::new();
        for c in &all_conns {
            if c.local_port > 0 {
                *port_counts.entry(c.local_port).or_insert(0) += 1;
            }
        }
        let mut top_ports: Vec<(u16, u64)> = port_counts.into_iter().collect();
        top_ports.sort_by_key(|b| std::cmp::Reverse(b.1));
        top_ports.truncate(top_n);

        Ok(PluginResult {
            status: "ok".to_string(),
            summary: Some(json!({
                "total_connections": total,
                "by_state": by_state,
                "by_protocol": by_protocol,
                "top_remote_addresses": top_remote.iter().map(|(addr, count)| {
                    json!({"address": addr, "count": count})
                }).collect::<Vec<_>>(),
                "top_local_ports": top_ports.iter().map(|(port, count)| {
                    json!({"port": port, "count": count})
                }).collect::<Vec<_>>(),
            })),
            output: String::new(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_hex_ip() {
        // Little-endian byte order: 0100007F -> 7F.00.00.01 = 127.0.0.1
        assert_eq!(parse_hex_ip("0100007F").unwrap(), "127.0.0.1");
        assert_eq!(parse_hex_ip("00000000").unwrap(), "0.0.0.0");
        // 0A00020F -> 0F.02.00.0A = 15.2.0.10
        assert_eq!(parse_hex_ip("0A00020F").unwrap(), "15.2.0.10");
    }

    #[test]
    fn test_parse_hex_ip_invalid() {
        assert!(parse_hex_ip("").is_none());
        assert!(parse_hex_ip("0100").is_none());
        assert!(parse_hex_ip("ZZZZZZZZ").is_none());
    }

    #[test]
    fn test_parse_hex_port() {
        assert_eq!(parse_hex_port("0050").unwrap(), 80);
        assert_eq!(parse_hex_port("1F90").unwrap(), 8080);
        assert_eq!(parse_hex_port("0016").unwrap(), 22);
    }

    #[test]
    fn test_tcp_state_name() {
        assert_eq!(tcp_state_name(0x01), "ESTABLISHED");
        assert_eq!(tcp_state_name(0x0A), "LISTEN");
        assert_eq!(tcp_state_name(0x06), "TIME_WAIT");
        assert_eq!(tcp_state_name(0x07), "CLOSE");
        assert_eq!(tcp_state_name(0xFF), "UNKNOWN");
    }

    #[test]
    fn test_parse_proc_net() {
        let content = "  sl  local_address rem_address   st tx_queue rx_queue\n\
                        0: 0100007F:0050 00000000:0000 0A 00000000:00000000\n\
                        1: 0100007F:1F90 0100007F:C000 01 00000000:00000000";
        let conns = parse_proc_net(content, "tcp");
        assert_eq!(conns.len(), 2);
        assert_eq!(conns[0].state, "LISTEN");
        assert_eq!(conns[0].local_port, 80);
        assert_eq!(conns[0]._local_addr, "127.0.0.1");
        assert_eq!(conns[1].state, "ESTABLISHED");
        assert_eq!(conns[1].local_port, 8080);
        assert_eq!(conns[1].remote_addr, "127.0.0.1");
    }

    #[test]
    fn test_parse_proc_net_skips_malformed() {
        let content = "header\nmalformed line\n0: 0100007F:0050 00000000:0000 0A 00000000:00000000";
        let conns = parse_proc_net(content, "udp");
        assert_eq!(conns.len(), 1);
        assert_eq!(conns[0].protocol, "udp");
    }

    #[tokio::test]
    async fn test_conn_analyze_real() {
        // This test requires /proc/net/tcp to exist (Linux only)
        if !std::path::Path::new("/proc/net/tcp").exists() {
            return;
        }
        let plugin = ConnAnalyzePlugin;
        let payload = json!({"top_n": 5});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.status, "ok");
        let summary = result.summary.unwrap();
        assert!(summary["total_connections"].as_u64().is_some());
        assert!(summary["by_state"].is_object());
        assert!(summary["by_protocol"].is_object());
    }

    #[tokio::test]
    async fn test_conn_analyze_with_state_filter() {
        if !std::path::Path::new("/proc/net/tcp").exists() {
            return;
        }
        let plugin = ConnAnalyzePlugin;
        let payload = json!({"include_states": ["LISTEN"], "top_n": 3});
        let result = plugin.execute(&payload).await.unwrap();
        assert_eq!(result.status, "ok");
        let summary = result.summary.unwrap();
        // All returned connections should be LISTEN state
        if let Some(by_state) = summary["by_state"].as_object() {
            for key in by_state.keys() {
                assert_eq!(key, "LISTEN");
            }
        }
    }

    #[test]
    fn test_metadata() {
        let plugin = ConnAnalyzePlugin;
        assert_eq!(plugin.name(), "conn_analyze");
        assert_eq!(plugin.task_type(), "plugin_conn_analyze");
    }
}
