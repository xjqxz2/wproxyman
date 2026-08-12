// Package codegen cURL 命令解析器。
// curl_parser.go 将 cURL 命令字符串解析回 Flow 对象，支持从剪贴板或手动输入粘贴。
//
// 支持的 cURL 标志：
//   - -X / --request：设置 HTTP 方法
//   - -H / --header：添加请求头
//   - -d / --data / --data-raw / --data-binary / --data-ascii / --data-urlencode：设置请求体
//   - --json：设置 JSON 请求体并自动添加 Content-Type: application/json
//   - -u / --user：设置 HTTP Basic 认证（自动生成 Authorization 头）
//   - 自动忽略标志：-L, -s, -k, -i, --location, --silent, --insecure, --include
//
// 实现细节：
//   - tokenize() 实现了一个简单的 Shell 分词器，支持单引号和双引号
//   - 未知标志和其参数会被跳过（宁可缺失信息，也不解析错误）
//   - 第一个非标志参数被识别为 URL
//   - URL 被解析以提取 scheme、host、path、query 等字段
//   - 请求体自动决定是否需要从 GET 切换为 POST
package codegen

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"wproxyman/internal/models"
)

// ParseCurl 将 cURL 命令字符串解析为 Flow 对象。
// 支持常用的 cURL 标志子集：-X、-H、-d、-u、--json 等。
// 返回的 Flow 包含完整的请求方法、URL、请求头和请求体。
func ParseCurl(text string) (*models.Flow, error) {
	// 首先将命令文本拆分为 token 列表
	args, err := tokenize(text)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || args[0] != "curl" {
		return nil, errors.New("not a curl command")
	}
	args = args[1:] // 移除 "curl" 本身

	f := models.NewFlow()
	f.Method = "GET" // 默认方法

	// 逐 token 解析
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		// 请求方法：-X <method> 或 --request <method>
		case a == "-X" || a == "--request":
			if i+1 < len(args) {
				f.Method = strings.ToUpper(args[i+1])
				i += 2
			} else {
				i++
			}
		// 请求头：-H <header> 或 --header <header>
		case a == "-H" || a == "--header":
			if i+1 < len(args) {
				addHeader(f, args[i+1])
				i += 2
			} else {
				i++
			}
		// 请求体（各种 data 变体）
		case a == "-d" || a == "--data" || a == "--data-raw" || a == "--data-binary" || a == "--data-ascii" || a == "--data-urlencode":
			if i+1 < len(args) {
				if a == "--data-urlencode" {
					f.RequestBody = append(f.RequestBody, []byte(args[i+1])...)
				} else {
					f.RequestBody = append(f.RequestBody, []byte(args[i+1])...)
				}
				// 有请求体 → 自动改为 POST
				if f.Method == "GET" {
					f.Method = "POST"
				}
				i += 2
			} else {
				i++
			}
		// JSON 请求体（自动设置 Content-Type）
		case a == "--json":
			if i+1 < len(args) {
				f.RequestBody = append(f.RequestBody, []byte(args[i+1])...)
				models.SetHeader(&f.RequestHeaders, "Content-Type", "application/json")
				if f.Method == "GET" {
					f.Method = "POST"
				}
				i += 2
			} else {
				i++
			}
		// HTTP Basic 认证：-u user:password 或 --user user:password
		case a == "-u" || a == "--user":
			if i+1 < len(args) {
				parts := strings.SplitN(args[i+1], ":", 2)
				if len(parts) == 2 {
					auth := parts[0] + ":" + parts[1]
					// 生成 Basic 认证头（自定义 Base64 编码）
					models.SetHeader(&f.RequestHeaders, "Authorization", "Basic "+b64(auth))
				}
				i += 2
			} else {
				i++
			}
		// 可安全忽略的标志：重定向、静默、跳过 SSL 验证、包含响应头
		case a == "-L" || a == "--location" || a == "-s" || a == "--silent" || a == "-k" || a == "--insecure" || a == "-i" || a == "--include":
			i++
		// 未知标志：跳过标志和其值
		case strings.HasPrefix(a, "-"):
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i += 2 // 跳过标志 + 值
			} else {
				i++ // 跳过仅标志
			}
		// 第一个非标志 token → URL
		default:
			if f.FullURL == "" {
				f.FullURL = strings.Trim(a, `"'`) // 移除引号
			}
			i++
		}
	}

	if f.FullURL == "" {
		return nil, errors.New("no URL found in curl command")
	}
	// 解析 URL 提取 scheme/host/path/query
	applyURLToFlow(f)
	f.RequestSize = int64(len(f.RequestBody))
	return f, nil
}

// addHeader 解析 "Name: Value" 格式的头字符串并添加到 Flow。
func addHeader(f *models.Flow, h string) {
	h = strings.Trim(h, `"'`)
	idx := strings.Index(h, ":")
	if idx < 0 {
		return // 无效头格式 → 忽略
	}
	name := strings.TrimSpace(h[:idx])
	value := strings.TrimSpace(h[idx+1:])
	models.SetHeader(&f.RequestHeaders, name, value)
}

// applyURLToFlow 解析 URL 并填充 Flow 的 scheme、TLS、host、path、query 字段。
func applyURLToFlow(f *models.Flow) {
	u, err := url.Parse(f.FullURL)
	if err != nil {
		return
	}
	f.Scheme = u.Scheme
	f.TLS = u.Scheme == "https"
	f.Host = u.Host
	f.Path = u.Path
	f.Query = u.RawQuery
}

// tokenize 实现一个简单的 Shell 命令分词器，正确处理单引号和双引号。
// 支持转义字符（反斜杠），但不处理所有 Shell 特性（如变量展开、命令替换等）。
//
// 分词规则：
//   - 空格/制表符/换行符分隔 token
//   - 单引号中的内容保持原样（不处理转义）
//   - 双引号中的内容保持原样，但反斜杠转义仍有效
//   - 引号外的反斜杠转义下一个字符
//
// 返回值：token 列表，或引号未终止错误。
func tokenize(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble, escaped := false, false, false

	for _, r := range s {
		switch {
		case escaped:
			// 前一个字符是反斜杠 → 原样写入当前字符
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && !inSingle:
			// 反斜杠在单引号外 → 转义模式
			escaped = true
		case r == '\'' && !inDouble:
			// 单引号切换（不在双引号内时有效）
			inSingle = !inSingle
		case r == '"' && !inSingle:
			// 双引号切换（不在单引号内时有效）
			inDouble = !inDouble
		case (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble:
			// 分隔符：结束当前 token
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			// 普通字符 → 写入当前 token
			cur.WriteRune(r)
		}
	}
	// 检查引号是否终止
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	// 处理末尾的转义反斜杠
	if escaped {
		cur.WriteRune('\\')
	}
	// 最后一个 token
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// b64 实现一个简单的 Base64 编码（避免依赖 encoding/base64 的依赖）。
// 用于将 HTTP Basic 认证凭据编码为 Base64 字符串。
func b64(s string) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := []byte(s)
	var out strings.Builder
	// 每 3 字节 → 4 个 Base64 字符
	for i := 0; i < len(b); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], b[i:])
		out.WriteByte(chars[chunk[0]>>2])
		out.WriteByte(chars[(chunk[0]&0x3)<<4|chunk[1]>>4])
		if n > 1 {
			out.WriteByte(chars[(chunk[1]&0xf)<<2|chunk[2]>>6])
		} else {
			out.WriteByte('=') // 填充
		}
		if n > 2 {
			out.WriteByte(chars[chunk[2]&0x3f])
		} else {
			out.WriteByte('=') // 填充
		}
	}
	return out.String()
}
