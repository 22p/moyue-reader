# 墨阅 · 开发与 Agent 提示

> 本文件面向继续开发本项目的工程师与 AI Agent；面向使用者的功能、部署与 API 说明见 [README.md](README.md)。

## Agent 提示词

你正在维护「墨阅」——一个用 Go 与原生前端实现的电子书库与阅读器。

**目标**：在不改变现有部署方式（单二进制、无 npm 构建、无外部 CDN）的前提下完成需求，并保持四语界面、移动端、PWA 与阅读进度兼容。

**硬性约束**

- 后端 Go 1.27，SQLite 使用 `modernc.org/sqlite`，不要引入 CGO 依赖
- 前端是 `web/` 下的原生 HTML / CSS / JS，通过 `go:embed` 打包；不要引入构建步骤或 CDN
- 用户可见文案必须同步 `web/assets/i18n.js` 的中 / 英 / 日 / 韩词典；动态拼接的文案用 `tr()` 包一层
- 数据结构变更必须通过 `internal/store/migrations.go` 递增 `schemaVersion`，并补迁移测试
- 书库路径、阅读进度、书签、批注按账号隔离；接口权限检查不可绕过
- 修改分页、朗读、上传、扫描等功能后，用真实书籍做浏览器回归，而不是只看编译通过

**工作方式**

1. 先读相关代码与 store schema，确认数据来源，再动 UI
2. 尽量复用现有 API、状态与组件，不新增不必要的依赖
3. 完成后运行 `gofmt`、`go vet ./...`、`go test ./...`，并验证桌面与移动端
4. 交付时说明改动文件、行为变化、验证方式与已知限制

## 实现要点

**认证**：PBKDF2-SHA256（标准库 `crypto/pbkdf2`），登录创建 32 字节随机 token，服务端只存哈希；Cookie 为 `HttpOnly` + `SameSite=Lax`，30 天有效，TLS 下带 `Secure`；同一 IP 10 分钟内失败 10 次会被暂时拒绝。放在反向代理后请加 `-trust-proxy`，否则真实 IP 与 HTTPS 判断不准确。

**权限**：书籍访问始终经过「书籍 → 书库 → 用户授权」校验；管理员绕过书库授权；章节、页面、PDF、封面、下载、进度、书签接口均做同一套检查。

**扫描**：`filepath.WalkDir` 递归目录，跳过隐藏目录；已有记录在 `size + mtime` 未变时直接跳过；文件变化后重新解析并保留 `AddedAt` 与阅读进度。完整扫描结束后用 `PruneBooks` 删除磁盘上已不存在的书籍记录；本地书库在 `GET /api/books` 时也会做一次轻量存在性检查。PDF 用 pdfcpu 读页数，漫画用 zip / rardecode 列页，FB2 / MOBI / AZW3 分别由纯 Go 解析器分章。

**TXT**：按 BOM / UTF-8 / GB18030 / Big5 顺序解码，按行扫描章节标题；章节以字节区间保存，只渲染当前章节。

**EPUB**：解析 `container.xml → OPF → nav/NCX`，服务端清洗 `<script>`、`on*`、`javascript:`，重写相对资源地址，并给章节响应加 `CSP: script-src 'none'`。

**PDF**：PDF.js 只渲染当前页到 canvas；父页面通过 `postMessage` 下发页码与主题，PDF 响应带独立 CSP 与认证。页数由 pdfcpu 在扫描时读取。

**漫画**：CBZ 直接读 ZIP；CBR 用纯 Go 的 rardecode 读取 RAR4/5。页面按自然顺序编号，第一页复用为封面。

**搜索与批注**：搜索在服务端按章节抽取纯文本，返回章节内百分比与摘要；批注位置使用渲染后章节正文的 UTF-16 字符偏移，存 SQLite 并在每次章节加载时重新包裹 `<mark>`。原始 EPUB / TXT / FB2 / MOBI 文件不会被修改。

**有声书**：扫描时登记为单章，`/audio` 走 `http.ServeFile` 支持 Range；播放器通过 `postMessage` 上报播放比例，父页面沿用现有进度接口保存。

**分页排版**：文字书在 iframe 内用 CSS 多栏排版，固定栏高后横向溢出形成页；单页一栏、双页两栏，按整屏宽度横向平移。页数由 `scrollWidth` 与栏宽推算，窗口缩放或字号变化时按当前比例重新分页。PDF 与漫画的双页由服务端 / PDF.js 并排渲染相邻页。

**书架文件夹**：书库递归扫描后，书籍的 `file` 字段保留相对路径。前端按路径的第一段 / 当前段拆出文件夹与直属书籍，不需要额外的数据库表；空文件夹不会显示。

**WebDAV**：`internal/webdav` 用 `PROPFIND Depth: 1` 递归枚举目录。扫描只写入文件名、大小、修改时间等元数据（`webdavBookStub`），不下载正文；`loadBook` 首次访问时调用 `ensureBookContent` 按需 `GET` 到 `data/webdav/<hash>/`，再复用本地解析与入库流程。远程文件变化时扫描会删除旧缓存并重置元数据。书库的 `kind` 为 `webdav`，缓存路径存在 `libraries.path`，远程地址与凭据存在 `remote_*` 字段。

**语音朗读**：`POST /api/books/{id}/tts` 校验书籍权限后，由服务端携带 API Key 调用 MiMo 的 OpenAI 兼容接口（assistant 消息放正文，`audio.format=mp3`），返回 MP3 并缓存到 `data/tts/`。前端按中文标点切句后合并为 300–400 字的朗读块，播放时按字数比例逐句高亮，朗读到页末时自动翻页，章末自动加载下一章。

**阅读记录**：滚动停止 900ms 后上传进度，页面隐藏/关闭时用 `fetch(keepalive)` 补写；阅读时长每 10 秒累计、30 秒上报，退出时补零头。

## 常用命令

```bash
go run .                              # 本地运行
make build                            # 静态二进制 → bin/moyue
go test ./...                         # 全部测试
gofmt -w . && go vet ./...            # 格式化与静态检查
docker build -t moyue .               # 容器镜像
```
