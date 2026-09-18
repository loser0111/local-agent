package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ===== 模板渲染：{{参数名}} 占位替换 =====

// renderTemplate 将模板中的 {{key}} 替换为 args 中对应的值
func renderTemplate(tpl string, args map[string]interface{}) string {
	out := tpl
	for k, v := range args {
		out = strings.ReplaceAll(out, "{{"+k+"}}", fmt.Sprintf("%v", v))
	}
	return out
}

// ===== 动态 CLI 工具 =====

// DynamicCLITool 用户自定义命令行工具（命令模板 + 自定义参数）
type DynamicCLITool struct {
	*BaseTool
	src *ToolSource
	// Dir 工作目录（会话项目目录），为空时继承进程工作目录
	Dir string
}

func NewDynamicCLITool(src *ToolSource) *DynamicCLITool {
	t := &DynamicCLITool{src: src}
	t.BaseTool = &BaseTool{
		Name:        src.Name,
		Description: src.Description,
		Parameters:  paramsFromConfig(src.Parameters),
	}
	return t
}

// RequiredParams 取配置声明的必填参数（见 requiredFromConfig）
func (t *DynamicCLITool) RequiredParams() []string {
	if t.src == nil {
		return nil
	}
	return requiredFromConfig(t.src.Parameters)
}

func (t *DynamicCLITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	// 配置已是类型化字段，不再需要每次 Unmarshal
	if t.src == nil || t.src.CLI == nil {
		return "", fmt.Errorf("CLI 配置缺失")
	}
	if strings.TrimSpace(t.src.CLI.Command) == "" {
		return "", fmt.Errorf("命令模板为空")
	}
	cmdLine := renderTemplate(t.src.CLI.Command, args)

	timeout := t.src.CLI.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "powershell", "-Command", cmdLine)
	} else {
		cmd = exec.CommandContext(cctx, "bash", "-c", cmdLine)
	}
	hideConsoleWindow(cmd) // Windows 上不弹控制台窗口（见该函数说明）
	if t.Dir != "" {
		cmd.Dir = t.Dir
	}
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("命令执行超时（%ds）", timeout)
	}
	return string(out), err
}

// PermissionSubject 声明判定主体：把参数模板展开成真正要执行的命令后再判定。
// 若配置无法解析，返回不可信主体（trusted=false），判定会落到"询问"。
func (t *DynamicCLITool) PermissionSubject(args map[string]interface{}) Subject {
	if t.src == nil || t.src.CLI == nil || strings.TrimSpace(t.src.CLI.Command) == "" {
		s := newRawSubject(t.GetName(), "CLI 工具配置缺失")
		s.Trusted = false
		return s
	}
	return newCommandSubject(t.GetName(), renderTemplate(t.src.CLI.Command, args))
}

// ===== 动态 HTTP API 工具 =====

// headerTransport 给每个请求注入固定请求头
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// DynamicAPITool 用户自定义 HTTP API 工具
type DynamicAPITool struct {
	*BaseTool
	src *ToolSource
}

func NewDynamicAPITool(src *ToolSource) *DynamicAPITool {
	t := &DynamicAPITool{src: src}
	t.BaseTool = &BaseTool{
		Name:        src.Name,
		Description: src.Description,
		Parameters:  paramsFromConfig(src.Parameters),
	}
	return t
}

// RequiredParams 取配置声明的必填参数（见 requiredFromConfig）
func (t *DynamicAPITool) RequiredParams() []string {
	if t.src == nil {
		return nil
	}
	return requiredFromConfig(t.src.Parameters)
}

func (t *DynamicAPITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if t.src == nil || t.src.HTTP == nil {
		return "", fmt.Errorf("HTTP 配置缺失")
	}
	apiCfg := t.src.HTTP
	if strings.TrimSpace(apiCfg.URL) == "" {
		return "", fmt.Errorf("请求 URL 为空")
	}
	method := strings.ToUpper(strings.TrimSpace(apiCfg.Method))
	if method == "" {
		method = http.MethodGet
	}
	url := renderTemplate(apiCfg.URL, args)

	var body io.Reader
	if apiCfg.Body != "" && method != http.MethodGet {
		body = bytes.NewBufferString(renderTemplate(apiCfg.Body, args))
	}

	timeout := apiCfg.Timeout
	if timeout <= 0 {
		timeout = 15
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, method, url, body)
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range apiCfg.Headers {
		req.Header.Set(k, renderTemplate(v, args))
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 响应上限 1MB
	result := string(data)
	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	return result, nil
}

// PermissionSubject 声明判定主体：HTTP API 工具的副作用不可知，主体取「方法 + 展开后的 URL」，
// 便于用户按目标地址授权。
func (t *DynamicAPITool) PermissionSubject(args map[string]interface{}) Subject {
	if t.src == nil || t.src.HTTP == nil {
		s := newRawSubject(t.GetName(), "HTTP 工具配置缺失")
		s.Trusted = false
		return s
	}
	apiCfg := t.src.HTTP
	method := strings.ToUpper(strings.TrimSpace(apiCfg.Method))
	if method == "" {
		method = http.MethodGet
	}
	return newRawSubject(t.GetName(), fmt.Sprintf("%s %s", method, renderTemplate(apiCfg.URL, args)))
}

// ===== MCP Server 连接池 =====

// mcpConnEntry 一个 MCP server 的连接条目
type mcpConnEntry struct {
	session *mcp.ClientSession
	tools   []*mcp.Tool
}

// MCPPool 管理所有 MCP server 连接（懒连接、按配置 ID 缓存）
type MCPPool struct {
	mu      sync.Mutex
	conns   map[string]*mcpConnEntry
	lastErr map[string]string // 最近一次连接错误（供界面展示）
}

func NewMCPPool() *MCPPool {
	return &MCPPool{conns: make(map[string]*mcpConnEntry), lastErr: make(map[string]string)}
}

// Status 返回某 server 的连接状态：已连接/错误信息
func (p *MCPPool) Status(id string) (connected bool, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.conns[id]; ok {
		return true, ""
	}
	return false, p.lastErr[id]
}

// buildTransport 根据 MCP 配置构造传输层。
// 配置用的是官方 type 取值（streamable-http / sse / stdio），不再有内部 transport 字段。
func buildTransport(cfg *MCPConfig) (mcp.Transport, error) {
	if cfg == nil {
		return nil, fmt.Errorf("缺少 MCP 配置")
	}
	if cfg.IsStdio() {
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, fmt.Errorf("stdio 接入缺少 command")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		hideConsoleWindow(cmd) // MCP 多为 node/python 进程，同样会闪控制台窗口
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
		return &mcp.CommandTransport{Command: cmd}, nil
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("%s 接入缺少 url", cfg.Type)
	}
	httpClient := &http.Client{
		Transport: &headerTransport{base: http.DefaultTransport, headers: cfg.Headers},
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Type), "sse") {
		return &mcp.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient}, nil
	}
	return &mcp.StreamableClientTransport{
		Endpoint:             cfg.URL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true, // 工具调用场景无需服务端推送，减少常驻连接
	}, nil
}

// Connect 连接（或复用缓存的）MCP server 并返回连接条目
func (p *MCPPool) Connect(parent context.Context, src *ToolSource) (*mcpConnEntry, error) {
	if src == nil {
		return nil, fmt.Errorf("来源为空")
	}
	p.mu.Lock()
	if entry, ok := p.conns[src.ID]; ok {
		p.mu.Unlock()
		return entry, nil
	}
	p.mu.Unlock()

	// 配置已是类型化字段：校验后直接建传输层，不再有中间解析
	if reason := src.MCP.Validate(); reason != "" {
		return nil, fmt.Errorf("MCP 配置无效: %s", reason)
	}
	transport, err := buildTransport(src.MCP)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "local-agent", Version: "v1.0.0"}, nil)

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		p.recordErr(src.ID, fmt.Sprintf("MCP 连接失败: %v", err))
		return nil, fmt.Errorf("MCP 连接失败: %w", err)
	}

	listCtx, listCancel := context.WithTimeout(parent, 10*time.Second)
	defer listCancel()
	listResult, err := session.ListTools(listCtx, nil)
	if err != nil {
		_ = session.Close()
		p.recordErr(src.ID, fmt.Sprintf("列出 MCP 工具失败: %v", err))
		return nil, fmt.Errorf("列出 MCP 工具失败: %w", err)
	}

	entry := &mcpConnEntry{session: session, tools: listResult.Tools}
	p.mu.Lock()
	// 并发场景下若已有连接，关闭后到的
	if old, ok := p.conns[src.ID]; ok {
		p.mu.Unlock()
		_ = session.Close()
		return old, nil
	}
	p.conns[src.ID] = entry
	delete(p.lastErr, src.ID)
	p.mu.Unlock()
	return entry, nil
}

// recordErr 记录连接错误
func (p *MCPPool) recordErr(id, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr[id] = msg
}

// Close 关闭并移除指定 server 的连接（配置变更/删除时调用）
func (p *MCPPool) Close(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.conns[id]; ok {
		_ = entry.session.Close()
		delete(p.conns, id)
	}
}

// CloseAll 关闭全部连接
func (p *MCPPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, entry := range p.conns {
		_ = entry.session.Close()
		delete(p.conns, id)
	}
}

// ===== MCP 子工具（包装为统一 ToolInterface）=====

// MCPTool 一个 MCP server 暴露的单个工具，注册名格式 mcp__<server>__<tool>
type MCPTool struct {
	*BaseTool
	pool      *MCPPool
	serverCfg *ToolSource
	toolName  string // server 上的原始工具名
	// schema 服务器给的原始 inputSchema（规范化后）。直通给模型，
	// 保留 required / enum / items / 嵌套结构——压平会让模型只能猜参数形状。
	schema json.RawMessage
}

func NewMCPTool(pool *MCPPool, serverCfg *ToolSource, tool *mcp.Tool) *MCPTool {
	fullName := mcpToolName(serverCfg.Name, tool.Name)
	t := &MCPTool{
		pool:      pool,
		serverCfg: serverCfg,
		toolName:  tool.Name,
	}
	t.BaseTool = &BaseTool{
		Name:        fullName,
		Description: tool.Description,
		Parameters:  paramsFromJSONSchema(tool.InputSchema), // 简化表：作为直通失败时的兜底
	}
	t.schema = normalizeInputSchema(fullName, tool.InputSchema)
	return t
}

// JSONSchema 实现 SchemaProvider：把服务器给的 inputSchema 原样交给模型
func (t *MCPTool) JSONSchema() json.RawMessage { return t.schema }

// PermissionSubject 实现 SubjectProvider：MCP 工具用结构化的参数对做判定主体，
// 使规则可以精确表达「只允许带 appid=100049128 的调用」。
func (t *MCPTool) PermissionSubject(args map[string]interface{}) Subject {
	return newMCPSubject(t.GetName(), args)
}

// normalizeInputSchema 把 MCP 的 inputSchema 规整成可直接传给模型的 JSON Schema：
// 必须是 JSON 对象、必须带 type（部分 server 省略），其余**原样保留**。
// 返回 nil 表示无法直通（非对象、或体积超限），调用方回退到简化参数表。
func normalizeInputSchema(toolName string, raw any) json.RawMessage {
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil || len(b) == 0 {
		return nil
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(b, &schema); err != nil || len(schema) == 0 {
		return nil
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	out, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	if len(out) > maxToolSchemaBytes {
		fmt.Printf("[ToolManager] MCP 工具 %s 的 inputSchema 过大（%d 字节，上限 %d），降级为简化参数表\n",
			toolName, len(out), maxToolSchemaBytes)
		return nil
	}
	return out
}

// mcpToolName MCP 子工具的注册名（Cline 风格前缀，避免重名）
func mcpToolName(server, tool string) string {
	return fmt.Sprintf("mcp__%s__%s", server, tool)
}

func (t *MCPTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	entry, err := t.pool.Connect(ctx, t.serverCfg)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := entry.session.CallTool(cctx, &mcp.CallToolParams{
		Name:      t.toolName,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("MCP 工具调用失败: %w", err)
	}
	return mcpContentToString(result.Content), nil
}

// mcpContentToString 将 MCP 返回的 Content 数组拍平为文本
func mcpContentToString(contents []mcp.Content) string {
	var sb strings.Builder
	for _, c := range contents {
		switch v := c.(type) {
		case *mcp.TextContent:
			sb.WriteString(v.Text)
		default:
			// 其他类型（图片/资源等）序列化为 JSON 占位
			if data, err := json.Marshal(v); err == nil {
				sb.Write(data)
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ===== 参数定义转换 =====

func paramsFromConfig(defs []ToolParamConfig) map[string]*ToolArgDef {
	if len(defs) == 0 {
		return map[string]*ToolArgDef{}
	}
	m := make(map[string]*ToolArgDef, len(defs))
	for _, d := range defs {
		m[d.Name] = &ToolArgDef{Type: "string", Description: d.Description}
	}
	return m
}

// requiredFromConfig 取来源配置里标为必填的参数名（保持配置中的顺序）。
//
// 必须与 paramsFromConfig 成对使用：参数定义那个 map 是无序的、且丢掉了 Required 标记，
// 必填信息只能走 RequiredParams() 这条独立通道才能到 buildJSONSchema。
// 历史缺陷：只有前者没有后者，于是 exec_shell 的 cmd 必填仅存在于 Execute 的错误
// 信息里（"cmd 参数是必需的"），模型完全看不到，只能靠猜——猜错就白烧一轮。
func requiredFromConfig(defs []ToolParamConfig) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.Required {
			out = append(out, d.Name)
		}
	}
	return out
}

// paramsFromJSONSchema 从 MCP 工具的 JSON Schema 提取第一层参数定义
func paramsFromJSONSchema(schema any) map[string]*ToolArgDef {
	m := map[string]*ToolArgDef{}
	root, ok := schema.(map[string]any)
	if !ok {
		return m
	}
	props, ok := root["properties"].(map[string]any)
	if !ok {
		return m
	}
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		argType := "string"
		if t, ok := prop["type"].(string); ok {
			argType = t
		}
		desc := ""
		if d, ok := prop["description"].(string); ok {
			desc = d
		}
		m[name] = &ToolArgDef{Type: argType, Description: desc}
	}
	return m
}
