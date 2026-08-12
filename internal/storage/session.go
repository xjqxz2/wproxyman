// Package storage 实现捕获会话的持久化存储，支持原生会话格式和 HAR 导入/导出。
//
// 会话存储格式：
//   - 原生格式：gzip 压缩的 JSON 文件（.wpx 扩展名）
//   - 文件头包含版本号、应用名称和创建时间
//   - 兼容纯 JSON 回退：如果 gzip 解压失败，尝试以纯 JSON 方式读取
//
// 与其他模块的关系：
//   - models 模块定义了 Flow 数据结构（会话的核心条目）
//   - 前端 UI 调用 SaveSession/OpenSession 实现保存/加载功能
package storage

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"wproxyman/internal/models"
)

// sessionFormatVersion 是当前会话文件格式的版本号。
// 改变此值意味着文件格式发生了不兼容的变更。
const sessionFormatVersion = 1

// SessionFile 是磁盘上的会话文件格式。
// 包含版本号、元数据和 Flow 列表。
type SessionFile struct {
	Version   int            `json:"version"`   // 文件格式版本号
	App       string         `json:"app"`       // 生成此文件的应用名称
	CreatedAt int64          `json:"createdAt"` // 创建时间（Unix 毫秒时间戳）
	Flows     []*models.Flow `json:"flows"`     // 捕获的 Flow 列表
}

// SaveSession 将 Flow 列表以 gzip 压缩的 JSON 格式写入指定路径。
// 使用标准 gzip 压缩以减小文件大小（会话文件可能包含大量请求体）。
func SaveSession(path string, flows []*models.Flow) error {
	sf := SessionFile{
		Version:   sessionFormatVersion,
		App:       "wproxyman",
		CreatedAt: time.Now().UnixMilli(),
		Flows:     flows,
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// gzip 压缩层
	gz := gzip.NewWriter(f)
	defer gz.Close()
	enc := json.NewEncoder(gz)
	if err := enc.Encode(&sf); err != nil {
		return err
	}
	return nil
}

// OpenSession 读取一个会话文件，返回 Flow 列表。
// 优先尝试 gzip 压缩格式，失败时回退到纯 JSON 格式。
// 这种方式兼容旧版本或手动编辑的 JSON 文件。
func OpenSession(path string) ([]*models.Flow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sf SessionFile
	// 首先尝试以 gzip 格式解压读取
	gz, gzErr := gzip.NewReader(f)
	if gzErr == nil {
		defer gz.Close()
		if err := json.NewDecoder(gz).Decode(&sf); err == nil {
			if sf.Version != sessionFormatVersion {
				return nil, fmt.Errorf("unsupported session version %d", sf.Version)
			}
			return sf.Flows, nil
		}
	}
	// gzip 解压失败 → 回退到纯 JSON 格式
	_, _ = f.Seek(0, 0) // 重置文件指针到开头
	if err := json.NewDecoder(f).Decode(&sf); err != nil {
		return nil, fmt.Errorf("not a valid session file: %v", err)
	}
	return sf.Flows, nil
}
