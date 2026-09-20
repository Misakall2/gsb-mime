# gsb-mime

Go standard library only. `go test ./...`

MIME 邮件编解码库（解析树 + 同树再封装），只用于网关拆信/重封，不含 SMTP。

## 能力

- `Parse([]byte) (*Part, error)`：解开 `multipart/mixed`、
  `multipart/alternative`、`multipart/related` 任意嵌套，子段顺序保持不动。
- 叶子正文支持 `quoted-printable`、`base64`；QP 非法十六进制、base64
  缺填充/损坏一律返回错误，不 panic。
- 头字段支持 RFC 2047 encoded-word（Q/B，同一头多段拼接）和 RFC 2231
  `filename*0*`/`filename*1*` 续行；charset 仅支持 `us-ascii`、`utf-8`，
  未知 charset 返回 `ErrUnsupportedCharset`，不会静默当 UTF-8。
- `Marshal(*Part) ([]byte, error)`：按解析树重新序列化。段数、媒体类型、
  附件字节、UTF-8 文件名、`Content-ID` 可往返；折叠空白允许有差异。
- `Forward(*Part) (*Part, error)`：把一封已解析的信深拷贝后包成
  `message/rfc822` 转发段，解开后 `Part.Message` 是内层完整树。
  转发段只标 `7bit`/`8bit`/`binary`（按内层字节自动选），不改成
  base64，8bit 中文正文可直接转发。
- `RelatedHTML(*Part)` / `RelatedResources(*Part)`：从
  `multipart/related` 取 HTML 根段（认 `start` 参数），并列出 HTML
  实际引用到的 `cid -> 字节` 映射（HTML 里的 cid 原样保留）。
  非法 `Content-ID`、悬空 cid、缺 HTML 根段返回 `ErrInvalidCID` /
  `ErrRelatedNoHTML`。
- `7bit` / `8bit` / `binary` 正文原样透传，绝不会被当 base64 乱解。

## 钉死的边界行为

- boundary 带引号：按 RFC 2045 去引号，内部空格原样保留。
- 空 boundary（`boundary=""`）：`ErrEmptyBoundary`。
- 缺收尾 `--boundary--`：`ErrMissingClosingBoundary`；粘在正文里、
  不在行首的伪收尾行不算数。事故宽容：输入恰好结束在最后一个
  `--boundary` 定界行（少了收尾两条横线）时按正常收尾处理，已收集
  的段一个不丢（与 Go 标准库 `mime/multipart` 一致）；定界行后还
  粘着非空白字符仍按缺失收尾报错。
- RFC 2047：encoded-word 内部被折行（token 被 CRLF+WSP 切开）会
  先拼回再解码；相邻 encoded-word 之间的空白吞并，词与普通文本
  之间的空格保留。
- RFC 2231 续行：`charset'lang'` 前缀只认第 0 段，后续段按纯数据
  做 percent 解码后拼字节，再统一按首段 charset 解释（多字节字符
  可以跨段）。
- quoted-printable：兼容 `=` 与 CRLF 之间夹了 WSP 的事故软换行，
  软换行后的未编码 UTF-8 高位字节按原样拼接；`abc=`、`=XY` 这类
  真损坏仍然返回 `ErrInvalidQuotedPrintable`。
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
```
