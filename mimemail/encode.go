package mimemail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/textproto"
	"sort"
	"strings"
	"unicode/utf8"
)

// Encode serializes the message back into an RFC 822/MIME wire format with
// CRLF line endings. The multipart tree, child order, media types, attachment
// bytes and UTF-8 file names round-trip through Parse unchanged (header
// folding whitespace may differ).
func (m *Message) Encode() ([]byte, error) {
	if m == nil || m.Root == nil {
		return nil, fmt.Errorf("mimemail: message has no root part")
	}
	var buf bytes.Buffer
	headers := clonePartHeader(m.Header)
	rootBody, err := encodeRootContentHeaders(headers, m.Root)
	if err != nil {
		return nil, err
	}
	if err := writeHeaderBlock(&buf, headers); err != nil {
		return nil, err
	}
	buf.Write(rootBody)
	return buf.Bytes(), nil
}

// encodeRootContentHeaders populates the envelope header map with the root
// part's content headers and returns the encoded body.
func encodeRootContentHeaders(headers textproto.MIMEHeader, root *Part) ([]byte, error) {
	if root.IsMultipart() {
		boundary := nextBoundary()
		ct := root.Type
		if ct == "" {
			ct = "multipart/mixed"
		}
		setHeader(headers, contentTypeHeader,
			contentTypeWithBoundary(ct, root.TypeParams, boundary))
		if root.ContentID != "" {
			setHeader(headers, contentIDHeader, "<"+root.ContentID+">")
		}
		return encodeMultipartChildren(root, boundary)
	}
	body, leafHeaders, err := encodeLeafContent(root)
	if err != nil {
		return nil, err
	}
	for k, vs := range leafHeaders {
		headers[k] = vs
	}
	return body, nil
}

var boundaryCounter int

func nextBoundary() string {
	boundaryCounter++
	return fmt.Sprintf("gsb-mime-boundary-%d", boundaryCounter)
}

func encodePart(part *Part) ([]byte, error) {
	// Used for any non-root part: emit its header block followed by its body.
	var buf bytes.Buffer
	if part.IsMultipart() {
		if len(part.Parts) == 0 {
			return nil, fmt.Errorf("mimemail: multipart part %q has no children", part.Type)
		}
		boundary := nextBoundary()
		headers := clonePartHeader(part.Header)
		ct := part.Type
		if ct == "" {
			ct = "multipart/mixed"
		}
		setHeader(headers, contentTypeHeader,
			contentTypeWithBoundary(ct, part.TypeParams, boundary))
		if part.ContentID != "" {
			setHeader(headers, contentIDHeader, "<"+part.ContentID+">")
		}
		if err := writeHeaderBlock(&buf, headers); err != nil {
			return nil, err
		}
		body, err := encodeMultipartChildren(part, boundary)
		if err != nil {
			return nil, err
		}
		buf.Write(body)
		return buf.Bytes(), nil
	}
	body, headers, err := encodeLeafContent(part)
	if err != nil {
		return nil, err
	}
	fullHeaders := clonePartHeader(part.Header)
	for k, vs := range headers {
		fullHeaders[k] = vs
	}
	if err := writeHeaderBlock(&buf, fullHeaders); err != nil {
		return nil, err
	}
	buf.Write(body)
	return buf.Bytes(), nil
}

func encodeMultipartChildren(part *Part, boundary string) ([]byte, error) {
	var buf bytes.Buffer
	for _, child := range part.Parts {
		buf.WriteString("--")
		buf.WriteString(boundary)
		buf.WriteString("\r\n")
		encoded, err := encodePart(child)
		if err != nil {
			return nil, err
		}
		buf.Write(encoded)
		if !bytes.HasSuffix(encoded, []byte("\r\n")) {
			buf.WriteString("\r\n")
		}
	}
	buf.WriteString("--")
	buf.WriteString(boundary)
	buf.WriteString("--\r\n")
	return buf.Bytes(), nil
}

// encodeLeafContent returns the encoded leaf body together with the content
// headers that describe it (Content-Type/Disposition/Id/Transfer-Encoding).
func encodeLeafContent(part *Part) ([]byte, textproto.MIMEHeader, error) {
	headers := textproto.MIMEHeader{}
	ct := part.Type
	if ct == "" {
		ct = "text/plain"
	}
	params := cloneParams(part.TypeParams)
	body := append([]byte(nil), part.Body...)

	transferEncoding := "base64"
	var encodedBody []byte
	if is7Bit(body) {
		transferEncoding = "7bit"
		encodedBody = normalizeLineEndings(body)
	} else {
		encodedBody = encodeBase64(body)
		if strings.HasPrefix(strings.ToLower(ct), "text/") &&
			utf8.Valid(body) && !hasCharsetParam(params) {
			params["charset"] = "utf-8"
		}
	}

	setHeader(headers, contentTypeHeader, contentTypeValue(ct, params))

	disposition := part.Disposition
	if disposition == "" && part.FileName != "" {
		disposition = "attachment"
	}
	if disposition != "" {
		cd := disposition
		if part.FileName != "" {
			cd += "; filename*=" + encodeExtendedFilename(part.FileName)
		}
		setHeader(headers, contentDispositionHeader, cd)
	}
	if part.ContentID != "" {
		setHeader(headers, contentIDHeader, "<"+part.ContentID+">")
	}
	setHeader(headers, contentTransferEncoding, transferEncoding)
	return encodedBody, headers, nil
}

func encodeBase64(body []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(body)
	var buf bytes.Buffer
	for len(encoded) > 0 {
		n := 76
		if n > len(encoded) {
			n = len(encoded)
		}
		buf.WriteString(encoded[:n])
		buf.WriteString("\r\n")
		encoded = encoded[n:]
	}
	return buf.Bytes()
}

func is7Bit(body []byte) bool {
	for _, c := range body {
		if c > 127 || c == 0 {
			return false
		}
	}
	return true
}

func hasCharsetParam(params map[string]string) bool {
	for k := range params {
		if strings.EqualFold(k, "charset") {
			return true
		}
	}
	return false
}

func cloneParams(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonePartHeader(h textproto.MIMEHeader) textproto.MIMEHeader {
	out := make(textproto.MIMEHeader, len(h)+4)
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func setHeader(h textproto.MIMEHeader, key, value string) {
	canonical := textproto.CanonicalMIMEHeaderKey(key)
	h[canonical] = []string{value}
}

func contentTypeWithBoundary(mediaType string, params map[string]string, boundary string) string {
	cp := cloneParams(params)
	cp["boundary"] = boundary
	return contentTypeValue(mediaType, cp)
}

func contentTypeValue(mediaType string, params map[string]string) string {
	var b strings.Builder
	b.WriteString(mediaType)
	keys := make([]string, 0, len(params))
	for k := range params {
		if strings.EqualFold(k, "name") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// boundary belongs right after the media type in practice.
	if boundary, ok := lookupParam(params, "boundary"); ok {
		b.WriteString("; boundary=" + quoteParamValue(boundary))
	}
	for _, k := range keys {
		if strings.EqualFold(k, "boundary") {
			continue
		}
		b.WriteString("; ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quoteParamValue(params[k]))
	}
	return b.String()
}

func lookupParam(params map[string]string, want string) (string, bool) {
	for k, v := range params {
		if strings.EqualFold(k, want) {
			return v, true
		}
	}
	return "", false
}

func quoteParamValue(v string) string {
	if v == "" {
		return "\"\""
	}
	if !paramNeedsQuote(v) {
		return v
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func paramNeedsQuote(v string) bool {
	for _, r := range v {
		if r <= 32 || r == 127 || strings.ContainsRune("()<>@,;:\\\"/[]?=", r) {
			return true
		}
	}
	return false
}

func writeHeaderBlock(buf *bytes.Buffer, headers textproto.MIMEHeader) error {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers[key] {
			encoded, err := encodeHeaderField(key, value)
			if err != nil {
				return err
			}
			buf.WriteString(key)
			buf.WriteString(": ")
			buf.WriteString(encoded)
			buf.WriteString("\r\n")
		}
	}
	buf.WriteString("\r\n")
	return nil
}

func encodeHeaderField(key, value string) (string, error) {
	if headerIsASCII(value) {
		return value, nil
	}
	// Encode non-ASCII values as RFC 2047 B encoded-words, splitting on rune
	// boundaries so each encoded line stays under 76 characters.
	var b strings.Builder
	var chunk []byte
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\r\n ")
		}
		b.WriteString("=?UTF-8?B?")
		b.WriteString(base64.StdEncoding.EncodeToString(chunk))
		b.WriteString("?=")
		chunk = nil
	}
	for _, r := range value {
		var encoded [4]byte
		n := utf8.EncodeRune(encoded[:], r)
		candidate := append(append([]byte(nil), chunk...), encoded[:n]...)
		wordLen := len("=?UTF-8?B??=") + base64.StdEncoding.EncodedLen(len(candidate))
		if wordLen > 60 {
			flush()
			candidate = encoded[:n]
		}
		chunk = candidate
	}
	flush()
	return b.String(), nil
}

func headerIsASCII(value string) bool {
	for _, r := range value {
		if r > 127 || r < 32 && r != '\t' {
			return false
		}
	}
	return true
}
