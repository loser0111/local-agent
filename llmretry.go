package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ===== LLM 调用的重试与退避 =====
//
// 这一层存在的理由：在此之前，四条调用路径（OpenAI / Anthropic × 流式 / 非流式）
// 对失败的处理都只有一句 `fmt.Errorf("LLM 返回错误 (status=%d): %s", ...)`——
// 429、500、400 走的是同一段代码，没有状态码分支、没有退避、没有重连。
// 后果是一次限流就把整轮对话判死，而 429 在长会话里是常态而不是意外。
//
// **为什么放在「按模型分发」那一层**（callLLMForModel / callLLMStreamForModel），
// 而不是写进四个 HTTP 函数里：写进去就是四份会各自漂移的实现。本项目在权限网关与
// 工具曝光策略上已经各吃过一次「两处实现悄悄漂移」的亏，这里不再重复。
// 分发层的入参是一个 *LLMReq，每次尝试内部都会重新序列化，所以重放是安全的
// ——不会遇到「请求体已被消费」的问题。
//
// 这一层刻意**保持纯粹**：不发事件、不碰会话状态，只做「重试或放弃」的判断与等待。
// 想给用户一个「正在重试」的提示是合理的，但那需要一个带会话归属的通道（本项目的事件
// 必须以 sessionId 盖章，猜错归属比丢掉一条事件更糟），而这里的调用栈里没有会话信息。
// 所以现在只在控制台留痕；要做界面提示，应该在调用方那层加，而不是在这里塞个全局回调。

// retryPolicy 重试策略。
type retryPolicy struct {
	// MaxAttempts 总尝试次数（含首次）。取 4 是权衡：三次退避合计约 3.5s，
	// 足以跨过绝大多数瞬时抖动与一次 429，又不会让用户觉得界面卡死。
	MaxAttempts int
	// BaseDelay 首次退避基数，之后按 2 的幂增长。
	BaseDelay time.Duration
	// MaxDelay 单次退避上限。
	MaxDelay time.Duration
	// MaxWait 服务端 Retry-After 超出它就放弃重试。
	// 宁可快速失败并说清楚原因，也不要安静地挂在那里等一分钟。
	MaxWait time.Duration
}

var defaultRetryPolicy = retryPolicy{
	MaxAttempts: 4,
	BaseDelay:   500 * time.Millisecond,
	MaxDelay:    8 * time.Second,
	MaxWait:     60 * time.Second,
}

// llmErrorBodyMax 错误响应体在错误消息里保留的最大字符数。
// 有些网关会把一整坨 HTML 错误页塞回来，原样带进日志与界面会淹没真正有用的那行。
const llmErrorBodyMax = 2000

// llmHTTPError 一次 HTTP 层的失败。
//
// 单独开一个类型而不是继续用 fmt.Errorf：重试判定必须看**状态码**，
// 而从错误字符串里再把它抠出来是典型的易碎写法（改一次文案就悄悄失效）。
// 同时它保留响应体——因为「上下文超限」这类错误只能靠正文措辞识别，
// 而它恰恰是**绝对不能在这里重试**的一类（见 shouldRetry）。
type llmHTTPError struct {
	Status int
	Body   string
	// RetryAfter 服务端要求的最短等待；0 表示没给。
	RetryAfter time.Duration
	// Stream 为真表示这个错误来自**流式响应内部**的错误帧，而不是 HTTP 状态码。
	// 两种来源共用同一套重试判定，但报错措辞要区分开：
	// 一个 200 响应里报出"status=429"会让排障的人一头雾水。
	Stream bool
}

// Error 的措辞与改造前保持一致（非流式仍是 "LLM 返回错误 (status=%d): %s"）。
// 这不是洁癖：runToolLoop 的上下文超限兜底靠 isContextOverflowError 做关键字匹配，
// 界面与日志里也一直按这个形状找问题。换措辞等于让那些判断静默失效。
func (e *llmHTTPError) Error() string {
	if e.Stream {
		return fmt.Sprintf("LLM 返回错误 (流式响应内错误): %s", e.Body)
	}
	return fmt.Sprintf("LLM 返回错误 (status=%d): %s", e.Status, e.Body)
}

func (e *llmHTTPError) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errLLMAuth
	case http.StatusTooManyRequests:
		return errLLMRateLimited
	}
	if e.Status >= 500 {
		return errLLMServerFault
	}
	return nil
}

// 三个哨兵错误，供上层按类别判断（errors.Is）。目前还没有调用方用到，
// 但重试层已经把它们接出来了——将来要在界面上分「密钥不对」和「被限流」两种文案时，
// 不必再回来改这里。
var (
	errLLMAuth        = errors.New("认证失败")
	errLLMRateLimited = errors.New("被限流")
	errLLMServerFault = errors.New("服务端故障")
)

// newLLMHTTPError 由一次非 2xx 响应构造错误。
func newLLMHTTPError(status int, body []byte, h http.Header) error {
	return &llmHTTPError{
		Status:     status,
		Body:       truncateRunes(string(body), llmErrorBodyMax),
		RetryAfter: parseRetryAfter(h.Get("Retry-After")),
	}
}

// sseStatusFor 把流内错误事件给出的类型名折算成一个**等效 HTTP 状态码**。
//
// 为什么要绕这一道：重试与否的判断已经全部写在 shouldRetry 里，而它认的是状态码。
// 与其再写一套并行的「流内错误该不该重试」的规则（那就是第二份会漂移的实现），
// 不如把服务端给的错误类型折算成状态码，复用同一套判断。
//
// 认不出来的一律落到 400，也就是**不重试**。这个方向是刻意选的：
// 把不可重试的错当成可重试，代价只是白等几次退避；反过来则是把该重试的错直接判死，
// 而后者正是这次要修的问题。所以这里本来该偏向重试——但类型名完全认不出来时，
// 我们连它是不是限流都不知道，唯一稳妥的假设是「请求本身有问题」，
// 否则会把一个明显的 400（比如模型名写错）重试四遍。
func sseStatusFor(typeName, message string) int {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "overloaded_error":
		return 529
	case "api_error", "internal_server_error", "server_error":
		return http.StatusInternalServerError
	case "rate_limit_error", "rate_limit_exceeded":
		return http.StatusTooManyRequests
	case "timeout_error":
		return http.StatusRequestTimeout
	}
	// 类型名缺失或认不出来时退回看正文：限流常常没有规范的 type 字段，
	// 但正文里几乎一定会提到它。
	msg := strings.ToLower(message)
	for _, k := range []string{"rate limit", "too many requests", "overloaded", "try again"} {
		if strings.Contains(msg, k) {
			return http.StatusTooManyRequests
		}
	}
	return http.StatusBadRequest
}

// llmStreamError 由流内错误事件构造错误，复用 HTTP 层那套分类。
//
// 这一处不能漏：OpenAI 系的错误是当作一个普通 data 帧回来的（HTTP 状态仍是 200），
// 不显式识别它，它就会被当成「没有 choices 的正常帧」静默跳过，
// 最后表现为「LLM 返回空响应」——把一次限流误报成空回复，而且永远不会触发重试。
func llmStreamError(typeName, message string) error {
	if strings.TrimSpace(message) == "" {
		message = "服务端未给出错误说明"
	}
	return &llmHTTPError{
		Status: sseStatusFor(typeName, message),
		Body:   truncateRunes(message, llmErrorBodyMax),
		Stream: true,
	}
}

// shouldRetry 给定一次尝试的错误，判断是否还值得再试。
//
// 四类**明确不重试**：
//
//   - 4xx 里除 408 / 429 之外的全部。请求本身有问题，重试只会拿到同样的错；
//     认证失败（401/403）尤其如此，重试是纯浪费。
//
//   - **上下文超限**。它多数时候以 400 出现，但它有自己的处置路径：
//     runToolLoop 收到后会压缩上下文再试一次。在这里先盲目重试，等于把重试额度
//     烧光，等真正需要压缩兜底时反而没机会了。这类错误各家措辞不同，只能看正文。
//
//   - ctx 已结束。那是用户点了停止、或上层限时到了，重试等于无视用户意图。
//
//   - 明显的配置错误（证书、协议、URL 形状）。重试几百毫秒也还是同样的错。
func shouldRetry(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var he *llmHTTPError
	if errors.As(err, &he) {
		// 上下文超限优先于状态码判定：它常常以 400 出现，但绝不能被当作
		// "普通 400"之外的任何东西——这里必须先把它摘出去。
		if isContextOverflowError(errors.New(he.Body)) {
			return false
		}
		switch he.Status {
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return true
		}
		return he.Status >= 500
	}
	return networkRetryable(err)
}

// networkRetryable 传输层错误（根本没拿到响应）是否值得重试。
//
// 默认取向是「值得」：连接被 reset、对端半开、DNS 抖一下，这些重试一次往往就好了，
// 而一次失败的代价是整轮对话作废。只排除两类不会自愈的：
//
//   - TLS / 证书问题：配置错了，重试只是把同样的失败再来三遍。
//   - URL 形状错误（scheme 不认识、编码非法）：重试无意义。
//
// 刻意**不**排除 "no such host"：域名解析失败确实多半是配置写错，但真出现瞬时 DNS
// 故障时，多等 3.5 秒远比让用户重来一轮便宜。
func networkRetryable(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, k := range []string{
		"x509:", "certificate", "unsupported protocol scheme", "invalid url",
	} {
		if strings.Contains(msg, k) {
			return false
		}
	}
	return true
}

// parseRetryAfter 解析 Retry-After 头。
//
// 只认「秒数」这一种形式。HTTP-date 形式在实践中几乎见不到，而支持它要引入一套
// 日期解析与失败分支——那些分支在本项目里永远不会被执行到，却要一直被维护。
// 认不出来就返回 0、退回指数退避：这个头是服务端的**建议**，不是约束。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// backoffFor 第 attempt 次失败（attempt 从 1 开始）之后该等多久。
//
// 第二个返回值表示「这个等待是否可接受」：服务端给的 Retry-After 超过策略上限时
// 返回 false，调用方应当直接失败而**不是**安静地等下去。
//
// 抖动用「等值抖动」——在 [d/2, d] 之间取随机值，而不是固定 d。
// 目的是让并发的几个请求不要同时醒来再一起撞上去（本项目支持多会话并行，
// 这不是理论问题）。下限留 d/2 而不是 0：退避退成 0 等于没有退避，
// 而全额抖动 [0, d] 的期望只有 d/2，等于把上限设计得再大一倍才能拿到同样的等待。
func backoffFor(p retryPolicy, attempt int, retryAfter time.Duration) (time.Duration, bool) {
	if retryAfter > 0 {
		if retryAfter > p.MaxWait {
			return retryAfter, false
		}
		return retryAfter, true
	}
	d := p.BaseDelay
	for i := 1; i < attempt && d < p.MaxDelay; i++ {
		d *= 2
	}
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	if d <= 0 {
		return 0, true
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(d-half)+1)), true
}

// sleepCtx 可中断的等待。返回 false 表示 ctx 已结束，调用方应立即收手。
//
// 不能用 time.Sleep：那样用户在退避期间点「停止」，要等退避走完才生效，
// 表现为几秒钟的「点了没反应」——这正是本项目在别处反复避免的那类体感缺陷。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// retryAfterOf 取错误里携带的 Retry-After；没有则返回 0。
func retryAfterOf(err error) time.Duration {
	var he *llmHTTPError
	if errors.As(err, &he) {
		return he.RetryAfter
	}
	return 0
}

// withRetry 按策略执行 attempt，直到成功、判定不可重试、或尝试次数用尽。
//
// giveUp 在**每次失败之后**被问一次「就算它是可重试的错，还要不要继续」。
// 它是流式路径的安全阀：一旦已经有分片推给了前端，就不能再重试了
// （前端是追加渲染的，重试会把同一段话再说一遍）。这个判断只能在失败之后做——
// 只有到那时才知道这一轮到底吐没吐过内容。
//
// 用泛型是为了让流式与非流式共用同一个循环：两条各写一份的话，
// 「重试了几次、什么时候放弃」这些规则迟早会不一致。
func withRetry[T any](ctx context.Context, p retryPolicy, giveUp func() bool, attempt func() (T, error)) (T, error) {
	var zero T
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	var lastErr error
	for i := 1; i <= p.MaxAttempts; i++ {
		v, err := attempt()
		if err == nil {
			if i > 1 {
				fmt.Printf("[llm] 第 %d 次尝试成功（前 %d 次失败）\n", i, i-1)
			}
			return v, nil
		}
		lastErr = err

		if !shouldRetry(ctx, err) {
			return zero, err
		}
		if giveUp != nil && giveUp() {
			return zero, err
		}
		if i == p.MaxAttempts {
			break
		}

		wait, ok := backoffFor(p, i, retryAfterOf(err))
		if !ok {
			return zero, fmt.Errorf("%w（服务端要求等待 %s，超过重试上限）", err, wait)
		}
		fmt.Printf("[llm] 第 %d 次尝试失败（%v），%s 后重试\n", i, err, wait.Round(time.Millisecond))
		if !sleepCtx(ctx, wait) {
			// ctx 在退避期间结束：返回这次失败，而不是"兜底成功"。
			return zero, err
		}
	}
	return zero, lastErr
}
