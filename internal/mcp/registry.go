package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// 能力注册表：把 App 的导出方法自动变成 MCP 工具。
//
// 为什么不手写工具：App 现在有 44 个导出方法，手写必然漂移（加一个方法就漏一个）。
// 做法是——**元数据手写、签名与 JSON Schema 从反射推导**：
//   - ToolSpec 里写清楚工具名、描述、分组、权限档、参数名（反射拿不到参数名）；
//   - 参数类型、返回类型、Schema 全部由 reflect 推导，改了签名不会漂移；
//   - 启动时校验元数据与真实签名一致（参数个数不符/类型不支持会报出来）。

// Tier 是工具的权限档。默认只放行 TierRead。
type Tier string

const (
	TierRead      Tier = "read"      // 只读：查流量、读配置
	TierWrite     Tier = "write"     // 改配置：规则、端口、代理启停
	TierDangerous Tier = "dangerous" // 破坏性/越权面：清空、装删证书、任意文件读写
)

// deniedMethods 是**永不允许**暴露给 MCP 的方法。
//
// 这些是 Wails 的生命周期钩子与内部实现：暴露出去意味着 AI 能让应用退出、
// 重启或绕过初始化。这里做硬性拒绝（而不是只靠元数据表自觉），
// 否则将来有人往元数据里加一条就悄悄开了个后门。
var deniedMethods = map[string]bool{
	"Cleanup":  true, // 进程清理（关闭事件泵、还原系统代理）
	"startup":  true, // Wails OnStartup
	"shutdown": true, // Wails OnShutdown
	"domReady": true, // Wails OnDomReady
}

// ToolSpec 是一个工具的元数据（手写）。
type ToolSpec struct {
	Name        string   // MCP 工具名（snake_case）
	Method      string   // App 上的方法名
	Description string   // 给模型看的说明：要写清楚"做什么、有什么副作用"
	Group       string   // 分组（flows / proxy / tools / sessions ...），便于 list_capabilities
	Tier        Tier     // 权限档
	Params      []string // 参数名，顺序与 Go 方法参数一致（反射拿不到名字）
}

// Tool 是 tools/list 返回给客户端的工具描述。
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// HandlerFunc 是自定义工具（包装工具）的实现。
// 入参是 MCP 的 arguments（一定是合法的 JSON 对象，无参时是 {}）。
type HandlerFunc func(args json.RawMessage) (any, *Error)

// Param 描述自定义工具的一个参数。比反射更细：可以带描述、枚举、默认值——
// 这些正是模型选择参数时最需要的信息。
type Param struct {
	Name        string
	Type        string // string | integer | number | boolean | array | object
	Description string
	Required    bool
	Enum        []string // 可选：限定取值
	Items       string   // Type=array 时元素类型
	Default     any      // 可选：默认值（会写进 Schema 提示模型）
}

// SchemaFromParams 由参数列表生成 JSON Schema。
func SchemaFromParams(params []Param) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range params {
		prop := map[string]any{}
		if p.Type != "" {
			prop["type"] = p.Type
		}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		if p.Type == "array" {
			itemType := p.Items
			if itemType == "" {
				itemType = "string"
			}
			prop["items"] = map[string]any{"type": itemType}
		}
		if p.Default != nil {
			prop["default"] = p.Default
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// AddHandler 注册一个自定义工具（不走反射，由 fn 实现）。
//
// 用途：反射能覆盖的只是"原样转发到 App 方法"，但有些能力必须重新包装才好用——
// 例如 list_flows 需要过滤/分页/字段裁剪，get_flow_body 需要分段读正文。
// 这些工具的参数往往也需要更细的描述与枚举，所以 Schema 由 Param 显式给出。
func (r *Registry) AddHandler(spec ToolSpec, params []Param, fn HandlerFunc) error {
	if spec.Name == "" || spec.Method == "" {
		return fmt.Errorf("mcp: 自定义工具必须有 name 与 method 标识")
	}
	if fn == nil {
		return fmt.Errorf("mcp: 自定义工具 %s 缺少实现", spec.Name)
	}
	if _, exists := r.tools[spec.Name]; exists {
		return fmt.Errorf("mcp: 工具名 %s 重复", spec.Name)
	}
	for _, p := range params {
		switch p.Type {
		case "", "string", "integer", "number", "boolean", "array", "object":
		default:
			return fmt.Errorf("mcp: 工具 %s 的参数 %s 类型非法：%q", spec.Name, p.Name, p.Type)
		}
		if p.Name == "" {
			return fmt.Errorf("mcp: 工具 %s 存在无名参数", spec.Name)
		}
	}
	spec.Params = nil // 自定义工具的参数由 Schema 描述，不参与反射校验
	r.tools[spec.Name] = &boundTool{
		spec:    spec,
		handler: fn,
		schema:  SchemaFromParams(params),
	}
	r.order = append(r.order, spec.Name)
	sort.Strings(r.order)
	return nil
}

type boundTool struct {
	spec    ToolSpec
	handler HandlerFunc // 非 nil 表示自定义工具（不走反射）
	method  reflect.Value
	// inTypes 不含接收者：inTypes[i] 对应 spec.Params[i]
	inTypes []reflect.Type
	schema  map[string]any
}

// Registry 持有全部可调用工具。
type Registry struct {
	tools map[string]*boundTool
	order []string
}

// NewRegistry 依据目标对象（一般是 *App）与元数据表构建注册表。
//
// 返回的 warnings 列出"元数据写了但目标上不存在/签名不符"的条目——不当作错误，
// 因为目标对象在不同构建里方法可能有增减，但必须能被测试与日志看到。
func NewRegistry(target any, specs []ToolSpec) (*Registry, []string, error) {
	if target == nil {
		return nil, nil, fmt.Errorf("mcp: nil target")
	}
	rv := reflect.ValueOf(target)
	rt := rv.Type()
	r := &Registry{tools: make(map[string]*boundTool, len(specs))}
	var warnings []string

	for _, spec := range specs {
		if spec.Name == "" || spec.Method == "" {
			warnings = append(warnings, fmt.Sprintf("元数据缺少 name/method：%+v", spec))
			continue
		}
		if deniedMethods[spec.Method] {
			warnings = append(warnings, fmt.Sprintf("%s：%s 属于禁止暴露的生命周期方法，已拒绝", spec.Name, spec.Method))
			continue
		}
		m, ok := rt.MethodByName(spec.Method)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s：目标上没有方法 %s", spec.Name, spec.Method))
			continue
		}
		bound := &boundTool{spec: spec, method: rv.Method(m.Index)}
		// m.Type.In(0) 是接收者。
		nIn := m.Type.NumIn() - 1
		if nIn != len(spec.Params) {
			warnings = append(warnings, fmt.Sprintf("%s：%s 需要 %d 个参数，元数据写了 %d 个（%v）",
				spec.Name, spec.Method, nIn, len(spec.Params), spec.Params))
			continue
		}
		unsupported := false
		for i := 0; i < nIn; i++ {
			t := m.Type.In(i + 1)
			if !schemaSupported(t) {
				warnings = append(warnings, fmt.Sprintf("%s：参数 %s 的类型 %v 不支持", spec.Name, spec.Params[i], t))
				unsupported = true
				break
			}
			bound.inTypes = append(bound.inTypes, t)
		}
		if unsupported {
			continue
		}
		bound.schema = schemaForParams(spec, bound.inTypes)
		r.tools[spec.Name] = bound
		r.order = append(r.order, spec.Name)
	}
	sort.Strings(r.order)
	return r, warnings, nil
}

// Tools 返回给 tools/list 的工具列表（按名字排序，顺序稳定便于测试）。
func (r *Registry) Tools() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		b := r.tools[name]
		out = append(out, Tool{
			Name:        b.spec.Name,
			Description: b.spec.Description,
			InputSchema: b.schema,
			Annotations: annotationsFor(b.spec.Tier),
		})
	}
	return out
}

// Specs 返回元数据副本（按工具名排序）。
func (r *Registry) Specs() []ToolSpec {
	out := make([]ToolSpec, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name].spec)
	}
	return out
}

// TierOf 返回工具的权限档；工具不存在时 ok=false。
func (r *Registry) TierOf(name string) (Tier, bool) {
	b, ok := r.tools[name]
	if !ok {
		return "", false
	}
	return b.spec.Tier, true
}

// Groups 汇总分组 → 工具名，供 list_capabilities 使用。
func (r *Registry) Groups() map[string][]string {
	out := map[string][]string{}
	for _, name := range r.order {
		g := r.tools[name].spec.Group
		if g == "" {
			g = "misc"
		}
		out[g] = append(out[g], name)
	}
	return out
}

// Call 按工具名调用目标方法。args 是 JSON 对象（MCP 的 arguments）。
// 权限校验不在这里做：由调用方（服务层）按 Tier 决定是否放行，
// 因为"能不能调用"取决于当前设置，而不是注册表本身的属性。
func (r *Registry) Call(name string, args json.RawMessage) (any, *Error) {
	b, ok := r.tools[name]
	if !ok {
		return nil, Errorf(CodeMethodNotFound, "未知工具 %s（可用 list_capabilities 查看）", name)
	}
	// 空参数按空对象处理，便于无参工具调用。
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	// 自定义工具：自身负责解析参数（通常反序列化进一个结构体）。
	if b.handler != nil {
		return b.handler(args)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, Errorf(CodeInvalidParams, "arguments 必须是 JSON 对象：%v", err)
	}

	in := make([]reflect.Value, 0, len(b.inTypes))
	for i, pname := range b.spec.Params {
		rawVal, present := raw[pname]
		if !present || string(rawVal) == "null" {
			return nil, Errorf(CodeInvalidParams, "缺少参数 %s", pname)
		}
		ptr := reflect.New(b.inTypes[i])
		if err := json.Unmarshal(rawVal, ptr.Interface()); err != nil {
			return nil, Errorf(CodeInvalidParams, "参数 %s 类型不符（期望 %s）：%v", pname, b.inTypes[i], err)
		}
		in = append(in, ptr.Elem())
	}

	out := b.method.Call(in)
	// 约定：最后一个返回值是 error 时，非 nil 即失败。
	if n := len(out); n > 0 {
		if last := out[n-1]; last.Type() == reflect.TypeOf((*error)(nil)).Elem() {
			if !last.IsNil() {
				return nil, Errorf(CodeInternalError, "%s 执行失败：%v", name, last.Interface())
			}
			out = out[:n-1]
		}
	}
	switch len(out) {
	case 0:
		return map[string]any{"ok": true}, nil
	case 1:
		return jsonSafe(out[0].Interface()), nil
	default:
		vals := make([]any, 0, len(out))
		for _, v := range out {
			vals = append(vals, jsonSafe(v.Interface()))
		}
		return vals, nil
	}
}

// jsonSafe 把不可序列化的返回值（如 []byte 会被转成 base64 字符串前的原始切片）
// 交给 encoding/json 处理，这里只做 nil 归一化。
func jsonSafe(v any) any {
	if v == nil {
		return map[string]any{"ok": true}
	}
	return v
}

// annotationsFor 把权限档映射成 MCP 的注解提示，客户端会据此提醒用户。
func annotationsFor(t Tier) map[string]any {
	switch t {
	case TierRead:
		return map[string]any{"readOnlyHint": true}
	case TierDangerous:
		return map[string]any{"readOnlyHint": false, "destructiveHint": true}
	default:
		return map[string]any{"readOnlyHint": false}
	}
}

// schemaForParams 生成 {"type":"object","properties":{...},"required":[...]}。
func schemaForParams(spec ToolSpec, types []reflect.Type) map[string]any {
	props := map[string]any{}
	required := make([]string, 0, len(spec.Params))
	for i, pname := range spec.Params {
		props[pname] = schemaForType(types[i], 0)
		required = append(required, pname)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// schemaForType 把 Go 类型映射成 JSON Schema（深度受限，避免结构体展开爆炸）。
func schemaForType(t reflect.Type, depth int) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaForType(t.Elem(), depth+1)}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Interface:
		return map[string]any{} // 任意值
	case reflect.Struct:
		s := map[string]any{"type": "object", "additionalProperties": true}
		if depth >= 2 {
			return s // 只展开两层，够用且不至于把 Flow 这种大结构全铺开
		}
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // 未导出字段
				continue
			}
			name := jsonFieldName(f)
			if name == "-" {
				continue
			}
			props[name] = schemaForType(f.Type, depth+1)
		}
		if len(props) > 0 {
			s["properties"] = props
		}
		return s
	default:
		return map[string]any{}
	}
}

// jsonFieldName 取结构体字段的 JSON 名（与 encoding/json 一致）。
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return f.Name
	}
	return name
}

// schemaSupported 判断参数类型能否生成 Schema 并反序列化。
func schemaSupported(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return false
	}
	return true
}
