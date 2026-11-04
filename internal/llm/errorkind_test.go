package llm

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// TestClassifyErrorKinds 各类失败落到预期的分类上。
func TestClassifyErrorKinds(t *testing.T) {
	overflow := NewLLMHTTPError(http.StatusBadRequest, []byte(`{"error":{"message":"This model's maximum context length is 8192 tokens"}}`), http.Header{})
	authErr := NewLLMHTTPError(http.StatusUnauthorized, []byte("bad key"), http.Header{})
	rateErr := NewLLMHTTPError(http.StatusTooManyRequests, []byte("slow down"), http.Header{})
	serverErr := NewLLMHTTPError(http.StatusBadGateway, []byte("upstream gone"), http.Header{})
	timeoutErr := NewLLMHTTPError(http.StatusRequestTimeout, []byte("too slow"), http.Header{})
	badReq := NewLLMHTTPError(http.StatusUnprocessableEntity, []byte("nope"), http.Header{})

	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"nil", nil, ErrorKindNone},
		{"上下文超限（400 但归 overflow）", overflow, ErrorKindContextOverflow},
		{"401", authErr, ErrorKindAuth},
		{"429", rateErr, ErrorKindRateLimit},
		{"502", serverErr, ErrorKindServer},
		{"408", timeoutErr, ErrorKindTimeout},
		{"422", badReq, ErrorKindBadRequest},
		{"ctx 取消", context.Canceled, ErrorKindAborted},
		{"ctx 超时", context.DeadlineExceeded, ErrorKindAborted},
		{"包装过的 ctx 取消", fmt.Errorf("请求失败: %w", context.Canceled), ErrorKindAborted},
		{"tls 证书", &url.Error{Op: "Post", URL: "https://x", Err: x509.UnknownAuthorityError{}}, ErrorKindConfig},
		{"scheme 非法", errors.New(`unsupported protocol scheme "foo"`), ErrorKindConfig},
		{"传输层超时", &timeoutNetError{}, ErrorKindTimeout},
		{"连接被拒（无从归类，按可重试）", errors.New("connection reset by peer"), ErrorKindNetwork},
	}

	for _, c := range cases {
		if got := ClassifyError(c.err); got != c.want {
			t.Errorf("%s：期望 %q，实际 %q", c.name, c.want, got)
		}
	}
}

// timeoutNetError 一个只实现 Timeout() 的假传输错误。
type timeoutNetError struct{}

func (e *timeoutNetError) Error() string   { return "i/o timeout" }
func (e *timeoutNetError) Timeout() bool   { return true }
func (e *timeoutNetError) Temporary() bool { return true }

// TestClassifyErrorStopsAtNetError 顺序守卫：ctx.DeadlineExceeded 也实现 net.Error 且
// Timeout() 为真，若先判 net.Error 就会把它误判成可重试的超时，
// 于是"用户已经放弃等待"的请求会在后台被再试一遍。
func TestClassifyErrorStopsAtNetError(t *testing.T) {
	var ne net.Error
	if !errors.As(context.DeadlineExceeded, &ne) || !ne.Timeout() {
		t.Fatal("前提不成立：context.DeadlineExceeded 应当是 Timeout() 为真的 net.Error")
	}
	if got := ClassifyError(context.DeadlineExceeded); got != ErrorKindAborted {
		t.Fatalf("ctx 超时必须优先判为 %q，实际 %q", ErrorKindAborted, got)
	}
	if ClassifyError(context.DeadlineExceeded).Retryable() {
		t.Fatal("ctx 超时不应被判为可重试")
	}
}

// TestRetryableAgreesWithShouldRetry 一致性守卫。
//
// Retryable 与 ShouldRetry 是同一批判定信号的两个出口：一个给"要不要再试"，
// 一个给"失败了算什么性质"。它们一旦分叉，就会出现"重试层已经放弃、计划层还在重试"
// （或反之）的自相矛盾，而且两边各自都自洽，极难发现。这里逐条对着断言。
func TestRetryableAgreesWithShouldRetry(t *testing.T) {
	errs := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"401", NewLLMHTTPError(http.StatusUnauthorized, []byte("bad key"), http.Header{})},
		{"403", NewLLMHTTPError(http.StatusForbidden, []byte("forbidden"), http.Header{})},
		{"400", NewLLMHTTPError(http.StatusBadRequest, []byte("bad"), http.Header{})},
		{"408", NewLLMHTTPError(http.StatusRequestTimeout, []byte("t"), http.Header{})},
		{"429", NewLLMHTTPError(http.StatusTooManyRequests, []byte("r"), http.Header{})},
		{"429 带 Retry-After", NewLLMHTTPError(http.StatusTooManyRequests, []byte("r"), http.Header{"Retry-After": []string{"3"}})},
		{"500", NewLLMHTTPError(http.StatusInternalServerError, []byte("s"), http.Header{})},
		{"529", NewLLMHTTPError(529, []byte("overloaded"), http.Header{})},
		{"上下文超限", NewLLMHTTPError(http.StatusBadRequest, []byte("maximum context length exceeded"), http.Header{})},
		{"流内限流", LLMStreamError("rate_limit_error", "too many requests")},
		{"流内无法识别", LLMStreamError("", "something odd")},
		{"ctx 取消", context.Canceled},
		{"ctx 超时", context.DeadlineExceeded},
		{"tls", errors.New("x509: certificate signed by unknown authority")},
		{"scheme", errors.New("unsupported protocol scheme \"foo\"")},
		{"形状", errors.New("invalid URL escape \"%zz\"")},
		{"传输层超时", &timeoutNetError{}},
		{"连接被拒", errors.New("connection reset by peer")},
		{"dns", errors.New("no such host")},
	}

	for _, c := range errs {
		want := ShouldRetry(context.Background(), c.err)
		got := ClassifyError(c.err).Retryable()
		if got != want {
			t.Errorf("%s：ShouldRetry=%v，但 ClassifyError=%q 的 Retryable()=%v（分类与重试判定已分叉）",
				c.name, want, ClassifyError(c.err), got)
		}
	}
}
