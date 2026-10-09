package task

import (
	"context"
	"testing"
)

// 进程级单例的生命周期语义。
//
// 这里只测"不装配也要正确"的那一半：根包的 e2e（taskapp_e2e_test.go）走真实宿主适配器
// 跑完整装配，而 Reset/Current/Stop 的边界——尤其"没有实例时不得 panic"——只能在包内
// 直接钉住（根包看不到 state）。
func TestLifecycleWithoutInstance(t *testing.T) {
	Reset()
	if p := Current(); p != nil {
		t.Fatalf("Reset 后应没有实例，实际 %+v", p.Info())
	}

	// 没有实例时停止/复位都不应 panic（退出流程与测试复位都会碰到）
	Stop()
	Reset()

	// 数据目录为空 → 跳过加载，不得登记任何实例（宿主侧 App 未就绪时走的就是这条路）
	Start(context.Background(), nil, "", "0.0.0")
	if p := Current(); p != nil {
		t.Fatalf("数据目录为空时不该装配插件，实际 %+v", p.Info())
	}

	// Reset 幂等
	Reset()
	if p := Current(); p != nil {
		t.Fatalf("再次 Reset 后仍应没有实例，实际 %+v", p.Info())
	}
}
