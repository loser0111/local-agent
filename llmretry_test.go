package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 重试层单测 =====
//
// 这一层的 bug 有个共同特点：**不会报错**。判错了只是多花钱、多等几秒，
// 或者把一次限流静默地当成「空回复」。所以下面的断言都盯在
// 「到底尝试了几次」和「等的是不是那个量级」上，而不是盯返回值的形状。

// fastPolicy 把退避压到毫秒级。
//
// 这是把策略做成参数而不是读包级常量的理由：否则每验一次「重试了两次」
// 都要真等一秒多的退避，那样的测试没人愿意跑，最后就没人跑了。
func fastPolicy(max int) retryPolicy {
	return retryPolicy{
		MaxAttempts: max,
		BaseDelay:   time.Millisecond,
		MaxDelay:    2 * time.Millisecond,
		MaxWait:     time.Second,
	}
}

// openAIOK 一份最小的、能被 LLMResp 解析的成功响应。
const openAIOK = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],` +
	`"usage":{"prompt_tokens":10,"completion_tokens":2}}`

func testModel(url string) *Model {
	return &Model{Name: "t", ModelID: "t", Protocol: "openai", URL: url, APIKey: "k"}
}

func testReq() *LLMReq {
	return &LLMReq{Model: "t", Messages: []LLMMessage{{Role: RoleUser, Content: "hi"}}}
}

// sse 拼一个 SSE 帧。
func sse(payload string) string { return "data: " + payload + "\n\n" }

// ===== HTTP 状态码路径 =====

func TestRetryOn429ThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		_, _ = w.Write([]byte(openAIOK))
	}))
	defer srv.Close()

	resp, err := callLLMForModelWith(context.Background(), testModel(srv.URL), testReq(), fastPolicy(4), nil)
	if err != nil {
		t.Fatalf("429 之后应当重试并成功，却失败了: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("重试成功后应当拿到正常响应")
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("期望恰好请求 2 次（1 次失败 + 1 次成功），实际 %d 次", n)
	}
}

func TestRetryOn500ThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`upstream exploded`))
			return
		}
		_, _ = w.Write([]byte(openAIOK))
	}))
	defer srv.Close()

	if _, err := callLLMForModelWith(context.Background(), testModel(srv.URL), testReq(), fastPolicy(4), nil); err != nil {
		t.Fatalf("5xx 应当重试并最终成功: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("期望请求 3 次，实际 %d 次", n)
	}
}

func TestNoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad model name"}}`))
	}))
	defer srv.Close()

	if _, err := callLLMForModelWith(context.Background(), testModel(srv.URL), testReq(), fastPolicy(4), nil); err == nil {
		t.Fatal("400 应当失败")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("400 是请求本身有问题，不该重试；实际请求了 %d 次", n)
	}
}

func TestNoRetryOn401(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`invalid api key`))
	}))
	defer srv.Close()

	if _, err := callLLMForModelWith(context.Background(), testModel(srv.URL), testReq(), fastPolicy(4), nil); err == nil {
		t.Fatal("401 应当失败")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("密钥不对时重试是纯浪费；实际请求了 %d 次", n)
	}
}

// TestNoRetryOnContextOverflow 是本文件里最重要的一条。
//
// 上下文超限通常以 400 出现，如果在这里被当成"可重试"，重试额度会被烧光，
// 而 runToolLoop 里那套「压缩上下文再试一次」的兜底就再也没机会执行了。
// 症状会是：长会话撞超限 → 重试三次同样的请求 → 全失败 → 用户看到报错，
// 而本来自动压缩一次就能救回来。
func TestNoRetryOnContextOverflow(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"prompt is too long: 250000 tokens > 200000 maximum"}}`,
		`{"error":{"message":"This model's maximum context length is 128000 tokens"}}`,
		`{"error":{"message":"请求过长，请缩短输入"}}`,
	} {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		}))

		_, err := callLLMForModelWith(context.Background(), testModel(srv.URL), testReq(), fastPolicy(4), nil)
		srv.Close()
		if err == nil {
			t.Fatalf("上下文超限应当报错（交给上层压缩兜底），body=%s", body)
		}
		if n := atomic.LoadInt32(&calls); n != 1 {
			t.Fatalf("上下文超限不该在这里重试（会烧掉压缩兜底的机会）；body=%s 实际请求 %d 次", body, n)
		}
		// 错误消息里必须仍然带着服务端原文，否则 isContextOverflowError 在上层就匹配不到了。
		if !isContextOverflowError(err) {
			t.Fatalf("错误消息丢失了服务端原文，上层的关键字匹配会失效: %v", err)
		}
	}
}

// TestRetryStopsOnContextCancel 验证退避期间能被取消。
//
// 用 time.Sleep 实现退避的话，用户点了停止要等退避走完才生效，
// 表现为几秒钟的「点了没反应」。
func TestRetryStopsOnContextCancel(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	slow := retryPolicy{MaxAttempts: 4, BaseDelay: 5 * time.Second, MaxDelay: 5 * time.Second, MaxWait: time.Second}
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := callLLMForModelWith(ctx, testModel(srv.URL), testReq(), slow, nil); err == nil {
		t.Fatal("取消后应当返回错误")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消没有中断退避：等了 %v（退避基数是 5s）", elapsed)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("取消后不该再发起请求；实际 %d 次", n)
	}
}

// ===== 流式路径 =====

func TestStreamRetriesBeforeFirstChunk(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(`{"choices":[{"delta":{"content":"hello"}}]}`)))
		_, _ = w.Write([]byte(sse("[DONE]")))
	}))
	defer srv.Close()

	var got strings.Builder
	req := testReq()
	req.Stream = true
	if _, err := callLLMStreamForModelWith(context.Background(), testModel(srv.URL), req,
		func(s string) { got.WriteString(s) }, fastPolicy(3)); err != nil {
		t.Fatalf("流在吐内容之前失败，应当重试: %v", err)
	}
	if got.String() != "hello" {
		t.Fatalf("重试后的内容不对: %q", got.String())
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("期望请求 2 次，实际 %d 次", n)
	}
}

// TestStreamNoRetryAfterChunkEmitted 是流式路径的安全阀。
//
// 前端的气泡是**追加**渲染的。已经吐过内容再重试，用户会看到同一段话出现两遍。
// 那比直接报错更糟——报错至少是诚实的。
func TestStreamNoRetryAfterChunkEmitted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(`{"choices":[{"delta":{"content":"partial"}}]}`)))
		// 吐完内容再报一个限流错（本可以被重试）
		_, _ = w.Write([]byte(sse(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`)))
	}))
	defer srv.Close()

	var got strings.Builder
	req := testReq()
	req.Stream = true
	_, err := callLLMStreamForModelWith(context.Background(), testModel(srv.URL), req,
		func(s string) { got.WriteString(s) }, fastPolicy(3))
	if err == nil {
		t.Fatal("流内错误帧应当让本次调用失败")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("已经吐出过内容就不该重试（会让前端重复渲染）；实际请求 %d 次", n)
	}
	if got.String() != "partial" {
		t.Fatalf("已吐出的内容不该被丢弃: %q", got.String())
	}
}

// TestStreamErrorFrameRetried 覆盖一个容易漏的路径：
// OpenAI 系把错误也放在 data 帧里发（HTTP 状态仍是 200）。
// 不识别这一帧的话，它会被当成"没有 choices 的正常帧"跳过，
// 最后表现为「LLM 返回空响应」——限流被误报成空回复，且永不重试。
func TestStreamErrorFrameRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			_, _ = w.Write([]byte(sse(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`)))
			return
		}
		_, _ = w.Write([]byte(sse(`{"choices":[{"delta":{"content":"recovered"}}]}`)))
		_, _ = w.Write([]byte(sse("[DONE]")))
	}))
	defer srv.Close()

	var got strings.Builder
	req := testReq()
	req.Stream = true
	if _, err := callLLMStreamForModelWith(context.Background(), testModel(srv.URL), req,
		func(s string) { got.WriteString(s) }, fastPolicy(3)); err != nil {
		t.Fatalf("流内限流应当被重试: %v", err)
	}
	if got.String() != "recovered" {
		t.Fatalf("重试后的内容不对: %q", got.String())
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("期望请求 2 次，实际 %d 次", n)
	}
}

// ===== 纯函数 =====

func TestBackoffBounds(t *testing.T) {
	p := retryPolicy{MaxAttempts: 8, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second, MaxWait: time.Minute}
	for attempt := 1; attempt <= 6; attempt++ {
		for i := 0; i < 40; i++ { // 抖动是随机的，多取几次看边界
			d, ok := backoffFor(p, attempt, 0)
			if !ok {
				t.Fatalf("没有 Retry-After 时不应拒绝退避")
			}
			expect := 100 * time.Millisecond << (attempt - 1)
			if expect > time.Second {
				expect = time.Second
			}
			// 等值抖动：落在 [d/2, d]
			if d < expect/2 || d > expect {
				t.Fatalf("第 %d 次退避 %v 落在 [%v, %v] 之外", attempt, d, expect/2, expect)
			}
		}
	}
}

func TestBackoffHonorsRetryAfter(t *testing.T) {
	p := retryPolicy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxWait: 30 * time.Second}

	d, ok := backoffFor(p, 1, 5*time.Second)
	if !ok || d != 5*time.Second {
		t.Fatalf("应当原样采用服务端给的 Retry-After，得到 %v / ok=%v", d, ok)
	}

	// 超过上限时应当拒绝等待：挂着不动比快速失败更糟——用户不知道该等多久。
	d, ok = backoffFor(p, 1, 10*time.Minute)
	if ok {
		t.Fatalf("Retry-After 超过 MaxWait 时不应继续等待，得到 %v", d)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{
		"":            0,
		"0":           0,
		"-3":          0,
		"7":           7 * time.Second,
		"  12  ":      12 * time.Second,
		"Wed, 21 Oct": 0, // HTTP-date 形式不支持，退回指数退避
		"abc":         0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("parseRetryAfter(%q) = %v，期望 %v", in, got, want)
		}
	}
}

func TestSSEStatusFor(t *testing.T) {
	cases := []struct {
		typ, msg string
		want     int
	}{
		{"overloaded_error", "", 529},
		{"rate_limit_error", "", http.StatusTooManyRequests},
		{"api_error", "", http.StatusInternalServerError},
		{"timeout_error", "", http.StatusRequestTimeout},
		{"invalid_request_error", "bad", http.StatusBadRequest},
		// 类型名缺失时靠正文兜底
		{"", "Rate limit exceeded, please try again", http.StatusTooManyRequests},
		{"", "something odd", http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := sseStatusFor(c.typ, c.msg); got != c.want {
			t.Errorf("sseStatusFor(%q,%q) = %d，期望 %d", c.typ, c.msg, got, c.want)
		}
	}
}

func TestShouldRetry(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"429", &llmHTTPError{Status: http.StatusTooManyRequests}, true},
		{"500", &llmHTTPError{Status: 500}, true},
		{"503", &llmHTTPError{Status: 503}, true},
		{"408", &llmHTTPError{Status: http.StatusRequestTimeout}, true},
		{"400", &llmHTTPError{Status: 400}, false},
		{"401", &llmHTTPError{Status: 401}, false},
		{"403", &llmHTTPError{Status: 403}, false},
		{"404", &llmHTTPError{Status: 404}, false},
		{"400+超限", &llmHTTPError{Status: 400, Body: "prompt is too long"}, false},
		{"流内限流", &llmHTTPError{Status: http.StatusTooManyRequests, Stream: true}, true},
		{"流内超限", &llmHTTPError{Status: 400, Body: "context length exceeded", Stream: true}, false},
		{"ctx 取消", context.Canceled, false},
		{"ctx 超时", context.DeadlineExceeded, false},
		{"证书错误", errors.New("x509: certificate signed by unknown authority"), false},
		{"协议错误", errors.New("unsupported protocol scheme \"ftp\""), false},
		{"连接被重置", errors.New("read tcp: connection reset by peer"), true},
		{"域名解析失败", errors.New("dial tcp: lookup api.example.com: no such host"), true},
	}
	for _, c := range cases {
		if got := shouldRetry(ctx, c.err); got != c.want {
			t.Errorf("%s: shouldRetry = %v，期望 %v", c.name, got, c.want)
		}
	}

	// ctx 已结束时，连可重试的错误也不该重试（用户点了停止）
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldRetry(dead, &llmHTTPError{Status: 500}) {
		t.Error("ctx 已取消时不应重试")
	}
}

func TestWithRetryGiveUp(t *testing.T) {
	var attempts int
	_, err := withRetry(context.Background(), fastPolicy(4), func() bool { return true },
		func() (int, error) { attempts++; return 0, &llmHTTPError{Status: 500} })
	if err == nil {
		t.Fatal("放弃时应当返回错误")
	}
	if attempts != 1 {
		t.Fatalf("giveUp 为真时应当只尝试一次，实际 %d 次", attempts)
	}
}

func TestWithRetryExhaustsAttempts(t *testing.T) {
	var attempts int
	_, err := withRetry(context.Background(), fastPolicy(3), nil,
		func() (int, error) { attempts++; return 0, &llmHTTPError{Status: 503} })
	if err == nil {
		t.Fatal("全部失败时应当返回错误")
	}
	if attempts != 3 {
		t.Fatalf("应当恰好尝试 MaxAttempts 次，实际 %d 次", attempts)
	}
}

// TestNewLLMHTTPErrorTruncatesBody 防止错误响应把日志与界面撑爆。
func TestNewLLMHTTPErrorTruncatesBody(t *testing.T) {
	huge := strings.Repeat("x", llmErrorBodyMax*3)
	err := newLLMHTTPError(500, []byte(huge), http.Header{})
	var he *llmHTTPError
	if !errors.As(err, &he) {
		t.Fatal("应当能 As 出 llmHTTPError")
	}
	if len([]rune(he.Body)) > llmErrorBodyMax+1 { // +1 给省略号
		t.Fatalf("响应体没有被截断: %d 字符", len([]rune(he.Body)))
	}
	if !strings.Contains(err.Error(), "status=500") {
		t.Fatalf("错误措辞变了，可能让上层的关键字匹配失效: %v", err)
	}
}
