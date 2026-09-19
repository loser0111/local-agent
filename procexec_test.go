package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// ===== 子进程的控制台窗口（Windows）=====
//
// 这类缺陷只在 Windows 上出现，而开发/自检常在 macOS 或 Linux 上做——所以这里不靠
// 平台判断，而是**扫全包源码**把"每个 spawn 点都必须隐藏控制台窗口"这条约束固定下来。

// spawnBody 一个函数的名字与函数体
type spawnBody struct {
	name string
	body string
}

// spawnScanBodies 把源码切成「函数名 + 函数体」。用花括号配对取体，不能用正则的
// 非贪婪匹配（函数体里嵌套着 if/for/闭包的花括号，正则会截断）。
//
// ⚠️ 返回**切片而不是 map[名字]函数体**：同名方法（CLITool.Execute / MetaTool.Execute /
// DynamicCLITool.Execute…）在 map 里会互相覆盖，结果是**含 spawn 的那个被静默丢掉、
// 整个函数根本没被检查**（我第一版就是这样：6 处 spawn 只扫出 4 处才发现）。
// 这类"静默漏检"比报错难查得多——检查工具本身必须宁可多报也不能少看。
//
// ⚠️ 找函数体起始的 `{` 时必须先跳过参数/返回值里的花括号：`args map[string]interface{}`
// 里就有一个，直接找"第一个 {"会把函数体截断在那个位置，同样是静默漏检。
func spawnScanBodies(src string) []spawnBody {
	out := []spawnBody{}
	re := regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?(\w+)`)
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[2]:m[3]]
		// 从函数名往后扫，跳过括号/方括号内的花括号，第一个处于"零深度"的 { 才是函数体
		paren, bracket := 0, 0
		i := -1
		for k := m[3]; k < len(src); k++ {
			// 还没找到函数体就撞上下一个函数声明（接口方法、函数类型声明那种没有体的形态）
			if strings.HasPrefix(src[k:], "\nfunc ") {
				break
			}
			switch src[k] {
			case '(':
				paren++
			case ')':
				paren--
			case '[':
				bracket++
			case ']':
				bracket--
			case '{':
				if paren == 0 && bracket == 0 {
					i = k
				}
			}
			if i >= 0 {
				break
			}
		}
		if i < 0 {
			continue
		}
		depth, j := 0, i
		for j < len(src) {
			if src[j] == '{' {
				depth++
			} else if src[j] == '}' {
				depth--
				if depth == 0 {
					break
				}
			}
			j++
		}
		if j >= len(src) {
			continue
		}
		out = append(out, spawnBody{name: name, body: src[i : j+1]})
	}
	return out
}

// 每个拉起子进程的函数都必须调用 hideConsoleWindow。
//
// 守的是这个缺陷：Wails 在 Windows 上按 GUI 子系统构建，程序自身没有控制台，于是它启动的
// 任何控制台程序（powershell / git / node / python）都会被 Windows **新建一个控制台窗口**，
// 表现为每执行一次工具闪一个黑框。漏一处就要用户来报一次。
//
// 判定粒度是**函数体**而不是文件：文件级判断会让"一个函数写了、另一个漏了"蒙混过关。
func TestEverySpawnHidesConsoleWindow(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	spawnCall := regexp.MustCompile(`exec\.Command(Context)?\(`)
	hides := regexp.MustCompile(`hideConsoleWindow\(`)

	// 目前已知的 spawn 点数量。新增调用点时把它改大（顺便会看到这段注释），
	// 变小时说明有调用点被删掉了——两种情况都值得停下来确认。
	const knownSpawnSites = 6

	sites, missing := 0, []string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "procexec_windows.go" {
			continue // 助手自身的实现（非 Windows 还有 procexec_other.go），不含 spawn 调用
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		for _, fn := range spawnScanBodies(string(src)) {
			if !spawnCall.MatchString(fn.body) {
				continue
			}
			sites++
			if !hides.MatchString(fn.body) {
				missing = append(missing, name+": "+fn.name)
			}
		}
	}

	if len(missing) > 0 {
		t.Fatalf("这些函数拉起了子进程却没有隐藏控制台窗口，Windows 上会弹黑框（见 procexec_windows.go）:\n  %s",
			strings.Join(missing, "\n  "))
	}
	if sites < knownSpawnSites {
		t.Fatalf("只扫到 %d 个 spawn 点，少于已知的 %d 个——判定规则或源码结构变了，先确认再改这个数字",
			sites, knownSpawnSites)
	}
}

// 非 Windows 上必须是空操作：不能顺手去动 SysProcAttr 的其他字段。
//
// 这条不是凑数：将来若有人想"统一在这里设置进程组/超时"之类的属性，改动会落到
// 每个平台，而这句话只是"隐藏控制台窗口"——让它保持单一职责，行为才可预期。
func TestHideConsoleWindowIsNoopOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上的行为由 procexec_windows.go 的实现与构建验证，这里只守非 Windows")
	}
	cmd := exec.Command("echo", "hi")
	hideConsoleWindow(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("非 Windows 上不该修改 SysProcAttr，实际 %+v", cmd.SysProcAttr)
	}
	// nil 也要安全
	hideConsoleWindow(nil)
}
