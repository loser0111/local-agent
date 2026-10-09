package permission

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ===== 询问回路 =====
//
// 这一组守的是"等待-唤醒"本身的边界：超时、跨会话取消、迟到答复。
// 它们都在超时/取消后回到**拒绝**，而不是放行——fail closed 在这里同样是硬要求。

func TestBrokerTimeout(t *testing.T) {
	b := NewBroker(30 * time.Millisecond)
	id, ch := b.Register("s1")
	start := time.Now()
	_, err := b.Wait(id, ch, context.Background())
	if err == nil {
		t.Fatal("无人应答时应返回错误（调用方据此按拒绝处理）")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应说明超时，实际 %v", err)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("不应提前返回")
	}
	// 超时后迟到的答复应被拒绝
	if err := b.Resolve(Answer{ID: id, Decision: "allow"}); err == nil {
		t.Fatal("超时后的迟到答复应返回错误")
	}
}

func TestBrokerCancelSession(t *testing.T) {
	b := NewBroker(time.Minute)
	id, ch := b.Register("s1")

	done := make(chan Answer, 1)
	go func() {
		ans, _ := b.Wait(id, ch, context.Background())
		done <- ans
	}()

	if n := b.CancelSession("s2"); n != 0 {
		t.Fatalf("不应取消其它会话的请求，实际取消 %d 个", n)
	}
	if n := b.CancelSession("s1"); n != 1 {
		t.Fatalf("应取消 1 个请求，实际 %d", n)
	}

	select {
	case ans := <-done:
		if AnswerAllows(ans) {
			t.Fatal("被取消的请求不应构成放行")
		}
	case <-time.After(time.Second):
		t.Fatal("取消后等待方应立即返回")
	}
}

func TestBrokerResolveUnknownID(t *testing.T) {
	b := NewBroker(time.Minute)
	if err := b.Resolve(Answer{ID: "nope", Decision: "allow"}); err == nil {
		t.Fatal("未知请求 ID 应返回错误")
	}
}

// A8：答复缺失/无法识别时按拒绝处理
func TestAnswerAllowsFailClosed(t *testing.T) {
	if AnswerAllows(Answer{Decision: ""}) {
		t.Fatal("空决策不应构成放行")
	}
	if AnswerAllows(Answer{Decision: "yolo"}) {
		t.Fatal("无法识别的决策不应构成放行")
	}
	if !AnswerAllows(Answer{Decision: "ALLOW"}) {
		t.Fatal("ALLOW 应构成放行")
	}
}
