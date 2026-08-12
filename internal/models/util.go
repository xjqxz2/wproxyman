// 工具函数：生成 Flow 的唯一标识符。
package models

import (
	"crypto/rand"
	"encoding/hex"
)

// GenID 返回一个 16 字节的随机十六进制标识符（128 位熵，用于 Flow ID）。
func GenID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下 rand 失败：回退到固定值（几乎不可能发生）。
		return "fallback"
	}
	return hex.EncodeToString(b)
}
