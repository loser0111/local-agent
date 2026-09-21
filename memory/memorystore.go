package memory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ===== 常量与配置 =====

// 记忆类型（对标 Claude Code 四分类）
var (
	TypeUser      = "user"      // 用户偏好/个人信息
	TypeFeedback  = "feedback"  // 用户对结果的反馈/评价
	TypeProject   = "project"   // 项目约束/决策/架构知识
	TypeReference = "reference" // 参考资料/文档/命令/链接
)

// 作用域
var (
	ScopeUser    = "user"    // 全局，跨项目
	ScopeProject = "project" // 项目级，按项目 slug 隔离
)

// 重要性
var (
	ImportanceLow    = "low"
	ImportanceMedium = "medium"
	ImportanceHigh   = "high"
)

// MemoryConfig 记忆模块配置（对应 memory/config.json）。
type MemoryConfig struct {
	MaxTopK           int  `json:"maxTopK"`
	MaxPerScope       int  `json:"maxMemoriesPerScope"`
	InjectionMaxChars int  `json:"injectionMaxChars"`
	PIIFilter         bool `json:"piiFilter"`
	StalenessCaveat   bool `json:"stalenessCaveat"`
}

// WithDefaults 返回补全默认值的配置（零值字段填充默认）。
func (c MemoryConfig) WithDefaults() MemoryConfig {
	if c.MaxTopK == 0 {
		c.MaxTopK = 5
	}
	if c.MaxPerScope == 0 {
		c.MaxPerScope = 400
	}
	if c.InjectionMaxChars == 0 {
		c.InjectionMaxChars = 800
	}
	return c
}

// ===== 数据模型 =====

// MemoryMeta 索引条目（memory/index.json 的 memories 数组元素）。
type MemoryMeta struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"` // 一行摘要，用于相关性判断
	Type          string   `json:"type"`
	Scope         string   `json:"scope"`   // ScopeUser | ScopeProject
	Project       string   `json:"project"` // ScopeProject 时填项目 slug
	Path          string   `json:"path"`    // 相对 memory/ 的主题文件路径（forward slash）
	Tags          []string `json:"tags,omitempty"`
	Importance    string   `json:"importance"`
	Source        string   `json:"source,omitempty"`        // 来源会话 id（溯源）
	SourceSummary string   `json:"sourceSummary,omitempty"` // 来源一句话上下文
	CreatedAt     int64    `json:"createdAt"`               // Unix 毫秒
	UpdatedAt     int64    `json:"updatedAt"`
	ExpiresAt     int64    `json:"expiresAt"` // 0=永久
}

// MemoryIndex 整个记忆库的索引。
type MemoryIndex struct {
	Version  int          `json:"version"`
	Memories []MemoryMeta `json:"memories"`
}
type MemoryEntry struct {
	Meta    MemoryMeta
	Content string
	Age     string // "today"|"yesterday"|"N days ago"
	Stale   bool   // 项目级且 >1 天 → 触发 drift caveat
}

// ===== 存储 =====

// MemoryStore 长期记忆存储（对标 sessions.go 的 SessionStore）。
// 线程安全。
type MemoryStore struct {
	mu  sync.RWMutex
	dir string // memory 根目录
	cfg MemoryConfig
	idx *MemoryIndex
	pii *regexp.Regexp
}

const (
	defaultConfigFile = "config.json"
	defaultIndexFile  = "index.json"
)

// NewMemoryStore 在 dir 下创建/复用记忆存储，确保目录与权限存在并加载 index。
func NewMemoryStore(dir string, cfg MemoryConfig) (*MemoryStore, error) {
	cfg = cfg.WithDefaults()
	absDir, aerr := filepath.Abs(dir)
	if aerr != nil {
		return nil, fmt.Errorf("解析记忆目录失败: %w", aerr)
	}
	m := &MemoryStore{
		dir: absDir,
		cfg: cfg,
		pii: piiPattern(),
		idx: &MemoryIndex{Version: 1},
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建记忆目录失败: %w", err)
	}
	for _, sub := range []string{"user", "project"} {
		if err := os.MkdirAll(filepath.Join(m.dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("创建子目录 %s 失败: %w", sub, err)
		}
	}
	b, err := os.ReadFile(m.idxPath())
	if err != nil {
		if os.IsNotExist(err) {
			// 首次运行无 index，正常
			m.idx = &MemoryIndex{Version: 1}
		} else {
			return nil, fmt.Errorf("读取索引失败: %w", err)
		}
	} else {
		var idx *MemoryIndex
		if err := json.Unmarshal(b, &idx); err != nil {
			return nil, fmt.Errorf("解析索引失败: %w", err)
		}
		if idx == nil {
			idx = &MemoryIndex{Version: 1}
		}
		m.idx = idx
	}
	cfgB, err := os.ReadFile(m.cfgPath())
	if err == nil {
		var c MemoryConfig
		if err := json.Unmarshal(cfgB, &c); err != nil {
			return nil, fmt.Errorf("解析配置失败: %w", err)
		}
		m.cfg = c.WithDefaults()
	}
	return m, nil
}

func (m *MemoryStore) idxPath() string { return filepath.Join(m.dir, defaultIndexFile) }

func (m *MemoryStore) cfgPath() string { return filepath.Join(m.dir, defaultConfigFile) }

// writeJSONAtomic 原子写（临时文件 + Rename），避免崩溃时半写。
func writeJSONAtomic(path string, v interface{}) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := json.NewEncoder(f).Encode(v); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ===== 安全辅助 =====

func isSafeRelSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for i, r := range []byte(s) {
		if r == 0 || (i == 0 && (r == '/' || r == '\\' || r == ':')) {
			return false
		}
		if r < 0x20 || r == 0x7F {
			return false
		}
	}
	return true
}

// sanitizeRelPath 校验并规范化相对 memory/ 的路径。
func sanitizeRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径为空")
	}
	if filepath.IsAbs(p) || strings.ContainsRune(p, 0) ||
		strings.ContainsRune(p, '\n') {
		return "", fmt.Errorf("拒绝绝对或含非法字符路径: %q", p)
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(p, "\\", "/"), " ", "_")
	if filepath.IsAbs(normalized) {
		return "", fmt.Errorf("拒绝绝对路径: %q", p)
	}
	for _, seg := range strings.FieldsFunc(normalized, func(r rune) bool { return r == '/' }) {
		if !isSafeRelSegment(seg) {
			return "", fmt.Errorf("路径含非法段 %q: %q", seg, p)
		}
	}
	return normalized, nil
}

// genID 生成唯一 id：mem_ + 16 位十六进制。
func genID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}
	return "mem_" + hex.EncodeToString(b)
}

// ===== 主题文件 =====

func validateSlug(s string) (string, error) {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			sb.WriteRune(r)
		}
	}
	s = sb.String()
	if s == "" {
		return "", fmt.Errorf("slug 无有效字符")
	}
	if len([]rune(s)) > 80 {
		return "", fmt.Errorf("slug 过长")
	}
	return s, nil
}

// themePath 将 (scope, project, 主题 slug) 映射为相对 memory/ 的路径。
func themePathFor(scope, project, slug string) (string, error) {
	slug = strings.ToLower(strings.Trim(slug, " \t"))
	slug = strings.ReplaceAll(slug, " ", "-")
	if slug == "" {
		return "", fmt.Errorf("主题 slug 为空")
	}
	s, err := validateSlug(slug)
	if err != nil {
		return "", err
	}
	var p string
	switch scope {
	case ScopeUser:
		p = "user/"
	case ScopeProject:
		if project == "" {
			return "", fmt.Errorf("project 作用域必须提供项目 slug")
		}
		sp, err := sanitizeRelPath(project)
		if err != nil {
			return "", err
		}
		p = "project/" + sp + "/"
	default:
		return "", fmt.Errorf("未知作用域: %q", scope)
	}
	return p + s + ".md", nil
}

// writeThemeFile 写主题文件（YAML frontmatter + 正文），0600 权限。
func writeThemeFile(baseDir, relPath string, meta MemoryMeta, content string) error {
	full := filepath.Join(baseDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return fmt.Errorf("建主题目录失败: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("id: %s\n", meta.ID))
	sb.WriteString(fmt.Sprintf("title: %s\n", meta.Title))
	sb.WriteString(fmt.Sprintf("type: %s\n", meta.Type))
	sb.WriteString(fmt.Sprintf("scope: %s\n", meta.Scope))
	if meta.Project != "" {
		sb.WriteString(fmt.Sprintf("project: %s\n", meta.Project))
	}
	if len(meta.Tags) > 0 {
		sb.WriteString("tags: [" + strings.Join(meta.Tags, ", ") + "]\n")
	}
	if meta.Source != "" {
		sb.WriteString(fmt.Sprintf("source: %s\n", meta.Source))
	}
	sb.WriteString(fmt.Sprintf("created: %d\n", meta.CreatedAt))
	sb.WriteString(fmt.Sprintf("updated: %d\n", meta.UpdatedAt))
	if meta.ExpiresAt != 0 {
		sb.WriteString(fmt.Sprintf("expires: %d\n", meta.ExpiresAt))
	}
	sb.WriteString("---\n\n")
	sb.WriteString(content)
	return os.WriteFile(full, []byte(sb.String()), 0o600)
}

// readThemeFile 读主题文件正文（去掉 YAML frontmatter）。
func readThemeFile(baseDir, relPath string) (string, error) {
	full := filepath.Join(baseDir, relPath)
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	s := string(b)
	// 找 "---\n ... \n---\n" 头，去掉
	if strings.HasPrefix(s, "---") {
		idx := strings.Index(s[1:], "---")
		if idx >= 0 {
			s = s[idx+4:] // 去掉前两个 ---
			s = strings.TrimSpace(s)
		}
	}
	return s, nil
}

// ===== PII =====

// piiPattern 匹配常见 PII（邮箱 / 手机号 / 银行卡 / 口令类键值 / 私钥头）。
//
// 注意：不能用带缩进的裸字符串（反引号）跨行书写——换行与制表符会被当作
// 正文字面量，使模式要求输入里含这些空白，从而永远匹配不到。
func piiPattern() *regexp.Regexp {
	return regexp.MustCompile("(" + strings.Join([]string{
		`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`,
		`1[3-9]\d{9}`,
		`\d{16,19}`,
		`(password|passwd|secret|api[_-]?key|token|credential)[:=]\s*\S+`,
		`-----BEGIN (RSA|DSA|EC|OPENSSH) PRIVATE KEY-----`,
	}, "|") + ")")
}

func (m *MemoryStore) containsPII(content, title, desc string) bool {
	if !m.cfg.PIIFilter {
		return false
	}
	return m.pii.MatchString(content) || m.pii.MatchString(title+desc)
}

// ===== 文本工具 =====

func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0x3000 && r <= 0x303F:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	}
	return false
}

// tokenize 中英文混合分词：CJK 按单字、拉丁按非字母数字切。
func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	var toks []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			toks = append(toks, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		switch {
		case isCJK(r):
			flush()
			toks = append(toks, string(r))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			word.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return toks
}

// Jaccard 相似度。
func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	m := map[string]bool{}
	for _, t := range a {
		m[t] = true
	}
	inter := 0
	for _, t := range b {
		if m[t] {
			inter++
		}
	}
	uniq := len(a) + len(b) - inter
	if uniq == 0 {
		return 0
	}
	return float64(inter) / float64(uniq)
}

func toLowerSet(items []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range items {
		m[strings.ToLower(s)] = true
	}
	return m
}

func mergeTags(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			set[s] = true
		}
	}
	for _, s := range b {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func importanceRank(e *MemoryMeta) int {
	switch e.Importance {
	case ImportanceHigh:
		return 2
	case ImportanceMedium:
		return 1
	default:
		return 0
	}
}

// ===== 去重 =====

// findSimilar 找与 candidate 相似的现有条目（阈值 0.6），返回下标或 -1。
func findSimilar(existing []MemoryMeta, c MemoryMeta) int {
	for i, e := range existing {
		tagJ := jaccard(c.Tags, e.Tags)
		cText := append(append([]string{}, tokenize(c.Title)...), tokenize(c.Description)...)
		eText := append(append([]string{}, tokenize(e.Title)...), tokenize(e.Description)...)
		textJ := jaccard(cText, eText)
		typeM := 0.0
		if c.Type == e.Type {
			typeM = 1.0
		}
		sim := 0.4*tagJ + 0.4*textJ + 0.2*typeM
		if sim >= 0.6 {
			return i
		}
	}
	return -1
}

// ===== 检索 =====

// Retrieve 按 query + 项目 slug 检索记忆，返回按相关性降序的 topK 条。
func (m *MemoryStore) Retrieve(query, projectSlug string, topK int) ([]*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	qToks := tokenize(query)
	if len(qToks) == 0 {
		return nil, nil
	}
	now := time.Now()
	type scored struct {
		*MemoryEntry
		Score float64
	}
	var out []scored
	for i := range m.idx.Memories {
		me := &m.idx.Memories[i]
		if me.ExpiresAt != 0 && me.ExpiresAt < now.UnixMilli() {
			continue
		}
		keep := false
		switch me.Scope {
		case ScopeUser:
			keep = true
		case ScopeProject:
			keep = (projectSlug == "" || me.Project == projectSlug)
		}
		if !keep {
			continue
		}
		score := 0.0
		tagSet := toLowerSet(me.Tags)
		for _, t := range qToks {
			if tagSet[t] {
				score += 3.0
			}
		}
		titleSet := toLowerSet(tokenize(me.Title))
		for _, t := range qToks {
			if titleSet[t] {
				score += 2.0
			}
		}
		descSet := toLowerSet(tokenize(me.Description))
		for _, t := range qToks {
			if descSet[t] {
				score += 1.0
			}
		}
		if now.Add(-72 * time.Hour).After(time.UnixMilli(me.UpdatedAt)) {
			score += 1.0
		}
		if me.Importance == ImportanceHigh {
			score += 0.5
		}
		if score > 0 {
			out = append(out, scored{MemoryEntry: m.buildEntry(*me, now), Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Meta.UpdatedAt > out[j].Meta.UpdatedAt
	})
	if topK < 0 {
		topK = 0
	}
	if topK > len(out) {
		topK = len(out)
	}
	entries := make([]*MemoryEntry, topK)
	for i := 0; i < topK; i++ {
		entries[i] = out[i].MemoryEntry
	}
	return entries, nil
}

// buildEntry 从 meta 构建 MemoryEntry（读主题文件内容、算相对龄/漂移）。
func (m *MemoryStore) buildEntry(meta MemoryMeta, now time.Time) *MemoryEntry {
	e := &MemoryEntry{
		Meta:    meta,
		Content: meta.Description,
		Age:     relAge(meta.UpdatedAt, now.UnixMilli()),
	}
	if b, err := readThemeFile(m.dir, meta.Path); err == nil {
		e.Content = b
	}
	if meta.Scope == ScopeProject {
		days := (now.UnixMilli() - meta.UpdatedAt) / (24 * 60 * 60 * 1000)
		e.Stale = days > 1
	}
	return e
}

// relAge 返回相对龄字符串。
func relAge(updatedAtMs, nowMs int64) string {
	days := (nowMs - updatedAtMs) / (24 * 60 * 60 * 1000)
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

// ===== 注入 =====

// FormatInjection 将记忆列表格式化为 prompt 注入块（≤ InjectionMaxChars）。
func (m *MemoryStore) FormatInjection(entries []*MemoryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	sorted := make([]*MemoryEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if importanceRank(&sorted[i].Meta) != importanceRank(&sorted[j].Meta) {
			return importanceRank(&sorted[i].Meta) > importanceRank(&sorted[j].Meta)
		}
		return sorted[i].Meta.UpdatedAt > sorted[j].Meta.UpdatedAt
	})
	var sb strings.Builder
	sb.WriteString("## 长期记忆（跨会话）\n")
	sb.WriteString("以下为过去积累的偏好/项目知识，与当前任务相关时请优先参考：\n")
	stale := false
	count := 0
	for _, e := range sorted {
		line := fmt.Sprintf("  %d. [%s] %s — %s（%s）\n",
			count+1, e.Meta.Scope, e.Meta.Title, e.Meta.Description, e.Age)
		if sb.Len()+len(line) > m.cfg.InjectionMaxChars {
			break
		}
		sb.WriteString(line)
		if e.Stale {
			stale = true
		}
		count++
	}
	if count < len(sorted) {
		sb.WriteString(fmt.Sprintf("（共 %d 条，已显示 top %d）\n", len(sorted), count))
	}
	if m.cfg.StalenessCaveat && (stale || count > 0) {
		sb.WriteString("\n> 注意：记忆是\"写下时的观察\"。超过 1 天的项目级记忆可能已过\n")
		sb.WriteString("> 期（代码会漂移）。引用具体文件/函数/行为前，先 grep/read 核实。")
	}
	return strings.TrimSpace(sb.String())
}

// ===== 写入 / 更新 / 删除 =====

func normalizeMeta(meta MemoryMeta, content string) MemoryMeta {
	meta.Title = trimNonEmpty(meta.Title)
	if meta.Title == "" {
		meta.Title = shortFromContent(content)
	}
	meta.Description = trimNonEmpty(meta.Description)
	if meta.Description == "" {
		meta.Description = firstLine(content)
	}
	if meta.Type == "" {
		meta.Type = TypeProject
	}
	if meta.Importance == "" {
		meta.Importance = ImportanceMedium
	}
	if meta.Scope == "" {
		meta.Scope = ScopeProject
	}
	if meta.Project == "" && meta.Scope == ScopeProject {
		meta.Project = "local-agent"
	}
	return meta
}

func trimNonEmpty(s string) string { return strings.TrimSpace(s) }

func shortFromContent(c string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return ""
	}
	r := []rune(c)
	if len(r) <= 30 {
		return string(r)
	}
	return string(r[:30]) + "…"
}

func firstLine(c string) string {
	for _, line := range strings.Split(c, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return c
}

// AddMemory 添加或合并一条记忆。返回最终落库的 meta。
func (m *MemoryStore) AddMemory(meta MemoryMeta, content string) (MemoryMeta, error) {
	if m.containsPII(content, meta.Title, meta.Description) {
		return meta, fmt.Errorf("记忆含敏感信息（PII），已拦截")
	}
	meta = normalizeMeta(meta, content)
	relPath, err := themePathFor(meta.Scope, meta.Project, meta.Title)
	if err != nil {
		return meta, err
	}
	meta.Path = relPath
	if meta.ID == "" {
		meta.ID = genID()
	}
	now := time.Now().UnixMilli()
	if meta.CreatedAt == 0 {
		meta.CreatedAt = now
	}
	if meta.UpdatedAt == 0 {
		meta.UpdatedAt = now
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	simIdx := findSimilar(m.idx.Memories, meta)
	if simIdx >= 0 {
		ex := &m.idx.Memories[simIdx]
		// 合并：把新内容追加到旧正文（设计 §6.1），不丢历史。
		merged := content
		if prev, rerr := readThemeFile(m.dir, ex.Path); rerr == nil {
			prev = strings.TrimSpace(prev)
			if prev != "" && prev != strings.TrimSpace(content) {
				merged = prev + "\n" + content
			}
		}
		if err := writeThemeFile(m.dir, ex.Path, *ex, merged); err != nil {
			return meta, err
		}
		ex.UpdatedAt = time.Now().UnixMilli()
		if meta.Tags != nil {
			ex.Tags = mergeTags(ex.Tags, meta.Tags)
		}
		if meta.Importance == ImportanceHigh {
			ex.Importance = meta.Importance
		}
		if meta.Source != "" {
			ex.Source = meta.Source
		}
		if err := m.saveIndexLocked(); err != nil {
			return meta, err
		}
		return *ex, nil
	}

	if err := writeThemeFile(m.dir, relPath, meta, content); err != nil {
		return meta, err
	}
	m.idx.Memories = append(m.idx.Memories, meta)
	m.evictOverflow()
	if err := m.saveIndexLocked(); err != nil {
		return meta, err
	}
	return meta, nil
}

// evictOverflow 容量淘汰：某 scope 超 cap 时，按 importance×recency 淘汰最低。
func (m *MemoryStore) evictOverflow() {
	capacity := m.cfg.MaxPerScope
	if capacity <= 0 || len(m.idx.Memories) == 0 {
		return
	}
	sort.Slice(m.idx.Memories, func(i, j int) bool {
		if importanceRank(&m.idx.Memories[i]) != importanceRank(&m.idx.Memories[j]) {
			return importanceRank(&m.idx.Memories[i]) > importanceRank(&m.idx.Memories[j])
		}
		return m.idx.Memories[i].UpdatedAt > m.idx.Memories[j].UpdatedAt
	})
	totalCap := capacity * 3
	if len(m.idx.Memories) > totalCap {
		m.deleteMetaLocked(&m.idx.Memories[totalCap])
		m.idx.Memories = append(m.idx.Memories[:totalCap], m.idx.Memories[totalCap+1:]...)
	}
}

func (m *MemoryStore) deleteMetaLocked(meta *MemoryMeta) {
	if meta.Path != "" {
		full := filepath.Join(m.dir, meta.Path)
		_ = os.Remove(full)
	}
}

func (m *MemoryStore) saveIndexLocked() error {
	return writeJSONAtomic(m.idxPath(), m.idx)
}

// UpdateMemory 更新一条记忆的内容。
func (m *MemoryStore) UpdateMemory(id, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.idx.Memories {
		me := &m.idx.Memories[i]
		if me.ID == id {
			me.UpdatedAt = time.Now().UnixMilli()
			if err := writeThemeFile(m.dir, me.Path, *me, content); err != nil {
				return err
			}
			return m.saveIndexLocked()
		}
	}
	return fmt.Errorf("记忆不存在: %s", id)
}

// DeleteMemory 删除一条记忆。
func (m *MemoryStore) DeleteMemory(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.idx.Memories {
		me := &m.idx.Memories[i]
		if me.ID == id {
			m.deleteMetaLocked(me)
			m.idx.Memories = append(m.idx.Memories[:i], m.idx.Memories[i+1:]...)
			if err := m.saveIndexLocked(); err != nil {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("记忆不存在: %s", id)
}

// ListMemories 列出某作用域/项目的记忆（完整）。
func (m *MemoryStore) ListMemories(scope, project string) ([]*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*MemoryEntry
	now := time.Now()
	for i := range m.idx.Memories {
		me := &m.idx.Memories[i]
		if scope != "" && me.Scope != scope {
			continue
		}
		if project != "" && me.Scope == ScopeProject && me.Project != project {
			continue
		}
		out = append(out, m.buildEntry(*me, now))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Meta.UpdatedAt > out[j].Meta.UpdatedAt
	})
	return out, nil
}

// Stats 返回记忆库统计。
func (m *MemoryStore) Stats() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := map[string]int{"total": 0}
	for _, me := range m.idx.Memories {
		stats["total"]++
		key := me.Scope
		if me.Scope == ScopeProject {
			key = me.Scope + "/" + me.Project
		}
		stats[key]++
	}
	return stats
}
