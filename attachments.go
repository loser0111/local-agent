package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ===== 附件存储（图片等二进制资产，与会话消息分离落盘）=====
//
// 为什么不把图片 base64 塞进会话 JSON：
//
//  1. 会话文件是**每次追加消息都整体重写**的（SessionStore.saveSession 用 MarshalIndent
//     写整个 Session），一张 3MB 的图 base64 后约 4MB，会话里贴三张就是十几 MB，
//     此后每发一条消息都要把这十几 MB 读出来再写回去。
//  2. ListSessions 要读目录下**全部**会话文件才能拿到元数据，单文件膨胀会直接拖慢会话列表。
//  3. base64 只是传输编码，没有任何"存下来"的理由——需要它的只有发请求那一刻。
//
// 目录布局（content-addressed，同一张图在同一个会话里只存一份）：
//
//	~/.local-agent/attachments/<会话ID>/<sha256 前 16 位>.<ext>
//
// 刻意**不写进工作区**：图片是对话资产而不是代码资产。写进工作区会被
// diff 归因/回退机制当成文件改动扫到（用户回退一轮代码时，图会跟着被"回退"），
// 还会逼用户往 .gitignore 里加东西。

// 附件类型与来源（落盘后仍可区分"用户贴的"与"模型自己读的"）
const (
	attachmentKindImage  = "image"
	attachmentSourceUser = "user"
	attachmentSourceTool = "tool"
)

// Attachment 一条附件在会话里的**引用**。真正的字节躺在 AttachmentStore 的目录下。
//
// 它同时是两件事的载体：界面展示（名字/尺寸/缩略图）与协议组装（宽高算 token、
// 媒体类型拼 data URL）。因此字段宁多勿少——少一个字段就意味着另外找一个地方重算。
type Attachment struct {
	ID        string `json:"id"`                  // 内容寻址 ID：规整后字节的 sha256 前 16 位
	Kind      string `json:"kind"`                // image（目前唯一）
	Name      string `json:"name"`                // 展示用文件名（用户贴图时的原始名）
	MediaType string `json:"mediaType"`           // 送给模型的媒体类型
	Bytes     int64  `json:"bytes"`               // 规整后的字节数
	Width     int    `json:"width,omitempty"`     // 像素宽
	Height    int    `json:"height,omitempty"`    // 像素高
	Source    string `json:"source,omitempty"`    // user / tool
	Changed   bool   `json:"changed,omitempty"`   // 是否被缩放或重编码过
	Path      string `json:"path,omitempty"`      // 相对附件根的路径（写入时记录，读取时校验）
	CreatedAt int64  `json:"createdAt,omitempty"` // Unix 毫秒
}

// Describe 单行人类可读描述，用于界面与工具结果文本
func (a Attachment) Describe() string {
	name := a.Name
	if strings.TrimSpace(name) == "" {
		name = a.ID
	}
	dim := ""
	if a.Width > 0 && a.Height > 0 {
		dim = fmt.Sprintf("%d×%d，", a.Width, a.Height)
	}
	return fmt.Sprintf("%s（%s%s，%.0f KB）", name, dim, a.MediaType, float64(a.Bytes)/1024)
}

// AttachmentStore 附件根目录下的读写。所有路径都由 ID 与媒体类型推导，
// 前端传来的文件名/路径一律不参与拼接（防路径穿越）。
type AttachmentStore struct {
	root string
	mu   sync.Mutex
}

// NewAttachmentStore 创建附件存储（目录按需创建，这里不预建：
// 从没贴过图的用户不该多出一个空目录）
func NewAttachmentStore(root string) *AttachmentStore {
	return &AttachmentStore{root: root}
}

// Root 附件根目录（排障用）
func (s *AttachmentStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// safePathSegment 校验单个路径段：只允许字母数字与 _ -，且不为空。
// 会话 ID 与附件 ID 都由此把关——它们是唯一参与路径拼接的外部输入。
func safePathSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// sessionDir 某会话的附件目录（已校验会话 ID）
func (s *AttachmentStore) sessionDir(sessionID string) (string, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return "", fmt.Errorf("附件存储未初始化")
	}
	if !safePathSegment(sessionID) {
		return "", fmt.Errorf("非法会话 ID: %q", sessionID)
	}
	return filepath.Join(s.root, sessionID), nil
}

// Save 规整并落盘一张图片，返回可写进消息的引用。
//
// source 只影响展示与排障（user=用户贴的 / tool=模型读的），不参与任何判定。
func (s *AttachmentStore) Save(sessionID, name string, data []byte, source string) (*Attachment, error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return nil, err
	}
	norm, err := normalizeImage(data)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(norm.Data)
	id := hex.EncodeToString(sum[:8]) // 16 位十六进制：碰撞概率对"单会话图片"这个量级足够
	ext := imageExtFor(norm.MediaType)
	rel := filepath.Join(sessionID, id+ext)
	abs := filepath.Join(dir, id+ext)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建附件目录失败: %w", err)
	}
	// 内容寻址天然去重：同一张图重发时文件已在，跳过写入。
	// 但**不跳过**返回——消息里该有一条引用，缺了它模型就看不到这张图。
	if _, statErr := os.Stat(abs); statErr != nil {
		if err := os.WriteFile(abs, norm.Data, 0o644); err != nil {
			return nil, fmt.Errorf("写入附件失败: %w", err)
		}
	}

	if source == "" {
		source = attachmentSourceUser
	}
	return &Attachment{
		ID:        id,
		Kind:      attachmentKindImage,
		Name:      displayNameFor(name, id, ext),
		MediaType: norm.MediaType,
		Bytes:     int64(len(norm.Data)),
		Width:     norm.Width,
		Height:    norm.Height,
		Source:    source,
		Changed:   norm.Changed,
		Path:      filepath.ToSlash(rel),
		CreatedAt: time.Now().UnixMilli(),
	}, nil
}

// displayNameFor 展示名：优先用调用方给的名字；为空时用 ID+扩展名兜底。
// 名字只影响展示，落盘路径永远由 ID 决定——两者刻意解耦。
func displayNameFor(name, id, ext string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return id + ext
	}
	// 展示名也不该长到撑坏气泡：截断到 80 字符（按 rune，中文文件名不被切坏）
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80]) + "…"
	}
	return name
}

// Load 读回附件的字节。att.Path 由写入时记录，这里必须校验它没跑出根目录——
// 会话文件是用户可手改的 JSON，路径字段等同于外部输入。
func (s *AttachmentStore) Load(att Attachment) ([]byte, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil, fmt.Errorf("附件存储未初始化")
	}
	rel := strings.TrimSpace(filepath.ToSlash(att.Path))
	if rel == "" {
		return nil, fmt.Errorf("附件缺少路径")
	}
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if !pathInside(s.root, abs) {
		return nil, fmt.Errorf("附件路径越界: %q", att.Path)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("附件文件已丢失: %s", att.Name)
		}
		return nil, fmt.Errorf("读取附件失败: %w", err)
	}
	return data, nil
}

// pathInside 判断 child 是否落在 root 之内（两者都按清理后的绝对形式比较）
func pathInside(root, child string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	c, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	r = filepath.Clean(r)
	c = filepath.Clean(c)
	if r == c {
		return true
	}
	return strings.HasPrefix(c, r+string(filepath.Separator))
}

// DataURL 按 ID 取出附件并编码为 data URL（供界面展示）。
//
// 只收 (会话ID, 附件ID)，不接受路径：界面拿到的引用来自消息，但拼路径这件事
// 一旦交给调用方，就等于把"读任意文件"的能力开给了前端。这里按 ID 在会话目录里找，
// 找不到就是文件确实丢了——那种情况必须能区分出来（界面要提示"图片已丢失"，
// 而不是显示一个破图）。
func (s *AttachmentStore) DataURL(sessionID, id string) (string, error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return "", err
	}
	if !safePathSegment(id) {
		return "", fmt.Errorf("非法附件 ID: %q", id)
	}
	matches, err := filepath.Glob(filepath.Join(dir, id+".*"))
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("附件不存在或已删除: %s", id)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return "", fmt.Errorf("读取附件失败: %w", err)
	}
	mediaType := sniffImageMediaType(data)
	if mediaType == "" {
		return "", fmt.Errorf("附件不是受支持的图片: %s", id)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// DeleteSession 删除某会话的全部附件（会话被删除时级联调用）。
// 附件不在会话列表里，会话删掉之后就再没有任何入口能发现它们——不清理就是永久垃圾。
func (s *AttachmentStore) DeleteSession(sessionID string) error {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		return nil
	}
	return os.RemoveAll(dir)
}

// DecodeUpload 解析前端上传的图片负载：接受裸 base64，也接受 data URL 形式。
//
// 为什么走 base64 字符串而不是 []byte 参数：Wails 对 []byte 的绑定依赖它自己的
// 生成规则（JSON 里是 base64，JS 侧要构造 Uint8Array），不同版本行为不一致；
// 而字符串是确定的，前端 FileReader 直接就能给。
func DecodeUpload(payload string) ([]byte, error) {
	p := strings.TrimSpace(payload)
	if p == "" {
		return nil, fmt.Errorf("图片内容为空")
	}
	if strings.HasPrefix(p, "data:") {
		i := strings.Index(p, ",")
		if i < 0 {
			return nil, fmt.Errorf("data URL 格式不正确")
		}
		meta := p[len("data:"):i]
		if !strings.Contains(meta, "base64") {
			return nil, fmt.Errorf("仅支持 base64 编码的 data URL")
		}
		p = p[i+1:]
	}
	// 先按长度粗筛：base64 解出来比原始字节多约 1/3，超限就别浪费内存去解。
	// 真正的大小校验在 normalizeImage 里（那里面对的是解码后的字节）。
	if int64(len(p)) > maxImageSourceBytes*4/3+4096 {
		return nil, fmt.Errorf("图片过大（上限 %d MB）", maxImageSourceBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(p)
	if err != nil {
		// 容忍缺失的 padding（部分前端实现会去掉 '='）
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(p, "="))
		if err != nil {
			return nil, fmt.Errorf("base64 解码失败: %v", err)
		}
	}
	return data, nil
}

// ===== 工具产图通道 =====
//
// read_image 这类工具的**结果是文本，但真正要给模型看的是图**。
// 把 "工具结果里夹带图片" 这件事显式建模成一个旁路通道，而不是往文本里塞
// 约定标记（那需要每个消费方都去解析标记，漏一处就静默丢图）：
//
//	工具 → collector.add(Attachment) → 工具循环在调用返回后 take() → 落到该次
//	工具结果消息的 Attachments 上 → 与用户贴的图走**完全相同**的存储与协议路径。
//
// 一次性取走（take）而不是只读：工具循环是串行执行 tool_calls 的，
// 每次执行完立刻取走，就不会出现"这两张图是哪一次调用产的"这种无法回答的问题。
type imageCollector struct {
	mu    sync.Mutex
	items []Attachment
}

// add 记录一张工具产出的图片
func (c *imageCollector) add(att Attachment) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, att)
}

// take 取出并清空当前累计的图片
func (c *imageCollector) take() []Attachment {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.items
	c.items = nil
	return out
}

// attachmentIDs 取附件 ID 列表（工具卡片只存 ID，见 ToolCall.Images 的说明）
func attachmentIDs(atts []Attachment) []string {
	out := make([]string, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.ID)
	}
	return out
}
