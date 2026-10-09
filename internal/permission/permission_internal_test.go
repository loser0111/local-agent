package permission

import "testing"

// 这组用例只碰包内部的匹配求值逻辑（不进 App 装配层），所以随包搬走：
// 它们原先在根包的 permission_test.go 里——那时 permission 还在 package main，
// 直接调用 matchAnyUnit / grantsCoverAll / coversAllUnits / matchBuiltinDeny
// 这些未导出符号毫无障碍。拆包后根测试够不着它们，而把这些内部判定函数导出
// 只为测试用又会污染包 API，所以把「测内部」的用例也一并搬进来。

// ===== 求值方向性：收紧用「存在」，放宽用「全部」 =====

// A1：授权过 grep 之后，复合命令里的 rm 段必须仍然被拦住
func TestCoversAll_GrantDoesNotLeakToOtherSegments(t *testing.T) {
	subject := NewCommandSubject("exec_shell", "rm -rf /tmp/x && grep -n y file.go")

	// 收紧方向：存在一段命中 → 成立
	denyRules := []Rule{{Tool: "exec_shell", Spec: "rm -rf /tmp/x"}}
	if _, hit := matchAnyUnit(denyRules, subject); !hit {
		t.Fatal("收紧方向应命中 rm 段")
	}

	// 放宽方向：只有 grep 被覆盖 → 不成立
	grants := []Grant{{Tool: "exec_shell", Spec: "grep -n y file.go"}}
	if _, hit := grantsCoverAll(grants, subject); hit {
		t.Fatal("授权只覆盖了 grep 段，不应放行整条复合命令（历史绕过漏洞 A）")
	}

	// 两段都被覆盖才放行
	grants = append(grants, Grant{Tool: "exec_shell", Spec: "rm -rf /tmp/x"})
	if _, hit := grantsCoverAll(grants, subject); !hit {
		t.Fatal("两段都被授权覆盖时应放行")
	}
}

// A2：前缀规则只在逐段判定里生效，不能因为"整行以 git 开头"就放行后面的 rm
func TestPrefixRuleDoesNotMatchWholeLine(t *testing.T) {
	subject := NewCommandSubject("exec_shell", "git status && rm -rf /tmp/x")

	prefixAllow := []Rule{{Tool: "exec_shell", Spec: "git", IsPrefix: true}}
	if _, hit := coversAllUnits(prefixAllow, subject); hit {
		t.Fatal("前缀规则不得覆盖整条复合命令（历史绕过漏洞 B）")
	}

	// 逐段判定下，前缀规则可以覆盖 "git status" 这一段
	if !prefixAllow[0].matchesUnit("exec_shell", "git status") {
		t.Fatal("前缀规则应覆盖以 git 开头的那一段")
	}
}

// 内置名单不应误伤常见命令（避免"安全"变成"不可用"）
func TestBuiltinDenyDoesNotOverreach(t *testing.T) {
	cases := []string{
		"rm -rf /tmp/build", "rm -rf ./node_modules", "rm -rf build", "rm -rf dist",
		"rm -rf *.log", "rm -rf ./dist", "rm -rf /var/log/myapp", "rm -rf /usr/local/myapp",
		"rm -rf ../build", "rm -rf ./tmp/cache", "rm -f package-lock.json",
		"rm -rf node_modules && npm install",
		"echo shutdown", "git commit -m 'fix chmod 777 handling'",
		"grep -rn 'rm -rf /' docs/",
		"dd if=/dev/zero of=./test.img bs=1M count=10",
		"ls -la", "mkdir -p src/utils", "git status", "npm test",
	}
	for _, cmd := range cases {
		if pat, hit := matchBuiltinDeny(NewCommandSubject("exec_shell", cmd)); hit {
			t.Errorf("命令 %q 不应命中内置拒绝名单（命中 %s）", cmd, pat)
		}
	}
}
