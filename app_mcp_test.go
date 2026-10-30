package main

import (
	"reflect"
	"testing"

	"wproxyman/internal/mcp"
)

// MCP 工具清单是"AI 能控制整个软件"的能力边界，必须与 App 的真实 API 一致。
//
// 这个测试是**漂移警戒线**：
//   - 有人给 App 加了新方法却没写元数据 → 失败（避免能力悄悄缺失）；
//   - 有人改了方法签名（增删参数）而没改元数据 → 失败（避免调用时参数错位）；
//   - 有人把生命周期方法加进元数据 → 失败（注册表里有硬性拒绝名单）。
func TestMCPToolSpecsCoverAppAPI(t *testing.T) {
	specs := mcp.AppToolSpecs()
	app := NewApp()

	reg, warns, err := mcp.NewRegistry(app, specs)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("元数据与真实签名不一致（改签名后没同步元数据？）：\n  %v", warns)
	}

	// 已知的、有意不暴露的方法。新增例外必须写在这里并说明原因。
	excluded := map[string]string{
		"Cleanup":      "生命周期方法：会让应用退出（注册表也硬性拒绝）",
		"GetCACertPEM": "返回一大段 PEM，对模型无意义；改为以资源形式提供",
	}

	// 1) App 的每个导出方法要么暴露，要么登记在排除名单里。
	exposed := make(map[string]bool, len(specs))
	for _, s := range specs {
		exposed[s.Method] = true
	}
	rt := reflect.TypeOf(app)
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		if exposed[name] {
			continue
		}
		if reason, ok := excluded[name]; ok {
			t.Logf("未暴露：%s（%s）", name, reason)
			continue
		}
		t.Errorf("App 方法 %s 既没暴露给 MCP，也没登记在排除名单里 —— 请在 AppToolSpecs 里补上", name)
	}

	// 2) 排除名单里的方法确实不在工具列表里。
	inTools := map[string]bool{}
	for _, tool := range reg.Tools() {
		inTools[tool.Name] = true
	}
	for method := range excluded {
		for _, s := range specs {
			if s.Method == method && inTools[s.Name] {
				t.Errorf("%s 应被排除，却出现在工具列表里（%s）", method, s.Name)
			}
		}
	}

	// 3) 每个工具都必须有合法档位与描述（描述是给模型看的，不能空着）。
	for _, s := range specs {
		switch s.Tier {
		case mcp.TierRead, mcp.TierWrite, mcp.TierDangerous:
		default:
			t.Errorf("工具 %s 的权限档非法：%q", s.Name, s.Tier)
		}
		if s.Description == "" {
			t.Errorf("工具 %s 缺少描述（模型靠它决定怎么用）", s.Name)
		}
		if s.Group == "" {
			t.Errorf("工具 %s 缺少分组", s.Name)
		}
	}

	// 4) 默认只放行 read：确认写/危险档确实存在（否则"默认最小权限"没有意义），
	//    且只读档占了相当比例。
	counts := map[mcp.Tier]int{}
	for _, s := range specs {
		counts[s.Tier]++
	}
	t.Logf("能力面：共 %d 个工具（read=%d write=%d dangerous=%d）",
		len(specs), counts[mcp.TierRead], counts[mcp.TierWrite], counts[mcp.TierDangerous])
	if counts[mcp.TierWrite] == 0 || counts[mcp.TierDangerous] == 0 {
		t.Errorf("写/危险档不应为空：%v", counts)
	}
}
