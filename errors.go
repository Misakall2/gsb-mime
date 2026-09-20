package mimemsg

import "errors"

var (
	// ErrEmptyBoundary 表示 multipart 的 boundary 参数为空（RFC 2046 禁止）。
	ErrEmptyBoundary = errors.New("mimemsg: empty multipart boundary")
	// ErrMissingClosingBoundary 表示输入结束也没有遇到收尾的 "--boundary--"。
	ErrMissingClosingBoundary = errors.New("mimemsg: missing closing multipart boundary")
	// ErrUnsupportedCharset 表示遇到了 us-ascii / utf-8 之外的字符集，
	// 库不会假装它是 UTF-8。
	ErrUnsupportedCharset = errors.New("mimemsg: unsupported charset")
	// ErrInvalidBase64 表示正文的 base64 传输编码无法解码（含缺填充）。
	ErrInvalidBase64 = errors.New("mimemsg: invalid base64 transfer encoding")
	// ErrInvalidQuotedPrintable 表示正文的 quoted-printable 非法
	// （例如 "=XY" 不是两个十六进制数字）。
	ErrInvalidQuotedPrintable = errors.New("mimemsg: invalid quoted-printable transfer encoding")
	// ErrUnsupportedEncoding 表示 Content-Transfer-Encoding 不被识别。
	ErrUnsupportedEncoding = errors.New("mimemsg: unsupported content-transfer-encoding")
	// ErrMalformedHeader 表示头字段语法损坏（例如没有冒号）。
	ErrMalformedHeader = errors.New("mimemsg: malformed header field")
	// ErrMalformedParameter 表示 Content-Type / Content-Disposition
	// 参数语法损坏，或者 2231 续行序号缺失、跳跃、重复。
	ErrMalformedParameter = errors.New("mimemsg: malformed media parameter")
)
