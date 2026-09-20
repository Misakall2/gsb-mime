package mimemsg

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
)

// Marshal 按 Part 树重新序列化。子段顺序、multipart 类型、叶子正文字节
// （重新编码前）与 UTF-8 文件名都保留；空行与折叠空白允许和原文不同。
func Marshal(p *Part) ([]byte, error) {
	return marshalEntity(p, true)
}

func marshalEntity(p *Part, root bool) ([]byte, error) {
	var out []byte

	if p.isMultipart() {
		boundary, err := ensureBoundary(p)
		if err != nil {
			return nil, err
		}
		out = append(out, serializeHeaders(p, boundary)...)
		out = append(out, "\r\n"...)
		var b strings.Builder
		for _, child := range p.Parts {
			entity, err := marshalEntity(child, false)
			if err != nil {
				return nil, err
			}
			b.WriteString("--")
			b.WriteString(boundary)
			b.WriteString("\r\n")
			b.Write(entity)
			if !hasCRLFSuffix(entity) {
				b.WriteString("\r\n")
			}
		}
		b.WriteString("--")
		b.WriteString(boundary)
		b.WriteString("--\r\n")
		out = append(out, b.String()...)
		return out, nil
	}

	out = append(out, serializeHeaders(p, "")...)
	cte := p.CTE
	if cte == "" {
		cte = chooseCTE(p.MediaType(), p.Body)
	}
	body, err := encodeTransfer(p.Body, cte)
	if err != nil {
		return nil, err
	}
	out = append(out, "Content-Transfer-Encoding: "...)
	out = append(out, cte...)
	out = append(out, "\r\n\r\n"...)
	out = append(out, body...)
	if root && len(body) > 0 && !strings.HasSuffix(string(body), "\r\n") {
		out = append(out, "\r\n"...)
	}
	return out, nil
}

func ensureBoundary(p *Part) (string, error) {
	if b := p.ctParams["boundary"]; b != "" {
		return b, nil
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	b := "gsb-mime-" + hex.EncodeToString(raw[:])
	if p.ctParams == nil {
		p.ctParams = map[string]string{}
	}
	p.ctParams["boundary"] = b
	return b, nil
}

func hasCRLFSuffix(b []byte) bool {
	return len(b) >= 2 && b[len(b)-2] == '\r' && b[len(b)-1] == '\n'
}

// serializeHeaders 产出头区块（含末尾空行）。multipart 的 boundary 单独传入。
func serializeHeaders(p *Part, boundary string) []byte {
	var b strings.Builder

	mt := p.MediaType()
	b.WriteString("Content-Type: ")
	b.WriteString(mt)
	ctParams := orderedContentTypeParams(p, boundary)
	for _, kv := range ctParams {
		b.WriteString("; ")
		b.WriteString(formatPValue(kv[0], kv[1]))
	}
	b.WriteString("\r\n")

	if p.disposition != "" {
		b.WriteString("Content-Disposition: ")
		b.WriteString(p.disposition)
		for _, name := range sortedKeys(p.dispParams) {
			b.WriteString("; ")
			b.WriteString(formatPValue(name, p.dispParams[name]))
		}
		b.WriteString("\r\n")
	}

	keys := make([]string, 0, len(p.Header))
	for k := range p.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range p.Header[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(encodeEncodedWords(v))
			b.WriteString("\r\n")
		}
	}

	return []byte(b.String())
}

// orderedContentTypeParams 决定 Content-Type 参数输出顺序：
// boundary 永远紧随主类型，其余按名字排序。
func orderedContentTypeParams(p *Part, boundary string) [][2]string {
	keys := make([]string, 0, len(p.ctParams)+1)
	seen := map[string]bool{}
	if boundary != "" {
		keys = append(keys, "boundary")
		seen["boundary"] = true
	}
	rest := make([]string, 0, len(p.ctParams))
	for k := range p.ctParams {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)

	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		v := p.ctParams[k]
		if k == "boundary" {
			v = boundary
		}
		out = append(out, [2]string{k, v})
	}
	return out
}
