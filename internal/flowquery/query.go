// Package flowquery 提供流量的过滤、分页与字段裁剪。
//
// 为什么单独成包：MCP 让 AI 查流量时**绝不能**一次返回全部（含正文），否则
// 一次调用就能把模型的上下文撑爆；前端大列表同样只需要摘要。把这段逻辑抽出来
// 两边共用，避免"给人看的查询"和"给 AI 看的查询"各写一套。
//
// 约定：Query 返回的是**投影后的副本**，不会改动传入的 Flow（store 里那份是
// 共享指针，直接改会串数据）。
package flowquery

import (
	"encoding/base64"
	"sort"
	"strings"
	"unicode/utf8"

	"wproxyman/internal/models"
)

// Filter 描述筛选条件；零值表示不筛。
type Filter struct {
	ID         string // 精确匹配流量 id（get_flow 用）
	Host       string // 精确匹配主机名（不区分大小写）
	Method     string // 精确匹配方法（不区分大小写）
	Status     string // "200" 精确；"2xx"/"4xx"/"5xx" 按类；"failed" 只看失败
	Search     string // 关键字：在 URL / 主机 / 路径 / 方法 / 状态码里做子串匹配
	OnlyFailed bool   // 仅失败（有 error 或状态码 >= 400）
	OnlyStream bool   // 仅流式响应（SSE / NDJSON）
	OnlyWS     bool   // 仅 WebSocket
	SinceMs    int64  // startedAt >= SinceMs（0 表示不限）
	UntilMs    int64  // startedAt <= UntilMs（0 表示不限）
}

// Page 描述分页。
type Page struct {
	Limit  int // <=0 时取 DefaultLimit
	Offset int // 跳过前 N 条
}

// DefaultLimit / MaxLimit 限制单次返回条数：给 AI 用的查询必须有上限。
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Result 是一次查询的结果。
type Result struct {
	Total    int            `json:"total"`    // 查询前的总条数
	Matched  int            `json:"matched"`  // 命中筛选的条数（分页前）
	Returned int            `json:"returned"` // 本次返回的条数
	Offset   int            `json:"offset"`   //
	Limit    int            `json:"limit"`    //
	HasMore  bool           `json:"hasMore"`  // 还有更多（提示模型继续翻页）
	Flows    []*FlowSummary `json:"flows"`    //
}

// FlowSummary 是流量的摘要视图。可选字段用 omitempty，只有显式请求才带上，
// 这样"给模型看"的默认载荷很小。
type FlowSummary struct {
	// 默认字段（总是返回）
	ID           string `json:"id"`
	Method       string `json:"method"`
	Host         string `json:"host"`
	Path         string `json:"path"`
	Status       int    `json:"status"`
	ResponseSize int64  `json:"responseSize"`
	Duration     int64  `json:"duration"`
	StartedAt    int64  `json:"startedAt"`
	Stream       bool   `json:"isStream,omitempty"`
	WebSocket    bool   `json:"isWebSocket,omitempty"`
	Error        string `json:"error,omitempty"`

	// 可选字段（fields 参数显式请求时才填）
	FullURL          string          `json:"fullUrl,omitempty"`
	Scheme           string          `json:"scheme,omitempty"`
	Query            string          `json:"query,omitempty"`
	HTTPVersion      string          `json:"httpVersion,omitempty"`
	TLS              bool            `json:"tls,omitempty"`
	ClientAddr       string          `json:"clientAddr,omitempty"`
	RequestSize      int64           `json:"requestSize,omitempty"`
	RequestMimeType  string          `json:"requestMimeType,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	RequestHeaders   []models.Header `json:"requestHeaders,omitempty"`
	ResponseHeaders  []models.Header `json:"responseHeaders,omitempty"`
	RequestTruncated bool            `json:"requestTruncated,omitempty"`
	RespTruncated    bool            `json:"responseTruncated,omitempty"`
	StreamKind       string          `json:"streamKind,omitempty"`
	FirstByteAt      int64           `json:"firstByteAt,omitempty"`
	EventCount       int             `json:"eventCount,omitempty"`
	Pinned           bool            `json:"isPinned,omitempty"`
	Saved            bool            `json:"isSaved,omitempty"`
}

// optionalFields 是 fields 允许请求的字段名（白名单，避免拼错静默无效）。
var optionalFields = map[string]bool{
	"fullUrl": true, "scheme": true, "query": true, "httpVersion": true, "tls": true,
	"clientAddr": true, "requestSize": true, "requestMimeType": true, "responseMimeType": true,
	"requestHeaders": true, "responseHeaders": true, "truncated": true,
	"streamKind": true, "firstByteAt": true, "eventCount": true, "pinned": true, "saved": true,
}

// KnownField 判断可选字段名是否合法（给上层做参数校验）。
func KnownField(name string) bool { return optionalFields[name] }

// Query 依次做：筛选 → 分页 → 投影。
func Query(flows []*models.Flow, f Filter, p Page, fields []string) Result {
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}

	matched := make([]*models.Flow, 0, len(flows))
	for _, fl := range flows {
		if fl == nil || !matches(fl, f) {
			continue
		}
		matched = append(matched, fl)
	}

	start := offset
	if start > len(matched) {
		start = len(matched)
	}
	end := start + limit
	if end > len(matched) {
		end = len(matched)
	}
	page := matched[start:end]

	out := make([]*FlowSummary, 0, len(page))
	for _, fl := range page {
		out = append(out, summarize(fl, fields))
	}
	return Result{
		Total:    len(flows),
		Matched:  len(matched),
		Returned: len(out),
		Offset:   offset,
		Limit:    limit,
		HasMore:  end < len(matched),
		Flows:    out,
	}
}

// matches 判断单条流量是否命中筛选条件。
func matches(fl *models.Flow, f Filter) bool {
	if f.ID != "" && fl.ID != f.ID {
		return false
	}
	if f.Host != "" && !strings.EqualFold(fl.Host, f.Host) {
		return false
	}
	if f.Method != "" && !strings.EqualFold(fl.Method, f.Method) {
		return false
	}
	if f.OnlyFailed && !isFailed(fl) {
		return false
	}
	if f.OnlyStream && !fl.IsStream {
		return false
	}
	if f.OnlyWS && !fl.IsWebSocket {
		return false
	}
	if f.SinceMs > 0 && fl.StartedAt < f.SinceMs {
		return false
	}
	if f.UntilMs > 0 && fl.StartedAt > f.UntilMs {
		return false
	}
	if f.Status != "" && !matchStatus(fl, f.Status) {
		return false
	}
	if f.Search != "" && !matchSearch(fl, f.Search) {
		return false
	}
	return true
}

func isFailed(fl *models.Flow) bool { return fl.Error != "" || fl.ResponseStatus >= 400 }

// matchStatus 支持 "200"（精确）、"2xx"/"4xx"/"5xx"（按类）、"failed"（失败）。
func matchStatus(fl *models.Flow, want string) bool {
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "failed" {
		return isFailed(fl)
	}
	if len(w) == 3 && strings.HasSuffix(w, "xx") {
		return fl.ResponseStatus/100 == int(w[0]-'0')
	}
	return strings.EqualFold(strings.TrimSpace(want), itoa(fl.ResponseStatus))
}

// matchSearch 在 URL / 主机 / 路径 / 方法 / 状态码里做不区分大小写的子串匹配。
func matchSearch(fl *models.Flow, q string) bool {
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(fl.FullURL), q) ||
		strings.Contains(strings.ToLower(fl.Host), q) ||
		strings.Contains(strings.ToLower(fl.Path), q) ||
		strings.Contains(strings.ToLower(fl.Method), q) {
		return true
	}
	return q == itoa(fl.ResponseStatus)
}

// summarize 生成投影副本；未请求的字段保持零值（omitempty 后不出现在 JSON 里）。
func summarize(fl *models.Flow, fields []string) *FlowSummary {
	s := &FlowSummary{
		ID:           fl.ID,
		Method:       fl.Method,
		Host:         fl.Host,
		Path:         fl.Path,
		Status:       fl.ResponseStatus,
		ResponseSize: fl.ResponseSize,
		Duration:     fl.Duration,
		StartedAt:    fl.StartedAt,
		Stream:       fl.IsStream,
		WebSocket:    fl.IsWebSocket,
		Error:        fl.Error,
	}
	// 只复制请求到的字段：正文（RequestBody/ResponseBody）永远不进摘要。
	for _, name := range fields {
		switch name {
		case "fullUrl":
			s.FullURL = fl.FullURL
		case "scheme":
			s.Scheme = fl.Scheme
		case "query":
			s.Query = fl.Query
		case "httpVersion":
			s.HTTPVersion = fl.HTTPVersion
		case "tls":
			s.TLS = fl.TLS
		case "clientAddr":
			s.ClientAddr = fl.ClientAddr
		case "requestSize":
			s.RequestSize = fl.RequestSize
		case "requestMimeType":
			s.RequestMimeType = fl.RequestMimeType
		case "responseMimeType":
			s.ResponseMimeType = fl.ResponseMimeType
		case "requestHeaders":
			s.RequestHeaders = fl.RequestHeaders
		case "responseHeaders":
			s.ResponseHeaders = fl.ResponseHeaders
		case "truncated":
			s.RequestTruncated = fl.RequestTruncated
			s.RespTruncated = fl.ResponseTruncated
		case "streamKind":
			s.StreamKind = fl.StreamKind
		case "firstByteAt":
			s.FirstByteAt = fl.FirstByteAt
		case "eventCount":
			s.EventCount = len(fl.SSEMsgs)
		case "pinned":
			s.Pinned = fl.IsPinned
		case "saved":
			s.Saved = fl.IsSaved
		}
	}
	return s
}

// itoa 是 strconv.Itoa 的小包装，避免为一个整数格式化引入额外导入。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// 正文分段读取：给 AI 用的时候，正文必须能"按需取一小段"，
// 否则一次读取就可能把上下文撑爆（响应体上限默认 64 MB）。

// DefaultBodyLimit / MaxBodyLimit 限制单次读取的字节数。
const (
	DefaultBodyLimit = 32 << 10  // 32 KiB
	MaxBodyLimit     = 256 << 10 // 256 KiB
)

// BodyChunk 是一次正文分片的结果。
type BodyChunk struct {
	Offset  int  `json:"offset"`
	Total   int  `json:"total"`   // 正文总字节数
	Length  int  `json:"length"`  // 本次返回的字节数
	HasMore bool `json:"hasMore"` // 还有后续，可带更大的 offset 继续读
	// 可打印内容给 text，二进制给 base64（二者只会有一个非空）。
	Text   string `json:"text,omitempty"`
	Base64 string `json:"base64,omitempty"`
	// Truncated 表示这段正文在保存时已被上限截断（读到结尾也不代表拿到全部）。
	Truncated bool `json:"truncated,omitempty"`
}

// SliceBody 按 offset/limit 取正文分片；limit 缺省/越界会被夹到合理范围。
func SliceBody(data []byte, offset, limit int) BodyChunk {
	if limit <= 0 {
		limit = DefaultBodyLimit
	}
	if limit > MaxBodyLimit {
		limit = MaxBodyLimit
	}
	if offset < 0 {
		offset = 0
	}
	total := len(data)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	chunk := data[offset:end]
	out := BodyChunk{
		Offset:  offset,
		Total:   total,
		Length:  len(chunk),
		HasMore: end < total,
	}
	if isPrintableText(chunk) {
		out.Text = string(chunk)
	} else {
		out.Base64 = base64.StdEncoding.EncodeToString(chunk)
	}
	return out
}

// isPrintableText 判断这段字节能否安全地按文本交给模型。
// 空串按文本处理（调用方会看到空内容，而不是一段空 base64）。
func isPrintableText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	if !utf8.Valid(b) {
		return false
	}
	// 允许常见空白；其它控制字符（除 NUL 外）也基本无害，但 NUL 说明这是二进制。
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}

// Stats 是流量统计结果（给 AI 一个"整体在发生什么"的概览，而不是逐条数据）。
type Stats struct {
	Total       int            `json:"total"`
	Failed      int            `json:"failed"`
	Stream      int            `json:"stream"`
	WebSocket   int            `json:"webSocket"`
	TotalBytes  int64          `json:"totalResponseBytes"`
	StatusClass map[string]int `json:"statusClass"` // "2xx"/"4xx"/"5xx"/"other"
	TopHosts    []HostStat     `json:"topHosts"`    // 按请求数排序
	Slowest     []FlowSummary  `json:"slowest"`     // 按耗时排序（摘要形式）
}

// HostStat 是单个域名的汇总。
type HostStat struct {
	Host   string `json:"host"`
	Count  int    `json:"count"`
	Failed int    `json:"failed"`
	Bytes  int64  `json:"bytes"`
	AvgMs  int64  `json:"avgDurationMs"`
}

// StatsOf 汇总一批流量；topN<=0 时用默认 10。
func StatsOf(flows []*models.Flow, topN int) Stats {
	if topN <= 0 {
		topN = 10
	}
	st := Stats{StatusClass: map[string]int{}}
	hosts := map[string]*HostStat{}
	var durSum = map[string]int64{}
	for _, f := range flows {
		if f == nil {
			continue
		}
		st.Total++
		if isFailed(f) {
			st.Failed++
		}
		if f.IsStream {
			st.Stream++
		}
		if f.IsWebSocket {
			st.WebSocket++
		}
		st.TotalBytes += f.ResponseSize
		switch {
		case f.ResponseStatus >= 500:
			st.StatusClass["5xx"]++
		case f.ResponseStatus >= 400:
			st.StatusClass["4xx"]++
		case f.ResponseStatus >= 300:
			st.StatusClass["3xx"]++
		case f.ResponseStatus >= 200:
			st.StatusClass["2xx"]++
		default:
			st.StatusClass["other"]++
		}
		h, ok := hosts[f.Host]
		if !ok {
			h = &HostStat{Host: f.Host}
			hosts[f.Host] = h
		}
		h.Count++
		if isFailed(f) {
			h.Failed++
		}
		h.Bytes += f.ResponseSize
		durSum[f.Host] += f.Duration
	}
	for host, h := range hosts {
		if h.Count > 0 {
			h.AvgMs = durSum[host] / int64(h.Count)
		}
		st.TopHosts = append(st.TopHosts, *h)
	}
	// 请求数降序；数量相同按域名升序，保证结果稳定（便于测试与缓存）。
	sort.Slice(st.TopHosts, func(i, j int) bool {
		if st.TopHosts[i].Count != st.TopHosts[j].Count {
			return st.TopHosts[i].Count > st.TopHosts[j].Count
		}
		return st.TopHosts[i].Host < st.TopHosts[j].Host
	})
	if len(st.TopHosts) > topN {
		st.TopHosts = st.TopHosts[:topN]
	}

	// 最慢的几条：只给摘要，不带正文。
	sorted := make([]*models.Flow, 0, len(flows))
	for _, f := range flows {
		if f != nil {
			sorted = append(sorted, f)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Duration > sorted[j].Duration })
	for i, f := range sorted {
		if i >= topN {
			break
		}
		st.Slowest = append(st.Slowest, *summarize(f, nil))
	}
	return st
}
