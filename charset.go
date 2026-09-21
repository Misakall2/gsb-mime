package mimemsg

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 本包的 charset 政策只在这一个文件里：只认 us-ascii 与 utf-8
// （含常见别名）。2047 encoded-word、2231 扩展参数、Content-Type
// 的 charset 参数、正文校验与 Text() 全部走这里的判定，其它地方
// 不再各自扫 charset；未知 charset 一律报 ErrUnsupportedCharset，
// 绝不静默当 UTF-8。

var errInvalidUTF8 = errors.New("mimemsg: body is not valid utf-8")

// normalizeCharset 统一大小写，供所有 charset 比较使用。
func normalizeCharset(cs string) string {
	return strings.ToLower(cs)
}

// isASCIICharset 报告 cs 是否命名 us-ascii。
func isASCIICharset(cs string) bool {
	switch normalizeCharset(cs) {
	case "us-ascii", "ascii":
		return true
	}
	return false
}

// isUTF8Charset 报告 cs 是否命名 utf-8。
func isUTF8Charset(cs string) bool {
	switch normalizeCharset(cs) {
	case "utf-8", "utf8":
		return true
	}
	return false
}

// checkCharset 在 cs 非空且不受支持时返回 ErrUnsupportedCharset；
// 空串表示未声明 charset，由调用方按各自默认值处理。
func checkCharset(cs string) error {
	if cs == "" || isASCIICharset(cs) || isUTF8Charset(cs) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnsupportedCharset, cs)
}

// requireASCIIBytes 拒绝 us-ascii 文本里的高位字节。
func requireASCIIBytes(b []byte) error {
	for _, c := range b {
		if c >= 0x80 {
			return fmt.Errorf("%w: non-ascii byte in us-ascii text", ErrUnsupportedCharset)
		}
	}
	return nil
}

// validateTextBytes 校验字节流与声明的 charset 一致：
// us-ascii 不得有高位字节；utf-8 必须合法。
func validateTextBytes(cs string, b []byte) error {
	switch {
	case isASCIICharset(cs):
		return requireASCIIBytes(b)
	case isUTF8Charset(cs):
		if !utf8.Valid(b) {
			return errInvalidUTF8
		}
	}
	return nil
}

// validateBodyCharset 钉住正文与声明 charset 的契约（charset 参数本身
// 的合法性在 parseContentType 阶段已被 checkCharset 拒绝）：
// us-ascii 正文不得有高位字节；text/* 或显式声明 utf-8 的正文必须是
// 合法 UTF-8；非文本类型（image/application/...）缺省 charset 不适用，
// 直接放行二进制。
func validateBodyCharset(p *Part) error {
	cs := p.Charset()
	switch {
	case isASCIICharset(cs):
		return requireASCIIBytes(p.Body)
	case isUTF8Charset(cs):
		if p.ctParams["charset"] != "" || strings.HasPrefix(p.mediaType, "text/") {
			return validateTextBytes(cs, p.Body)
		}
	}
	return nil
}
