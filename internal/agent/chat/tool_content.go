package chat

import "unicode/utf8"

const toolContentOmission = "\n[中间内容已省略，结果不完整]\n"

func trimToolContent(content string, tokens int) string {
	if tokens < 64 || tokens > int(^uint(0)>>1)/4 {
		tokens = 8192
	}
	limit := tokens * 4
	if len(content) <= limit {
		return content
	}
	// 扣除说明后按字节各留一半，只向内收缩 UTF-8 边界，不补全 JSON。
	half := (limit - len(toolContentOmission)) / 2
	head, tail := half, len(content)-half
	for head > 0 && !utf8.RuneStart(content[head]) {
		head--
	}
	for tail < len(content) && !utf8.RuneStart(content[tail]) {
		tail++
	}
	return content[:head] + toolContentOmission + content[tail:]
}
