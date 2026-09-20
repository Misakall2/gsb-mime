# gsb-mime

Go standard library only. `go test ./...`

MIME 邮件编解码库（解析树 + 同树再封装），只用于网关拆信/重封，不含 SMTP。

## 能力

- `Parse([]byte) (*Part, error)`：解开 `multipart/mixed`、
  `multipart/alternative`、`multipart/related` 任意嵌套，子段顺序保持不动。
- 叶子正文支持 `quoted-printable`、`base64`；QP 非法十六进制、base64
  缺填充/损坏一律返回错误，不 panic。
- `7bit` / `8bit` / `binary` 身份编码原样透传：8bit 中文正文、binary
  附件不会被当 base64 乱解，解-编-解字节一致。
- `message/rfc822` 封装段：`Parse` 后内层树挂在 `Part.Message`；
  `WrapMessage(inner)` 把已解析的信包一层用于转发，封装段只允许
  身份 CTE（base64/QP 返回 `ErrUnsupportedEncoding`）。
- `Part.RelatedHTML(start)` 从 `multipart/related` 取根 HTML 与
  `cid -> []byte` 映射，HTML 里的 `cid:` 引用不改写；缺根 HTML、
  cid 非法/重复/悬挂返回 `ErrMissingHTMLPart` / `ErrInvalidCID`。
- 头字段支持 RFC 2047 encoded-word（Q/B，同一头多段拼接）和 RFC 2231
  `filename*0*`/`filename*1*` 续行；charset 仅支持 `us-ascii`、`utf-8`，
  未知 charset 返回 `ErrUnsupportedCharset`，不会静默当 UTF-8。
- `Marshal(*Part) ([]byte, error)`：按解析树重新序列化。段数、媒体类型、
  附件字节、UTF-8 文件名、`Content-ID` 可往返；折叠空白允许有差异。

## 钉死的边界行为

- boundary 带引号：按 RFC 2045 去引号，内部空格原样保留。
- 空 boundary（`boundary=""`）：`ErrEmptyBoundary`。
- 缺收尾 `--boundary--`：`ErrMissingClosingBoundary`；粘在正文里、
  不在行首的伪收尾行不算数。
- preamble / epilogue 忽略，不产生段；空 multipart 合法。

## 快速示例

```go
p, err := mimemsg.Parse(raw)       // raw 是整封邮件的字节
if err != nil { return err }
for _, part := range p.Parts {
    _ = part.MediaType()           // "text/plain" / "image/png" / ...
    _ = part.Filename()            // RFC 2047/2231 拼好后的文件名
    _ = part.ContentID()           // related 的 cid
    _ = part.Body                  // CTE 解码后的字节
}
	out, err := mimemsg.Marshal(p)     // 同树封回，boundary 可重新生成

	fwd := mimemsg.WrapMessage(p)      // 把整封信包成 message/rfc822 段
	html, imgs, err := p.RelatedHTML("") // related: HTML + cid 到字节
```
