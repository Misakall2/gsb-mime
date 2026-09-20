package mimemsg

import (
	"fmt"
	"regexp"
)

// WrapMessage 把一封已解析的信（inner）包成一个 message/rfc822 叶子段，
// 供网关做转发封装。返回的 Part 挂在 multipart/mixed 之类的父段下即可；
// 内层树原样挂在 Message 字段，不做深拷贝。
func WrapMessage(inner *Part) *Part {
	return &Part{
		mediaType: "message/rfc822",
		Header:    Header{},
		Message:   inner,
	}
}

// RelatedHTML 从 multipart/related 节点抽取根 HTML 正文与 cid -> 字节
// 映射。HTML 中的 cid: 引用保持原样不改写。规则钉死如下：
//   - 节点必须是 multipart/related，否则 ErrNotRelated；
//   - 必须存在根 text/html 段（默认第一个子段；start 参数非空时按
//     Content-ID 匹配，值可带可不带尖括号），否则 ErrMissingHTMLPart；
//   - 每个带 Content-ID 的段，cid 必须是 "<...>" 形态且不重复，
//     否则 ErrInvalidCID；
//   - HTML 里每个 cid: 引用都必须能在映射中找到，否则 ErrInvalidCID。
func (p *Part) RelatedHTML(start string) (html []byte, resources map[string][]byte, err error) {
	if !p.isMultipart() || p.mediaType != "multipart/related" {
		return nil, nil, ErrNotRelated
	}
	root := p.relatedRoot(start)
	if root == nil || root.isMultipart() || root.Message != nil || root.MediaType() != "text/html" {
		return nil, nil, fmt.Errorf("%w: start=%q", ErrMissingHTMLPart, start)
	}

	resources = map[string][]byte{}
	if err := collectCIDResources(p, root, resources); err != nil {
		return nil, nil, err
	}
	html = root.Body

	for _, ref := range extractCIDRefs(html) {
		if ref == "" {
			return nil, nil, fmt.Errorf("%w: empty cid reference", ErrInvalidCID)
		}
		if _, ok := resources[ref]; !ok {
			return nil, nil, fmt.Errorf("%w: html references cid:%s without matching part", ErrInvalidCID, ref)
		}
	}
	return html, resources, nil
}

// relatedRoot 按 RFC 2387 找 related 的根段：显式 start 参数（可带
// 可不带尖括号）优先，否则取第一个子段。
func (p *Part) relatedRoot(start string) *Part {
	if start != "" {
		want := start
		if len(want) >= 2 && want[0] == '<' && want[len(want)-1] == '>' {
			want = want[1 : len(want)-1]
		}
		for _, child := range p.Parts {
			if bareContentID(child.ContentID()) == want {
				return child
			}
		}
		return nil
	}
	if len(p.Parts) == 0 {
		return nil
	}
	return p.Parts[0]
}

// collectCIDResources 收集 related 子树里所有带 Content-ID 的叶子段，
// 键为去掉尖括号的裸 cid。根段本身不参与；只下钻 multipart 子节点，
// 不再进入嵌套 message/rfc822。
func collectCIDResources(container, root *Part, out map[string][]byte) error {
	for _, child := range container.Parts {
		if child == root {
			continue
		}
		if child.isMultipart() {
			if err := collectCIDResources(child, root, out); err != nil {
				return err
			}
			continue
		}
		raw := child.ContentID()
		if raw == "" {
			continue
		}
		cid, ok := validBareCID(raw)
		if !ok {
			return fmt.Errorf("%w: %q", ErrInvalidCID, raw)
		}
		if _, dup := out[cid]; dup {
			return fmt.Errorf("%w: duplicate cid %q", ErrInvalidCID, raw)
		}
		out[cid] = child.Body
	}
	return nil
}

// validBareCID 校验 Content-ID 是 "<addr-spec>" 形态并返回裸 cid：
// 必须有尖括号包裹，内部非空且不含空白、尖括号或控制字符。
func validBareCID(raw string) (string, bool) {
	if len(raw) < 3 || raw[0] != '<' || raw[len(raw)-1] != '>' {
		return "", false
	}
	inner := raw[1 : len(raw)-1]
	if inner == "" {
		return "", false
	}
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c <= ' ' || c >= 0x7f || c == '<' || c == '>' {
			return "", false
		}
	}
	return inner, true
}

// bareContentID 取 Content-ID 的裸 cid；不做合法性断言，仅用于 start 匹配。
func bareContentID(raw string) string {
	if len(raw) >= 2 && raw[0] == '<' && raw[len(raw)-1] == '>' {
		return raw[1 : len(raw)-1]
	}
	return raw
}

var (
	// 前置字符不允许是字母数字，避免把 "acid:x" 之类误当 cid 引用。
	cidRefPattern = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])cid:[^\s"'<>]*`)
)

// extractCIDRefs 从 HTML 中抽出 cid: 引用，返回去掉 "cid:" 前缀的
// 裸 cid 列表（保持出现顺序，允许重复引用）。空引用（cid: 后面紧跟
// 引号或空白）以空串返回，由调用方按非法 cid 报错。
func extractCIDRefs(html []byte) []string {
	matches := cidRefPattern.FindAllString(string(html), -1)
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		off := 4
		if len(m) > 4 && m[0] != 'c' {
			off++ // 命中了前导边界字符（^ 时 m[0] 就是 'c'）
		}
		refs = append(refs, m[off:])
	}
	return refs
}
