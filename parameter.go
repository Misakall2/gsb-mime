package mimemsg

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 参数值解析后的形态。同一个逻辑参数（例如 filename）可能同时有
// 普通值和 RFC 2231 扩展值；扩展值优先。
type paramValue struct {
	value   string // 普通参数值，已经过 quoted-string 反转义
	ext     string // RFC 2231 单段扩展值（filename*=...），已做 percent 解码
	extOK   bool
	segs    map[int]string // 2231 多段值，各段保留 percent 解码后的原始字节
	charset string
}

func newParamValue() *paramValue {
	return &paramValue{segs: map[int]string{}}
}

type tokenizer struct {
	s   string
	pos int
}

func (t *tokenizer) skipLWSP() {
	for t.pos < len(t.s) {
		switch t.s[t.pos] {
		case ' ', '\t':
			t.pos++
		default:
			return
		}
	}
}

// readToken 读取 RFC 2045 tspecials 之外的 token。
func (t *tokenizer) readToken() (string, bool) {
	start := t.pos
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		if c <= ' ' || c == 0x7f || strings.IndexRune(`()<>@,;:\"[]?=`, rune(c)) >= 0 {
			break
		}
		t.pos++
	}
	return t.s[start:t.pos], t.pos > start
}

func (t *tokenizer) readQuotedString() (string, bool, error) {
	if t.pos >= len(t.s) || t.s[t.pos] != '"' {
		return "", false, nil
	}
	var b strings.Builder
	t.pos++
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		switch {
		case c == '"':
			t.pos++
			return b.String(), true, nil
		case c == '\\':
			t.pos++
			if t.pos >= len(t.s) {
				return "", false, fmt.Errorf("%w: dangling backslash in quoted string", ErrMalformedParameter)
			}
			b.WriteByte(t.s[t.pos])
			t.pos++
		case c == '\r' || c == '\n':
			return "", false, fmt.Errorf("%w: raw line break in quoted string", ErrMalformedParameter)
		default:
			b.WriteByte(c)
			t.pos++
		}
	}
	return "", false, fmt.Errorf("%w: unterminated quoted string", ErrMalformedParameter)
}

// parseParameters 解析 "a=1; b=\"x\"; c*0*=us-ascii”..." 形式的参数串。
// 返回值保留参数原始出现顺序。
func parseParameters(s string) (map[string]*paramValue, []string, error) {
	t := &tokenizer{s: s}
	params := map[string]*paramValue{}
	var order []string

	for {
		t.skipLWSP()
		if t.pos >= len(t.s) {
			return params, order, nil
		}
		name, ok := t.readToken()
		if !ok {
			return nil, nil, fmt.Errorf("%w: expected parameter name", ErrMalformedParameter)
		}
		t.skipLWSP()
		if t.pos >= len(t.s) || t.s[t.pos] != '=' {
			return nil, nil, fmt.Errorf("%w: parameter %q missing '='", ErrMalformedParameter, name)
		}
		t.pos++
		t.skipLWSP()

		raw, isQuoted, err := t.readQuotedString()
		if err != nil {
			return nil, nil, err
		}
		if !isQuoted {
			var ok2 bool
			raw, ok2 = t.readToken()
			if !ok2 {
				return nil, nil, fmt.Errorf("%w: parameter %q missing value", ErrMalformedParameter, name)
			}
		}

		logical := strings.ToLower(name)
		isExtended := false
		segIndex := -1
		if strings.HasSuffix(logical, "*") {
			isExtended = true
			logical = logical[:len(logical)-1]
		}
		if star := strings.LastIndex(logical, "*"); isExtended && star >= 0 {
			if n, perr := strconv.Atoi(logical[star+1:]); perr == nil {
				segIndex = n
				logical = logical[:star]
			}
		}

		pv := params[logical]
		if pv == nil {
			pv = newParamValue()
			params[logical] = pv
			order = append(order, logical)
		}

		if !isExtended {
			pv.value = raw
		} else {
			if perr := pv.assignExtended(segIndex, raw); perr != nil {
				return nil, nil, fmt.Errorf("parameter %q: %w", logical, perr)
			}
		}

		t.skipLWSP()
		if t.pos >= len(t.s) {
			return params, order, nil
		}
		if t.s[t.pos] != ';' {
			return nil, nil, fmt.Errorf("%w: unexpected %q after parameter %q", ErrMalformedParameter, t.s[t.pos], name)
		}
		t.pos++
	}
}

func (pv *paramValue) assignExtended(segIndex int, raw string) error {
	if segIndex < 0 {
		charset, _, data, hasCharset := splitExtValue(raw)
		decoded, err := decodePercent(data)
		if err != nil {
			return err
		}
		pv.ext = decoded
		pv.extOK = true
		if hasCharset {
			pv.charset = strings.ToLower(charset)
		}
		return nil
	}

	if _, dup := pv.segs[segIndex]; dup {
		return fmt.Errorf("%w: duplicate segment %d", ErrMalformedParameter, segIndex)
	}
	data := raw
	if segIndex == 0 {
		if charset, _, rest, hasCharset := splitExtValue(raw); hasCharset {
			pv.charset = strings.ToLower(charset)
			data = rest
		}
	}
	decoded, err := decodePercent(data)
	if err != nil {
		return err
	}
	pv.segs[segIndex] = decoded
	return nil
}

// splitExtValue 切分 RFC 2231 的 charset'lang'value。
// 没有 charset 段（lang/charset 段只允许在第 0 段或单段扩展值里）时 hasCharset 为 false。
func splitExtValue(v string) (charset, lang, data string, hasCharset bool) {
	first := strings.IndexByte(v, '\'')
	if first < 0 {
		return "", "", v, false
	}
	second := strings.IndexByte(v[first+1:], '\'')
	if second < 0 {
		// 畸形：只有一个引号，整体按数据处理。
		return "", "", v, false
	}
	second += first + 1
	return v[:first], v[first+1 : second], v[second+1:], true
}

// decodePercent 是 RFC 2231 percent-encoding；'+' 就是普通加号。
// 返回原始字节，charset 在完整续行拼接后再解释。
func decodePercent(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", fmt.Errorf("%w: bad percent escape", ErrMalformedParameter)
		}
		b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
		i += 2
	}
	return b.String(), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func decodeCharset(s, charset string) (string, error) {
	if !isSupportedCharset(charset) {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedCharset, normalizeCharset(charset))
	}
	return s, nil
}

func parseParameterString(s string) (map[string]string, error) {
	params, _, err := parseParameters(s)
	if err != nil {
		return nil, err
	}
	return resolveParams(params)
}

// resolve 按 RFC 2231 规则合并参数：多段扩展值优先于单段扩展值，
// 扩展值优先于普通值；序号必须从 0 开始连续。
func (pv *paramValue) resolve() (string, error) {
	if len(pv.segs) > 0 {
		n := len(pv.segs)
		for i := 0; i < n; i++ {
			if _, ok := pv.segs[i]; !ok {
				return "", fmt.Errorf("%w: segmented parameter missing index %d", ErrMalformedParameter, i)
			}
		}
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(pv.segs[i])
		}
		return decodeCharset(b.String(), pv.charset)
	}
	if pv.extOK {
		return decodeCharset(pv.ext, pv.charset)
	}
	return pv.value, nil
}

func resolveParams(params map[string]*paramValue) (map[string]string, error) {
	out := make(map[string]string, len(params))
	for k, pv := range params {
		v, err := pv.resolve()
		if err != nil {
			return nil, err
		}
		// 普通参数值允许使用 RFC 2047 encoded-word（历史上 filename
		// 常这么发）；扩展值（filename*）已经按 charset 解码过，不走这里。
		if !pv.extOK && len(pv.segs) == 0 && strings.Contains(v, "=?") {
			if d, derr := decodeEncodedWords(v); derr == nil {
				v = d
			}
		}
		out[k] = v
	}
	return out, nil
}

// formatPValue 为单个参数选择序列化策略：非 ASCII 值用 RFC 2231
// 单段扩展（UTF-8 + percent-encoding），ASCII 值正常引用。
func formatPValue(name, value string) string {
	if isASCII(value) {
		return name + "=" + quoteIfNeeded(value)
	}
	return fmt.Sprintf("%s*=utf-8''%s", name, percentEncode(value))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || strings.IndexRune(`()<>@,;:\"/[]?=`, rune(c)) >= 0 {
			return true
		}
	}
	return false
}

func quoteIfNeeded(s string) string {
	if !needsQuote(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// percentEncode 按 RFC 3986 unreserved 字符集编码，其余字节全部转义，
// 保证 UTF-8 文件名原样回来。
func percentEncode(s string) string {
	const unreserved = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
