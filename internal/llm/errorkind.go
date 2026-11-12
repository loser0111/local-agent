package llm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
)

// ===== 一次模型调用失败的性质分类 =====
//
// 为什么需要这一层：`ShouldRetry` 只回答"这次值不值得再试"，而计划执行层要回答
// "这一步该不该自动重来、失败原因该怎么向用户说"。两者依据同一批信号，但一个是布尔、
// 一个是可读的分类——直接在计划层再写一遍状态码判断，就是本项目在权限网关与工具曝光
// 策略上各吃过一次的亏：两处实现迟早漂移。
//
// **本文件不新增任何判定口径**：它把 `ShouldRetry` 已经在用的信号（`LLMHTTPError.Status`、
// ctx 是否结束、传输层错误、配置错误清单）摊开成枚举。因此有一条硬约束——
// `kind.Retryable()` 必须与 `ShouldRetry(context.Background(), err)` 对同一个错误给出
// 同样的答案，由 errorkind_test.go 的一致性用例守住。
type ErrorKind string

const (
	// ErrorKindNone 没有错误（nil）。
	ErrorKindNone ErrorKind = ""

	// ErrorKindAborted ctx 已结束：用户点了停止，或上层限时到了。
	//
	// 独立成一类而不是并进 timeout：它的正确处置是**立刻收手**，而超时的正确处置是
	// 再等一会儿重试。混在一起会让"用户已经点了停止"的请求在后台又被重试一遍。
	// context.DeadlineExceeded 也归这里——它同样是"调用方的耐心用完了"，
	// 与"对端响应慢"（timeout）不是一回事。
	ErrorKindAborted ErrorKind = "aborted"

	// ErrorKindTimeout 超时但 ctx 还活着：408，或传输层报告超时。
	ErrorKindTimeout ErrorKind = "timeout"

	// ErrorKindNetwork 传输层失败（连接被 reset、对端半开、DNS 抖动），以及无从归类的错误。
	//
	// 无从归类的错误一律落这里，与 networkRetryable 的默认取向一致：宁可多试一次，
	// 也不要把一次抖动判成整轮失败。这不是"猜它是网络问题"，而是"默认按可重试处理"。
	ErrorKindNetwork ErrorKind = "network"

	// ErrorKindRateLimit 被限流（429）。
	ErrorKindRateLimit ErrorKind = "rate_limit"

	// ErrorKindServer 对端故障（5xx）。
	ErrorKindServer ErrorKind = "server"

	// ErrorKindAuth 认证失败（401/403）：密钥不对或没有权限。
	ErrorKindAuth ErrorKind = "auth"

	// ErrorKindBadRequest 请求本身有问题（除 408/429 之外的 4xx）。
	ErrorKindBadRequest ErrorKind = "bad_request"

	// ErrorKindContextOverflow 上下文超长。
	//
	// 它有**自己的处置路径**（压缩后重试，见 runToolLoop），因此不能按"普通可重试错误"
	// 反复重试——那会把重试额度烧光，等真正需要压缩兜底时反而没机会了。
	ErrorKindContextOverflow ErrorKind = "context_overflow"

	// ErrorKindConfig 明显的配置错误（TLS/证书、scheme 不认识、URL 形状非法）：
	// 重试几百毫秒后还是同样的错，应当直接告诉用户去改配置。
	ErrorKindConfig ErrorKind = "config"
)

// configErrorMarkers 重试也不会变好的配置类错误标记。
//
// 由 networkRetryable 与 isConfigError 共用**同一份清单**：分叉成两份就会出现
// "ShouldRetry 说不试了、但 Retryable 说要试"的自相矛盾。
var configErrorMarkers = []string{
	"x509:", "certificate", "unsupported protocol scheme", "invalid url",
}

// ClassifyError 把一次模型调用失败归类。
//
// 判定顺序是有讲究的：
//  1. ctx 错误必须排在最前。context.DeadlineExceeded 也实现了 net.Error 且 Timeout() 为真，
//     先判 net.Error 会把它误判成可重试的超时。
//  2. 上下文超限要排在状态码判断之前：它多数以 400 出现，但绝不能当成"普通 400"。
//  3. 状态码来自 LLMHTTPError，不解析错误文案——文案改一次就会静默失效。
func ClassifyError(err error) ErrorKind {
	if err == nil {
		return ErrorKindNone
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorKindAborted
	}

	var he *LLMHTTPError
	if errors.As(err, &he) {
		if IsContextOverflowError(errors.New(he.Body)) {
			return ErrorKindContextOverflow
		}
		switch {
		case he.Status == http.StatusRequestTimeout:
			return ErrorKindTimeout
		case he.Status == http.StatusTooManyRequests:
			return ErrorKindRateLimit
		case he.Status == http.StatusUnauthorized, he.Status == http.StatusForbidden:
			return ErrorKindAuth
		case he.Status >= 500:
			return ErrorKindServer
		default:
			return ErrorKindBadRequest
		}
	}

	// 传输层超时（还没拿到响应就超时了）：值得重试，与"用户放弃等待"区分开。
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrorKindTimeout
	}

	if isConfigError(err) {
		return ErrorKindConfig
	}
	return ErrorKindNetwork
}

// Retryable 这一类失败是否值得再试一次。
//
// 与 ShouldRetry 保持一致（见本文件头部的一致性约束），但**不看 ctx**：
// ctx 是否结束是"此刻还能不能试"，是调用时刻的状态；这里回答的是"这类错误本身"。
// 计划执行层因此必须两者都查：先看 Retryable，再看自己的取消信号。
func (k ErrorKind) Retryable() bool {
	switch k {
	case ErrorKindTimeout, ErrorKindNetwork, ErrorKindRateLimit, ErrorKindServer:
		return true
	default:
		return false
	}
}

// isConfigError 是否是"重试也不会变"的配置类错误。
func isConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range configErrorMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
