//go:build !windows && !bindings

package main

// showFatalError 在非 Windows 平台不重复标准错误输出。
func showFatalError(string, error) {}
