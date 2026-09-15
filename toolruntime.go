package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	cfg *ToolConfig
}

func NewDynamicCLITool(cfg *ToolConfig) *DynamicCLITool {
	var cliCfg CLIToolConfig
	_ = json.Unmarshal(cfg.Config, &cliCfg)
	t := &DynamicCLITool{cfg: cfg}
	t.BaseTool = &BaseTool{
		Name:        cfg.Name,
		Description: cfg.Description,
		Parameters:  paramsFromConfig(cfg.Parameters),
	}
	return t
}

func (t *DynamicCLITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	var cliCfg CLIToolConfig
	if err := json.Unmarshal(t.cfg.Config, &cliCfg); err != nil {
		return "", fmt.Errorf("CLI 配置解析失败: %w", err)
	}
	if strings.TrimSpace(cliCfg.Command) == "" {
		return "", fmt.Errorf("命令模板为空")
	}
	cmdLine := renderTemplate(cliCfg.Command, args)

	timeout := cliCfg.Timeout
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
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("命令执行超时（%ds）", timeout)
	}
	if cctx.Err() == context.Canceled {
		return string(out), fmt.Errorf("命令已取消")
	}
	return string(out), err
}

// DescribeOperation 用与 Execute **完全相同**的渲染路径产出命令行。
//
// 同源是硬要求：若这里渲染出的字符串与 Execute 实际执行的字符串不一致，
// 就会出现「规则放行了 A、实际执行的是 B」的绕过。两处都调用
// renderTemplate(cliCfg.Command, args)，保证匹配串 == 执行串。
func (t *DynamicCLITool) DescribeOperation(args map[string]interface{}) PermissionSubject {
	var cliCfg CLIToolConfig
	_ = json.Unmarshal(t.cfg.Config, &cliCfg)
	return PermissionSubject{
		ToolName: t.GetName(),
		Command:  renderTemplate(cliCfg.Command, args),
		Raw:      args,
	}
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
	cfg *ToolConfig
}

func NewDynamicAPITool(cfg *ToolConfig) *DynamicAPITool {
	t := &DynamicAPITool{cfg: cfg}
	t.BaseTool = &BaseTool{
		Name:        cfg.Name,
		Description: cfg.Description,
		Parameters:  paramsFromConfig(cfg.Parameters),
	}
	return t
}

func (t *DynamicAPITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	var apiCfg APIToolConfig
	if err := json.Unmarshal(t.cfg.Config, &apiCfg); err != nil {
		return "", fmt.Errorf("API 配置解析失败: %w", err)
	}
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

// DescribeOperation API 工具按「目标域名」匹配规则（对齐 Claude Code 的
// WebFetch(domain:...) 语义）。域名同样取自与 Execute 相同的渲染路径。
func (t *DynamicAPITool) DescribeOperation(args map[string]interface{}) PermissionSubject {
	var apiCfg APIToolConfig
	_ = json.Unmarshal(t.cfg.Config, &apiCfg)
	return PermissionSubject{
		ToolName: t.GetName(),
		Domain:   hostOfURL(renderTemplate(apiCfg.URL, args)),
		Raw:      args,
	}
}

// hostOfURL 取 URL 的主机名；解析失败返回空串（空串不会命中任何域名规则，
// 于是落到兜底询问 —— fail closed 方向）
func hostOfURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
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

// buildTransport 根据配置构造 MCP 传输层
func buildTransport(cfg *MCPToolConfig) (mcp.Transport, error) {
	switch cfg.Transport {
	case "http":
		return &mcp.StreamableClientTransport{
			Endpoint: cfg.URL,
			HTTPClient: &http.Client{
				Transport: &headerTransport{base: http.DefaultTransport, headers: cfg.Headers},
			},
			DisableStandaloneSSE: true, // 工具调用场景无需服务端推送，减少常驻连接
		}, nil
	case "sse":
		return &mcp.SSEClientTransport{
			Endpoint: cfg.URL,
			HTTPClient: &http.Client{
				Transport: &headerTransport{base: http.DefaultTransport, headers: cfg.Headers},
			},
		}, nil
	default: // stdio
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, fmt.Errorf("stdio 传输缺少 command")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
		return &mcp.CommandTransport{Command: cmd}, nil
	}
}

// Connect 连接（或复用缓存的）MCP server 并返回连接条目
func (p *MCPPool) Connect(parent context.Context, cfg *ToolConfig) (*mcpConnEntry, error) {
	p.mu.Lock()
	if entry, ok := p.conns[cfg.ID]; ok {
		p.mu.Unlock()
		return entry, nil
	}
	p.mu.Unlock()

	var mcpCfg MCPToolConfig
	if err := json.Unmarshal(cfg.Config, &mcpCfg); err != nil {
		return nil, fmt.Errorf("MCP 配置解析失败: %w", err)
	}
	if mcpCfg.Transport != "stdio" && strings.TrimSpace(mcpCfg.URL) == "" {
		return nil, fmt.Errorf("%s 传输缺少 url", mcpCfg.Transport)
	}

	transport, err := buildTransport(&mcpCfg)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "local-agent", Version: "v1.0.0"}, nil)

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		p.recordErr(cfg.ID, fmt.Sprintf("MCP 连接失败: %v", err))
		return nil, fmt.Errorf("MCP 连接失败: %w", err)
	}

	listCtx, listCancel := context.WithTimeout(parent, 10*time.Second)
	defer listCancel()
	listResult, err := session.ListTools(listCtx, nil)
	if err != nil {
		_ = session.Close()
		p.recordErr(cfg.ID, fmt.Sprintf("列出 MCP 工具失败: %v", err))
		return nil, fmt.Errorf("列出 MCP 工具失败: %w", err)
	}

	entry := &mcpConnEntry{session: session, tools: listResult.Tools}
	p.mu.Lock()
	// 并发场景下若已有连接，关闭后到的
	if old, ok := p.conns[cfg.ID]; ok {
		p.mu.Unlock()
		_ = session.Close()
		return old, nil
	}
	p.conns[cfg.ID] = entry
	delete(p.lastErr, cfg.ID)
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
	serverCfg *ToolConfig
	toolName  string // server 上的原始工具名
}

func NewMCPTool(pool *MCPPool, serverCfg *ToolConfig, tool *mcp.Tool) *MCPTool {
	fullName := mcpToolName(serverCfg.Name, tool.Name)
	t := &MCPTool{
		pool:      pool,
		serverCfg: serverCfg,
		toolName:  tool.Name,
	}
	t.BaseTool = &BaseTool{
		Name:        fullName,
		Description: tool.Description,
		Parameters:  paramsFromJSONSchema(tool.InputSchema),
	}
	return t
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

// DescribeOperation MCP 子工具按 server 上的原始工具名匹配规则
// （注册名已含 mcp__<server>__ 前缀，工具名本身即规则中的标识）
func (t *MCPTool) DescribeOperation(args map[string]interface{}) PermissionSubject {
	return PermissionSubject{
		ToolName:  t.GetName(),
		SpecValue: t.toolName,
		Raw:       args,
	}
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
