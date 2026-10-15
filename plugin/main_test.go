package plugin

import (
	"os"
	"testing"
	"time"
)

// TestMain 把测试进程的本地时区钉死为 UTC+8。
//
// 为什么非钉不可：这个包的时间用例有两套写法，只有「本机时区 = +08:00」时才自洽 ——
//   - service_test.go 的 baseTime()、plugin_test.go 的固定时钟用 time.Date(..., time.Local)，
//     表达的是「本机本地时间」；
//   - schedule_test.go 用 mustTime("...T09:30:00+08:00") 写数据，断言比的却是
//     Format("2006-01-02 15:04") 出来的**本机墙上时间**。
//
// 而 schedule.go 的日历计算全部落在 time.Local 上（NextAfter 开头的 now.Local()、
// nextRecurring 里的 time.Date(..., time.Local)）。本机时区一旦不是 +08:00，两者就错开：
// 「本地 10:00 已过 09:30 → 下一次是明天」在 UTC 下变成「本地才 02:00 → 下一次就是今天」，
// 断言随即落空。开发机在 +08:00，所以本地一直看不出来；CI（ubuntu-latest，TZ=UTC）直接红：
//
//	schedule_test.go:80: got 2025-06-10 09:30:00 +0000 UTC, true
//	schedule_test.go:217: 应为 2025-06-16 09:00（周一），实际 2025-06-13 09:00:00 +0000 UTC
//
// 钉在 FixedZone(+8h) 而不是 LoadLocation("Asia/Shanghai")：这些用例本身就声明了
// 「夏令时无关的本地时间」，固定偏移既不需要 tzdata、也不受容器时区设置影响，
// 任何机器与 CI 上跑出来都一致。
//
// 另注：Go 在 Windows 上不认 TZ 环境变量，靠 `TZ=UTC go test` 是复现不出来的
// （实测照过），只能像这里一样在测试进程内钉。
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("CST", 8*3600)
	os.Exit(m.Run())
}

// TestLocalZoneIsPinned 守住上面那个钉死本身。
//
// 少了它，TestMain 被删掉或挪走时不会有任何报错——用例只是在 CI 上（UTC）静默变红，
// 而失败原因又指向 schedule 的日历断言，排查时很容易往错的方向找。
func TestLocalZoneIsPinned(t *testing.T) {
	if _, off := time.Date(2025, 6, 10, 12, 0, 0, 0, time.Local).Zone(); off != 8*3600 {
		t.Fatalf("测试进程的本地时区偏移为 %d 秒，应为 28800（UTC+8）——"+
			"日历用例的数据按 +08:00 写，时区一旦漂移就会在 CI 上失败（见 TestMain 注释）", off)
	}
}
