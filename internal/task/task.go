package task

// AgentTask is the generic AI-agent collaboration task envelope.
type AgentTask struct {
	TaskID  string         `json:"task_id"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

const (
	TypeCollectMetrics    = "collect_metrics"
	TypeExecCommand       = "exec_command"
	TypeHealthCheck       = "health_check"
	TypePluginLogParse    = "plugin_log_parse"
	TypePluginTextProcess = "plugin_text_process"
	TypePluginEBPFCollect = "plugin_ebpf_collect"
	TypePluginFSScan      = "plugin_fs_scan"
	TypePluginConnAnalyze = "plugin_conn_analyze"
	TypePluginLocalProbe  = "plugin_local_probe"
	TypeSandboxExec       = "sandbox_exec"
)
