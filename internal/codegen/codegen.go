// Package codegen 将捕获的 HTTP 流（Flow）转换为代码片段，
// 支持 cURL、Node.js fetch 和 Postman Collection 格式导出。
//
// 功能：
//   - BuildCurl：生成 cURL 命令行（含请求方法、头、请求体）
//   - BuildNodeFetch：生成 Node.js fetch() 代码片段
//   - BuildPostman：生成 Postman Collection v2.1 JSON 文件
//   - ParseCurl：将 cURL 命令解析回 Flow 对象（见 curl_parser.go）
//
// 设计要点：
//   - 所有构建函数都是纯函数（无状态，无副作用）
//   - cURL 命令生成使用单引号转义以确保 Shell 兼容性
//   - Postman Collection 使用最小化的 v2.1 格式
//   - 请求体过大时（>64KB），cURL 使用 --data @- 从标准输入读取
//
// 与其他模块的关系：
//   - models 模块定义了 Flow 数据结构（代码片段生成的输入）
//   - 前端 UI 调用这些函数生成可复制的代码片段
package codegen

import (
	"fmt"
	"strings"

	"wproxyman/internal/models"
)

// BuildCurl 为 Flow 生成 cURL 命令行字符串。
// 包含：请求方法（非 GET 时显式指定）、请求头、请求体、URL。
// 对于超过 64KB 的请求体，使用 --data @- 从标准输入读取，避免命令行过长。
func BuildCurl(f *models.Flow) string {
	var b strings.Builder
	b.WriteString("curl")
	// 非 GET 请求 → 显式指定方法
	if f.Method != "" && f.Method != "GET" {
		fmt.Fprintf(&b, " --request %s", f.Method)
	}
	// 请求头（跳过 Host 头——curl 会自动设置）
	for _, h := range f.RequestHeaders {
		if strings.EqualFold(h.Name, "Host") {
			continue
		}
		fmt.Fprintf(&b, " --header %s", shellQuote(h.Name+": "+h.Value))
	}
	// 请求体
	if len(f.RequestBody) > 0 {
		if len(f.RequestBody) < 64*1024 {
			// 小请求体：直接内联
			fmt.Fprintf(&b, " --data %s", shellQuote(string(f.RequestBody)))
		} else {
			// 大请求体：从标准输入读取（避免命令行过长）
			b.WriteString(" --data @-")
		}
	}
	// URL（放在最后是 curl 惯例）
	fmt.Fprintf(&b, " %s", shellQuote(f.FullURL))
	return b.String()
}

// BuildNodeFetch 为 Flow 生成 Node.js fetch() 代码片段。
// 使用现代 fetch API，含 .then(r => r.text()).then(console.log) 链式调用。
func BuildNodeFetch(f *models.Flow) string {
	var b strings.Builder
	b.WriteString("fetch(" + jsQuote(f.FullURL))
	// 仅当有非默认配置时添加选项对象
	if f.Method != "GET" || len(f.RequestHeaders) > 0 || len(f.RequestBody) > 0 {
		b.WriteString(", {\n")
		if f.Method != "GET" {
			fmt.Fprintf(&b, "  method: %s,\n", jsQuote(f.Method))
		}
		if len(f.RequestHeaders) > 0 {
			b.WriteString("  headers: {\n")
			for _, h := range f.RequestHeaders {
				fmt.Fprintf(&b, "    %s: %s,\n", jsQuote(h.Name), jsQuote(h.Value))
			}
			b.WriteString("  },\n")
		}
		if len(f.RequestBody) > 0 {
			fmt.Fprintf(&b, "  body: %s,\n", jsQuote(string(f.RequestBody)))
		}
		b.WriteString("})")
	} else {
		b.WriteString(")")
	}
	// 标准响应处理链
	b.WriteString("\n  .then(r => r.text())\n  .then(console.log);")
	return b.String()
}

// BuildPostman 将 Flow 列表导出为最小化的 Postman Collection v2.1 JSON 字符串。
// 使用 wproxyman-export 作为集合名称。
func BuildPostman(flows []*models.Flow) string {
	items := make([]string, 0, len(flows))
	for _, f := range flows {
		items = append(items, postmanItem(f))
	}
	// 构建最小化但有效的 Postman Collection JSON
	return fmt.Sprintf(`{
  "info": {
    "name": "wproxyman-export",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "item": [%s]
}`, strings.Join(items, ",\n"))
}

// postmanItem 将单个 Flow 转换为 Postman Collection 中的 item JSON 片段。
func postmanItem(f *models.Flow) string {
	// 请求头
	headers := make([]string, 0, len(f.RequestHeaders))
	for _, h := range f.RequestHeaders {
		headers = append(headers, fmt.Sprintf(`{"key": %s, "value": %s}`, jsQuote(h.Name), jsQuote(h.Value)))
	}
	// 请求体（仅 raw 模式）
	body := ""
	if len(f.RequestBody) > 0 {
		body = fmt.Sprintf(`,"body": {"mode": "raw", "raw": %s}`, jsQuote(string(f.RequestBody)))
	}
	return fmt.Sprintf(`{
    "name": %s,
    "request": {
      "method": %s,
      "url": %s,
      "header": [%s]
      %s
    }
  }`, jsQuote(f.Method+" "+f.Path), jsQuote(f.Method), jsQuote(f.FullURL), strings.Join(headers, ","), body)
}

// shellQuote 对 Shell 参数进行单引号转义。
// 使用 Shell 标准方式：替换单引号为 '\''（结束引号 → 转义的单引号 → 恢复引号）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// jsQuote 对 JavaScript 字符串进行双引号转义。
// 仅转义双引号（\")，假设其他字符都是安全的。
func jsQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
