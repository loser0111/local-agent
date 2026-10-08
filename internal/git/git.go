// Package git 封装本项目的 git 调用。
//
// 为什么独立成包：diff 计算、checkpoint 快照等多个域都要跑 git，而每处调用都必须
// 显式隐藏子进程的控制台窗口（Windows GUI 子系统下会闪黑框，见 procx 包说明）。
// 原先这份封装被拆在两个文件里（runGitEnv 在 checkpoint.go、runGit 与 gitTimeout
// 在 diff.go）——摘包时按域归位到这里，全项目只留这一份 git 调用封装。
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"wails-tmp/internal/procx"
)

// Timeout 单次 git 调用的超时上限。超时即失败，由调用方决定降级策略
// （diff 的选择是"返回空差异并降级"，见 internal/diff）。
const Timeout = 15 * time.Second

// Run 执行 git 命令并返回 stdout。dir 为空时交给 git 自己处理（当前目录）。
func Run(dir string, args ...string) (string, error) {
	return RunEnv(dir, nil, args...)
}

// RunEnv 与 Run 相同，但可附加环境变量（checkpoint 需要 GIT_INDEX_FILE，
// 否则索引会写到真实仓库上，污染用户的工作区）。
func RunEnv(dir string, extraEnv []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	full := append([]string{"-C", dir, "-c", "core.quotepath=false", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	procx.HideConsoleWindow(cmd) // 每轮对话都可能起 git，Windows 上会连着闪黑框
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s 失败: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
