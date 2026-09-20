// Package mimemsg 用 Go 标准库实现 MIME 邮件的解析与再封装。
//
// 能力边界：
//   - Parse 解开 multipart/mixed、multipart/alternative、multipart/related
//   - 解开 multipart/mixed、multipart/alternative、multipart/related 的任意
//     嵌套，子段严格按原始顺序保留（alternative 的 plain/html 不会对调）；
//   - 叶子段支持 quoted-printable 与 base64 两种 Content-Transfer-Encoding，
//     非法 QP 转义、base64 缺填充等错误返回给调用方，不 panic；
//   - 7bit / 8bit / binary 正文原样透传，不会误当 base64 解码；
//   - message/rfc822 转发段递归解析成内层完整 Part 树（Part.Message），
//     Forward 可把一封已解析的信深拷贝后再包一层，转发段只允许
//     7bit/8bit/binary 标识型编码，8bit 中文正文直接转发；
//   - RelatedHTML / RelatedResources 从 multipart/related 取 HTML 根段
//     （认 start 参数）并列出 HTML 引用到的 cid -> 字节映射；非法
//     Content-ID、悬空 cid、缺 HTML 根段都返回错误；
//   - 头字段支持 RFC 2047 encoded-word（Q/B、多段相邻拼接）与 RFC 2231
//     filename*0*/filename*1* 续行；charset 只接受 us-ascii 与 utf-8，
//     未知 charset 返回 ErrUnsupportedCharset；
//   - Marshal 按同一棵树重新序列化，multipart 段数、媒体类型、附件字节、
//     UTF-8 文件名、Content-ID 都能往返还原；折叠空白不保证与原文逐字节一致。
//
// 本包不包含 SMTP 客户端，也不依赖 golang.org/x 或第三方 mime 库。
package mimemsg
