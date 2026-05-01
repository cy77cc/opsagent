# OpsAgent 更新日志

本文档记录 OpsAgent 项目的版本变更历史，按日期倒序排列。

---

## 2026-05-01 -- 安全加固冲刺

本次更新对 OpsAgent 多个子系统进行了全面的安全加固，涵盖认证授权、输入验证、沙箱隔离、网络通信等各个层面。

### 全局安全加固

- 对多个子系统实施综合性安全加固措施

### 认证与授权

- 默认启用认证功能，限制仅监听 localhost，令牌设置最小长度要求
- 使用常量时间比较算法验证 Bearer 令牌，防止时序攻击
- 防止通过热重载机制禁用认证功能

### 输入验证与请求安全

- 在执行和任务端点上限制请求体大小为 1MB
- 验证命令参数中的空字节和长度限制
- 对 TaskID 进行清理，防止沙箱文件操作中的路径遍历攻击
- 将 TaskID 清理扩展到所有入口点，提高测试覆盖率
- 清理 API 响应中的错误消息，防止信息泄露

### 沙箱安全

- 拒绝未知解释器，不再传递原始输入
- 向沙箱策略中补充遗漏的 shell 元字符
- 使用不可预测的临时路径，审计日志权限限制为 0600
- 移除 /etc 挂载，添加资源限制，检查命令名称中的元字符
- 使 seccomp 策略动态化，移除 clone/fork/vfork 以防止 fork 炸弹
- 清理传递给沙箱的环境变量，阻止 LD_PRELOAD
- 防止插件二进制路径遍历，清理 socket 路径，阻止符号链接攻击
- 清理插件环境变量，socket 权限设置为 0600

### 网络与通信安全

- 在传递给 iptables 之前验证 IP 地址
- 拒绝不安全的 gRPC 连接，强制 TLS 1.2+，设置 ServerName
- 验证 HTTP 输出 URL 协议方案，对未认证的健康端点隐藏版本信息

### HTTP 服务安全

- 添加安全响应头，限制 HTTP 方法，超时上限设为 300 秒
- 添加基于 IP 的速率限制中间件（10 请求/秒，突发 20）

### 配置与文件安全

- 在配置差异中掩码敏感信息，文件权限设为 0600，ConfigUpdate 设置大小限制
- 转义 Prometheus 标签值，默认绑定到 127.0.0.1:9100

### 系统服务安全

- 在 systemd 服务单元中设置 NoNewPrivileges=true
- 添加 FsScan 路径白名单，限制 dmesg 输出，要求 socket 环境变量

### 测试

- 为 HTTP 和 stdout 报告器、沙箱执行器和服务端处理器添加全面测试
- 修复请求体大小限制测试，确保回归测试有效性

---

## 2026-04-30 -- SDK 与代码审查

本次更新引入了插件 SDK（Go 和 Rust）、指标系统、审计日志、健康检查等核心基础设施，并完成了全面的代码审查改进。

### 插件 SDK

- 新增 Go 插件 SDK，提供 Handler 接口和 Serve 函数
- 新增 Rust 插件 SDK，提供 Plugin trait 和 serve 函数
- 新增 Go echo 示例插件
- 新增 Rust 审计示例插件

### 插件网关与运行时

- 新增 PluginGateway 接口和 PluginInfo 类型
- 新增 PluginGatewayConfig，包含默认值和验证逻辑
- 将 PluginGateway 接入 Agent 生命周期
- 新增 PluginManifest 类型、解析和验证
- 新增插件清单文件监视器，支持防抖
- 为 PluginGateway 添加端到端集成测试

### 指标系统

- 使用 client_golang registry 替换手写的 Prometheus 指标
- 新增 IncPipelineErrors 和 IncPluginRequests 便捷方法
- 在 Agent 中接入 IncMetricsCollected 和 IncPluginRequests 计数器

### 审计日志

- 新增结构化 JSON-lines 审计日志器，支持日志轮转
- 新增 gRPC 连接/断开审计事件
- 新增配置、插件、任务取消和沙箱审计事件

### 健康检查

- 为子系统添加 HealthStatus，增强 /healthz 端点
- 在健康状态中添加 last_collection 字段
- 从 Agent 传播版本信息到健康端点

### Agent 核心

- 将指标、审计、健康检查、版本信息和 RunOnce 接入 Agent
- 在 RunOnce 输出中使用 totalFields

### CI/CD 与工程化

- 添加 80% 覆盖率门禁、Rust CI 任务和集成测试任务
- 更新 Go 和 Rust 的 CI 配置，添加缓存和代码检查
- 添加 FORCE_JAVASCRIPT_ACTIONS_TO_NODE24 环境变量
- 为集成任务添加 Rust 依赖
- 添加 Prometheus client_golang、lumberjack、testify 依赖，更新 Makefile
- 将 lumberjack 提升为直接依赖
- 为指标收集和管道处理添加基准测试

### 文档与代码清理

- 新增插件契约文档
- 新增代码审查修复设计规范
- 改善多个文件的代码可读性和组织结构
- 从 Git 中移除 Go 示例二进制文件和 Rust target 目录，更新 .gitignore
- 清理 go.sum 中 testify 的间接依赖

---

## 2026-04-29 -- 重大功能日

多个并行工作流合并，涵盖 Rust 运行时处理器、新采集插件、配置热重载、优雅关停等核心功能。

### Rust 运行时处理器

新增 6 个处理器实现：
- **log_parse** -- 日志解析
- **text_process** -- 文本处理
- **fs_scan** -- 文件系统扫描
- **ebpf_collect** -- eBPF 数据采集
- **conn_analyze** -- 连接分析
- **local_probe** -- 本地探测

### 采集器新输入插件

- **connections** -- 网络连接采集
- **load** -- 系统负载采集
- **gpu** -- GPU 指标采集
- **temp** -- 温度传感器采集
- **diskio** -- 磁盘 I/O 采集

### 聚合器

- 新增百分位聚合器（percentile aggregator）

### 配置热重载

- 新增 ConfigReloader，支持配置热重载
- 实现原子回滚机制
- 新增配置差异引擎（Diff engine）

### 应用生命周期

- 实现优雅关停（graceful shutdown）
- 新增 SIGHUP 信号处理器

### gRPC 客户端

- 新增 FlushAndStop 功能
- 实现缓存持久化

### 子系统重载

- 为 Collector、Server、Reporter 添加 Reloader 实现

### 工程质量

- 提升测试覆盖率
- 代码重构
- Agent 依赖注入改进

---

## 2026-04-28 -- 初始版本发布

OpsAgent 项目首次发布，包含完整的采集管道、沙箱执行、gRPC 通信和 HTTP 服务等核心功能。

### 项目基础设施

- 项目初始化提交
- Proto 定义与 gRPC 基础框架搭建
- 基于 Cobra 的命令行接口（CLI）

### 沙箱执行器

- nsjail 配置与安全策略
- 网络隔离
- cgroup 资源统计
- 审计日志记录
- 输出流式传输
- 沙箱执行器核心实现

### 采集管道

- Metric 数据模型定义
- 插件接口规范
- 插件注册中心
- 指标累加器（Accumulator）
- 指标缓冲区（Buffer）
- 采集调度器（Scheduler）

### 输入插件

- **cpu** -- CPU 使用率采集
- **memory** -- 内存使用采集
- **disk** -- 磁盘使用采集
- **net** -- 网络流量采集
- **process** -- 进程信息采集

### 处理器插件

- **tagger** -- 标签添加器
- **regex** -- 正则表达式处理器

### 聚合器插件

- **avg** -- 平均值聚合
- **sum** -- 求和聚合

### 输出插件

- **http** -- HTTP 输出
- **prometheus** -- Prometheus 指标暴露
- **promrw** -- Prometheus Remote Write 输出

### gRPC 客户端

- 数据发送器（Sender）
- 数据接收器（Receiver）
- 数据缓存（Cache）
- 断线重连（Reconnect）

### HTTP 服务

- HTTP 服务端与处理器实现

### 测试

- 集成测试套件
