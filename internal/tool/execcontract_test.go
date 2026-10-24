package tool

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// ===== exec_shell 参数一致性回归测试 =====
//
// 历史 bug：给模型的 schema 声明参数名 cmd（toolstore.DefaultSources），
// 而 CLITool.Execute 读 args["command"]。结果模型完全按 schema 正确传
// {"cmd": "..."}，执行时却取不到值，直接报「command 参数是必需的」——
// 表现为「实际参数列表与给大模型的不一致」。
//
// 这组测试锁住三件事，缺一不可：
//  1. schema 里声明的参数名 == Execute 实际读取的键；
//  2. PermissionSubject 与 Execute 读同一个键（否则权限判定会看着空命令放行）；
//  3. 模型按 schema 传参时，工具真的能跑起来。

// execShellSource 取出内置 exec_shell 的来源定义。
func execShellSource(t *testing.T) *ToolSource {
	t.Helper()
	for _, s := range DefaultSources() {
		if s.Name == ToolExecShell {
			return s
		}
	}
	t.Fatalf("内置来源里找不到 %s", ToolExecShell)
	return nil
}

// schema 声明的参数名必须与 Execute 读取的键一致。
// 这是本 bug 的根因所在：两边用不同字面量必然漂移，故统一到 ShellArgCommand。
func TestExecShell_SchemaNameMatchesExecuteKey(t *testing.T) {
	src := execShellSource(t)

	if len(src.Parameters) == 0 {
		t.Fatal("exec_shell 应至少声明一个参数")
	}
	var declared string
	for _, p := range src.Parameters {
		if p.Required {
			declared = p.Name
		}
	}
	if declared == "" {
		t.Fatal("exec_shell 应有必填参数")
	}
	if declared != ShellArgCommand {
		t.Fatalf("schema 声明的必填参数是 %q，但 Execute 读取的是 %q", declared, ShellArgCommand)
	}

	// 反向确认：构造出来的工具，其参数表里确实有这个键（模型看到的就是它）
	tool := NewCLITool(src.Name, src.Description,
		ParamsFromConfig(src.Parameters), RequiredFromConfig(src.Parameters), t.TempDir())
	if _, ok := tool.Parameters[ShellArgCommand]; !ok {
		t.Fatalf("工具参数表里没有 %q，模型将无法正确传参", ShellArgCommand)
	}
}

// 权限判定与执行必须读同一个键。
// 若 PermissionSubject 读的键与 Execute 不同，会出现「判定时命令为空 → 放行，
// 实际执行另一条命令」的绕过风险。
func TestExecShell_PermissionSubjectUsesSameKey(t *testing.T) {
	tl := NewCLITool("exec_shell", "d",
		map[string]*ToolArgDef{ShellArgCommand: {Type: "string"}},
		[]string{ShellArgCommand}, t.TempDir())

	subject := tl.PermissionSubject(map[string]interface{}{ShellArgCommand: "rm -rf /tmp/x"})
	if subject.Tool != "exec_shell" {
		t.Fatalf("判定主体应记录工具名，实际: %q", subject.Tool)
	}
	// Raw 保存原始命令文本：它为空就说明 PermissionSubject 没读到正确的键，
	// 权限判定会对着空命令放行（fail-open），而 Execute 仍会执行真实命令。
	if strings.TrimSpace(subject.Raw) == "" {
		t.Fatalf("判定主体的命令为空，说明 PermissionSubject 没读到 %q 键", ShellArgCommand)
	}
	if !strings.Contains(subject.Raw, "rm -rf") {
		t.Fatalf("判定主体应包含真实命令，实际: %q", subject.Raw)
	}
}

// 端到端：按 schema 的方式传参，工具必须真的执行成功。
// 用 runtime.GOOS 选命令，保证跨平台可跑。
func TestExecShell_ExecutesWithSchemaArgName(t *testing.T) {
	tl := NewCLITool("exec_shell", "d",
		map[string]*ToolArgDef{ShellArgCommand: {Type: "string"}},
		[]string{ShellArgCommand}, t.TempDir())

	cmd := "echo hello"
	if runtime.GOOS == "windows" {
		cmd = "Write-Output hello"
	}

	out, err := tl.Execute(context.Background(), map[string]interface{}{ShellArgCommand: cmd})
	if err != nil {
		t.Fatalf("按 schema 传 %q 执行失败: %v（out=%q）", ShellArgCommand, err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("输出应含 hello，实际: %q", out)
	}
}

// 旧的错误键名不应再被接受——否则说明又出现了两套参数名。
func TestExecShell_RejectsLegacyCommandKey(t *testing.T) {
	tl := NewCLITool("exec_shell", "d",
		map[string]*ToolArgDef{ShellArgCommand: {Type: "string"}},
		[]string{ShellArgCommand}, t.TempDir())

	_, err := tl.Execute(context.Background(), map[string]interface{}{"command": "echo hi"})
	if err == nil {
		t.Fatal("传入旧键 command 应当报错，否则说明两套参数名并存")
	}
	if !strings.Contains(err.Error(), ShellArgCommand) {
		t.Fatalf("错误信息应指出正确键名 %q，实际: %v", ShellArgCommand, err)
	}
}

// 缺参时错误信息要直接给出正确键名，模型据此才能自我纠正。
func TestExecShell_MissingArgErrorNamesCorrectKey(t *testing.T) {
	tl := NewCLITool("exec_shell", "d",
		map[string]*ToolArgDef{ShellArgCommand: {Type: "string"}},
		[]string{ShellArgCommand}, t.TempDir())

	_, err := tl.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("缺少必填参数应报错")
	}
	if !strings.Contains(err.Error(), ShellArgCommand) {
		t.Fatalf("错误信息应含 %q，实际: %v", ShellArgCommand, err)
	}
}
