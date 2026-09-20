package mimemsg

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Forward 把一封已解析的邮件深拷贝后包成 message/rfc822 转发段。
// 返回的 *Part 可直接挂进外层 multipart/mixed 的 Parts（再附上转发
// 说明段）。深拷贝保证同一封内层信被转发多次时，各自的树与 boundary
// 互不影响。
func Forward(inner *Part) (*Part, error) {
	if inner == nil {
		return nil, fmt.Errorf("mimemsg: forward requires a parsed message")
	}
	wrap := &Part{
		Header:  Header{},
		Message: clonePart(inner),
	}
	wrap.SetMediaType("message/rfc822")
	return wrap, nil
}

// clonePart 递归深拷贝一棵 Part 树（含头与正文切片）。
func clonePart(p *Part) *Part {
	if p == nil {
		return nil
	}
	c := &Part{
		mediaType:   p.mediaType,
		disposition: p.disposition,
		CTE:         p.CTE,
	}
	if p.ctParams != nil {
		c.ctParams = make(map[string]string, len(p.ctParams))
		for k, v := range p.ctParams {
			c.ctParams[k] = v
		}
	}
	if p.dispParams != nil {
		c.dispParams = make(map[string]string, len(p.dispParams))
		for k, v := range p.dispParams {
			c.dispParams[k] = v
		}
	}
	if p.Header != nil {
		c.Header = Header{}
		for k, vs := range p.Header {
			c.Header[k] = append([]string(nil), vs...)
		}
	}
	if p.Body != nil {
		c.Body = append([]byte(nil), p.Body...)
	}
	if p.Message != nil {
		c.Message = clonePart(p.Message)
	}
	for _, child := range p.Parts {
		c.Parts = append(c.Parts, clonePart(child))
	}
	return c
}

var cidRefPattern = regexp.MustCompile(`(?i)cid:[^\s"'<>()]+`)

// RelatedHTML 返回 multipart/related 段里的 HTML 根段：优先 Content-Type
// 的 start 参数指定的 Content-ID，其次第一个 text/html 子段。
// related 没有 HTML 根段时返回 ErrRelatedNoHTML。
func RelatedHTML(p *Part) (*Part, error) {
	if p == nil || p.MediaType() != "multipart/related" {
		return nil, fmt.Errorf("%w: not a multipart/related part", ErrRelatedNoHTML)
	}
	if start := strings.TrimSpace(p.ctParams["start"]); start != "" {
		for _, child := range p.Parts {
			if child.ContentID() == start {
				return child, nil
			}
		}
		return nil, fmt.Errorf("%w: start parameter %q has no matching part", ErrRelatedNoHTML, start)
	}
	for _, child := range p.Parts {
		if child.MediaType() == "text/html" {
			return child, nil
		}
	}
	return nil, ErrRelatedNoHTML
}

// RelatedResources 抽出 multipart/related 的 HTML 根段正文，并列出
// HTML 实际引用到的 cid -> 字节映射。规则：

// - 每个带 Content-ID 的子段都必须是 "<id>" 形态，否则 ErrInvalidCID；
// - HTML 中每个 cid: 引用都必须有对应子段，否则 ErrInvalidCID；
// - 没有 text/html 根段（或 start 指空）返回 ErrRelatedNoHTML；
// - 返回的字节是拷贝，HTML 中的 cid 引用保持原样，不做替换。
func RelatedResources(p *Part) (html []byte, resources map[string][]byte, err error) {
	root, err := RelatedHTML(p)
	if err != nil {
		return nil, nil, err
	}

	byCID := make(map[string]*Part)
	for _, child := range p.Parts {
		raw := child.ContentID()
		if raw == "" {
			continue
		}
		if len(raw) < 2 || raw[0] != '<' || raw[len(raw)-1] != '>' {
			return nil, nil, fmt.Errorf("%w: %q is not angle-bracketed", ErrInvalidCID, raw)
		}
		id := raw[1 : len(raw)-1]
		if id == "" {
			return nil, nil, fmt.Errorf("%w: empty content-id", ErrInvalidCID)
		}
		byCID[id] = child
	}

	htmlText := string(root.Body)
	refs := cidRefPattern.FindAllString(htmlText, -1)
	resources = make(map[string][]byte)
	for _, ref := range refs {
		id := ref[len("cid:"):]
		if decoded, derr := url.PathUnescape(id); derr == nil {
			id = decoded
		}
		child, ok := byCID[id]
		if !ok {
			return nil, nil, fmt.Errorf("%w: cid:%s has no matching part", ErrInvalidCID, id)
		}
		if _, seen := resources[id]; !seen {
			resources[id] = append([]byte(nil), child.Body...)
		}
	}
	return append([]byte(nil), root.Body...), resources, nil
}
