package chat

// EstimateTokens 统一以 UTF-8 字节数除以四并向上取整估算 token，不作为实际 usage。
func EstimateTokens(encoded []byte) int {
	return (len(encoded) + 3) / 4
}
