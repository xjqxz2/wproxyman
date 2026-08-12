package main

// 文件说明： 代码生成：为选中的 Flow 生成 cURL / Node-fetch / Postman 代码片段。

import (
	"os"

	"wproxyman/internal/codegen"
	"wproxyman/internal/models"
)

// GenerateCurl returns a cURL command for the flow.
func (a *App) GenerateCurl(flowID string) string {
	f := a.GetFlow(flowID)
	if f == nil {
		return ""
	}
	return codegen.BuildCurl(f)
}

// GenerateNodeFetch returns a Node.js fetch snippet for the flow.
func (a *App) GenerateNodeFetch(flowID string) string {
	f := a.GetFlow(flowID)
	if f == nil {
		return ""
	}
	return codegen.BuildNodeFetch(f)
}

// GeneratePostmanCollection exports selected flows as a Postman collection.
func (a *App) GeneratePostmanCollection(ids []string) string {
	a.mu.RLock()
	flows := make([]*models.Flow, 0)
	if len(ids) == 0 {
		flows = append(flows, a.flows...)
	} else {
		for _, id := range ids {
			if f, ok := a.flowIdx[id]; ok {
				flows = append(flows, f)
			}
		}
	}
	a.mu.RUnlock()
	return codegen.BuildPostman(flows)
}

// GetRequestBody returns the raw request body bytes (base64 in JSON).
func (a *App) GetRequestBody(flowID string) []byte {
	f := a.GetFlow(flowID)
	if f == nil {
		return nil
	}
	return f.RequestBody
}

// GetResponseBody returns the raw response body bytes (base64 in JSON).
func (a *App) GetResponseBody(flowID string) []byte {
	f := a.GetFlow(flowID)
	if f == nil {
		return nil
	}
	return f.ResponseBody
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
