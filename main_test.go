package main

import (
	"os"
	"runtime"
	"testing"
)

// TestMain 把用户配置目录重定向到临时目录。
//
// 应用代码（startProxy / SetSettings / 系统代理记录）都会调用 saveSettings()
// 写入 configDir()/settings.json —— 不隔离的话，跑一次测试就会覆盖开发者真实的
// settings.json（端口、Allow LAN、SSL 规则等）。
func TestMain(m *testing.M) {
	if runtime.GOOS != "windows" {
		// linux: XDG_CONFIG_HOME；darwin: $HOME/Library/Application Support。
		if dir, err := os.MkdirTemp("", "wproxyman-test-config"); err == nil {
			_ = os.Setenv("XDG_CONFIG_HOME", dir)
			_ = os.Setenv("HOME", dir)
			code := m.Run()
			_ = os.RemoveAll(dir)
			os.Exit(code)
		}
	}
	os.Exit(m.Run())
}
