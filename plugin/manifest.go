package plugin

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ===== 插件清单（manifest）=====
//
// 清单是插件的「描述文件 + 自检依据」：
//   - 声明插件身份（id / name / version）与落地形态（kind=builtin，编译期内置）；
//   - 声明所依赖的宿主接口版本（hostAPIVersion），主程序据此判断能否加载；
//   - 声明能力、数据文件、事件通道、所需宿主方法。
//
// 清单内嵌进二进制（go:embed），因此「加载」= 解析 + 校验内嵌 JSON，
// 不依赖任何运行时文件，也不会因为磁盘文件被改动而改变插件行为
// （符合附录 §6「信任边界」：磁盘内容不完全可信）。

// HostAPIVersion 是本插件所依赖的宿主接口版本。
//
// 主版本不同视为不兼容（拒绝加载）；次版本向后兼容。
const HostAPIVersion = "1.0"

// Manifest 是插件清单。
type Manifest struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	HostAPIVersion string   `json:"hostAPIVersion"`
	Kind           string   `json:"kind"`
	Entry          string   `json:"entry"`
	Capabilities   []string `json:"capabilities,omitempty"`
	DataFiles      []string `json:"dataFiles,omitempty"`
	Emits          []string `json:"emits,omitempty"`
	HostMethods    []string `json:"hostMethods,omitempty"`
	Docs           []string `json:"docs,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

// LoadManifest 解析并校验内嵌清单。
func LoadManifest() (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return Manifest{}, fmt.Errorf("插件清单解析失败: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate 校验清单的必填项与宿主接口兼容性。
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("插件清单缺少 id")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("插件清单缺少 version")
	}
	if strings.TrimSpace(m.Entry) == "" {
		return fmt.Errorf("插件清单缺少 entry")
	}
	if m.Kind != "builtin" {
		// ADR-4：本插件是编译期内置模块，不支持热插拔。
		return fmt.Errorf("插件清单 kind=%q 不受支持（本插件为编译期内置，须为 builtin）", m.Kind)
	}
	if err := checkHostAPICompatible(m.HostAPIVersion); err != nil {
		return err
	}
	// 清单声明的宿主方法必须与 Host 接口实际方法完全一致，
	// 防止接口改了而清单没改（清单是给人看的契约，不该悄悄失真）。
	want := hostMethodNames()
	got := append([]string(nil), m.HostMethods...)
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("插件清单 hostMethods 与 Host 接口不一致: 清单=%v 接口=%v", got, want)
	}
	if !m.SupportsCapability("notify.toast") {
		return fmt.Errorf("插件清单缺少 notify.toast 能力")
	}
	return nil
}

// SupportsCapability 判断清单是否声明了某项能力。
func (m Manifest) SupportsCapability(capability string) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// checkHostAPICompatible 比较主版本号，主版本不同即不兼容。
func checkHostAPICompatible(declared string) error {
	if strings.TrimSpace(declared) == "" {
		return fmt.Errorf("插件清单缺少 hostAPIVersion")
	}
	wantMajor, err := majorOf(HostAPIVersion)
	if err != nil {
		return err
	}
	gotMajor, err := majorOf(declared)
	if err != nil {
		return fmt.Errorf("插件清单 hostAPIVersion=%q 非法: %w", declared, err)
	}
	if wantMajor != gotMajor {
		return fmt.Errorf("插件与宿主接口不兼容: 插件要求 hostAPIVersion=%s，宿主为 %s", declared, HostAPIVersion)
	}
	return nil
}

func majorOf(v string) (int, error) {
	head := strings.SplitN(strings.TrimSpace(v), ".", 2)[0]
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0, fmt.Errorf("%q 的主版本号不是数字", v)
	}
	return n, nil
}

// hostMethodNames 反射列出 Host 接口的方法名。
func hostMethodNames() []string {
	t := reflect.TypeOf((*Host)(nil)).Elem()
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	return names
}
