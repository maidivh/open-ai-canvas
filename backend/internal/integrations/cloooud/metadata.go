package cloooud

import (
	_ "embed"
	"strings"
)

//go:embed openapi.yaml
var openAPIPaths string

// ExtendOpenAPI 保留宿主规范，仅在顶层 paths 下加入本模块的路径。
func ExtendOpenAPI(spec []byte) []byte {
	return []byte(strings.Replace(string(spec), "\npaths:\n", "\npaths:\n"+openAPIPaths, 1))
}

// RedactRequestPath 供最外层访问日志使用，授权码和 state 不进入日志。
func RedactRequestPath(path string) string {
	base, _, _ := strings.Cut(path, "?")
	if base == "/api/auth/cloooud/start" || base == "/api/auth/cloooud/callback" {
		return base
	}
	return path
}
