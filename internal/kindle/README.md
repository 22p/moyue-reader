# kindle

MOBI / AZW / AZW3 / PRC 解析，改编自
[`jparkerweb/azw3-to-pdf`](https://github.com/jparkerweb/azw3-to-pdf) 的
`internal/ebook`（MIT License，见同目录 `LICENSE`）。

本目录额外增加了：

- `SplitChapters`：按 `<h2>` / `<h1>` / `<mbp:pagebreak>` 拆分章节
- 图片引用重写（`recindex` 与 `kindle:embed`）
- 与墨阅阅读器 HTML 清洗流程的衔接

DRM 加密的 Kindle 文件不会被解密，会直接返回错误。
