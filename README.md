# 墨阅 · Moyue Library

**书库与阅读器**：管理员维护书库目录与账号，每位读者拥有独立的书架、阅读进度、书签和阅读统计。

支持 **EPUB / TXT / PDF / CBZ / CBR / FB2 / MOBI / AZW3 / 有声书**，内置单页 / 双页 / 滚动三种翻页方式与 **小米 MiMo 语音朗读**。后端 Go 1.27，前端原生 HTML / CSS / JS（无 npm 构建、无 CDN），一个二进制即可部署，界面支持中文、英文、日文与韩文。

![书库](docs/library.png)

## 功能

**账号与权限**
- 首次启动创建管理员，之后由管理员创建账号，不开放公开注册
- 角色区分（管理员 / 普通用户），可访问的书库按账号分配
- 每个账号独立的阅读进度、书签、阅读时长与连续天数
- PBKDF2-SHA256 密码哈希、HttpOnly 会话 Cookie、30 天会话（重启不掉线）
- 登录失败限流；修改密码会注销其它设备
- 登录设备管理：查看设备 / IP / 最后活跃时间，可单独撤销或一键退出其它设备

**书库管理（类 Jellyfin）**
- 添加本地服务器目录或 **WebDAV** 作为书库，按类型扫描：全部格式 / 电子书 / Kindle / 漫画 / PDF / 有声书 / 仅 EPUB / 仅 TXT
- WebDAV 扫描只登记文件名、大小、修改时间与书籍类型，不下载正文；首次打开某本书时才下载并缓存到 `data/webdav/`，之后直接读取缓存
- 书库内的子目录按文件夹展示，可逐层进入，面包屑返回上层；搜索会跨文件夹查找
- 启动时自动扫描，也可在管理台手动扫描；递归子目录，展示扫描状态与失败原因
- EPUB / FB2 / MOBI / AZW3 提取封面、作者、简介；CBZ/CBR 用第一页作封面；PDF 与音频使用文字封面占位
- 管理员可上传、删除书籍；普通用户只能阅读和下载
- 上传按内容哈希（SHA-256）去重，同名文件自动追加序号

**格式支持**

| 格式 | 阅读方式 | 进度记录 |
| --- | --- | --- |
| EPUB 2/3 | 章节重排阅读，支持 nav/NCX 嵌套目录、内链、图片与 CSS | 章节 + 章节内比例 |
| TXT | 自动识别编码并分章，按段落重排 | 章节 + 章节内比例 |
| PDF | 内置 PDF.js 渲染，单页或宽屏双页，滚轮 / 方向键 / 按钮翻页 | 页码 |
| CBZ / CBR | 逐页显示图片，点击左右区域或按钮翻页；宽屏可并排双页 | 页码 |
| FB2 | 解析 FictionBook 2 章节与内嵌图片，按流式文档重排 | 章节 + 章节内比例 |
| MOBI / AZW / AZW3 / PRC | 解析 PalmDB / KF8，拆分章节并重排正文；支持封面 | 章节 + 章节内比例 |
| MP3 / M4A / M4B / FLAC / OGG / OPUS / WAV | 内置音频播放器，播放进度自动保存 | 播放位置百分比 |

**翻页方式**
- 阅读设置里可选 `上下滚动` / `单页` / `双页`，设置按浏览器保存
- 文字书用 CSS 多栏横向排版：单页一栏，双页两栏；窗口宽度不足 960 px 时双页自动退化为单页
- 滚动模式不分栏，双页设置不会生效；PDF 与漫画在宽屏下并排渲染相邻两页
- 分页模式下滚轮、左右方向键、点击页面左右三分之一都会翻页，跨过章末自动进入下一章

**搜索、划线与笔记**
- 整本书全文搜索：EPUB / TXT / FB2 / MOBI / AZW3，返回章节、上下文摘要，点击跳转
- 选中正文即可划线（黄 / 绿 / 蓝 / 粉），可附加笔记；高亮随章节渲染
- 全部保存在 SQLite 的 `annotations` 表，**不修改原始书籍文件**
- 目录抽屉新增「笔记」列表，可跳转、编辑、删除；支持跨设备同步

**TXT 自动分章**
- 编码识别：UTF-8 / BOM / UTF-16 LE·BE / GB18030 / GBK / Big5
- 标题识别：`第X章`、`第X节`、`第X回`、`第X卷`、`序章`、`楔子`、`番外`、`Chapter N` 等
- 自动合并「第一卷」卷标、过滤空章节、跳过书名/作者版权行
- 无标题时按段落切成约 9000 字片段；单文件上限 64MB
- 从 `书名 - 作者.txt`、`《书名》作者` 或正文 `作者：` 提取元数据

**阅读体验**
- 章节 / 页码级进度，重新打开回到原位置
- 翻页方式可切换：**上下滚动、单页、双页**；双页仅在大屏生效，滚动模式不分栏
- **语音朗读（TTS）**：把正文按句朗读，自动跨章、逐句高亮并跟随翻页；默认接入小米 MiMo，API Key 由管理员在管理台配置
- 到达全书最后一页后再次向后翻页会标记为已读完，进度记为 100%
- 书签（文字书带正文摘录，可原地取消）、阅读时长、今日时长、连续阅读、7 日趋势
- 纸白 / 米黄 / 青瓷 / 夜幕四套主题，可在头像菜单或阅读设置切换；默认黑体，宋体 / 黑体 / 楷体可选，字号、行距、版心可调
- 桌面抽屉式目录，手机全屏阅读；拖拽导入、搜索、排序、批量上传
- 中/英/日/韩四语界面，登录页、阅读设置与头像菜单均可切换
- 快捷键：`←`/`→` 翻页或切章，`T` 目录，`B` 书签，`S` 设置，`F` 全屏，`R` 朗读，`Esc` 返回

**上传**
- 独立的上传对话框：拖拽或点选多个文件，逐个显示上传进度、解析状态与失败原因
- 可选择目标书库，支持停止与失败重试；上传完成后自动刷新书架

**PWA / 安装到桌面**
- 可安装到桌面（Android / Chrome / Edge / iOS Safari / macOS），以独立窗口运行，无浏览器地址栏
- 提供 192 / 512 / maskable 图标、离线说明页与浅色 / 深色跟随
- Service Worker 只缓存应用外壳与静态资源，**不缓存 `/api/` 下的书籍、进度或账号数据**
- 头像菜单中提供「安装到桌面」；iOS 会提示通过「分享 → 添加到主屏幕」安装
- 离线时打开应用会显示说明页，恢复网络后重新加载即可继续阅读

## 环境要求

- Go **1.27+**
- 现代浏览器（Chrome / Edge / Safari / Firefox 近两年版本）

Go 依赖：`modernc.org/sqlite`（纯 Go SQLite）、`golang.org/x/text`（GB18030 / Big5 解码）、`golang.org/x/net`（FB2 XML 编码识别）、`github.com/nwaples/rardecode/v2`（CBR/RAR）、`github.com/pdfcpu/pdfcpu`（PDF 页数）。前端内嵌 [PDF.js](https://mozilla.github.io/pdf.js/) 3.11（Apache-2.0），不访问外部 CDN；MOBI/KF8 解析器为 MIT，见 `internal/kindle/LICENSE`。

PWA 的 Service Worker 只在 **HTTPS 或 localhost** 下生效；通过局域网 IP 的普通 HTTP 访问时，阅读器本身可用，但不会出现安装与离线能力。

语音朗读需要服务器能访问你配置的接口地址（默认 `https://api.xiaomimimo.com`）。纯内网部署时，可以在管理台把接口地址改成内网兼容服务，或关闭语音朗读。

## 快速开始

```bash
# 直接运行，默认 http://localhost:8080
go run .

# 或编译（静态二进制，便于单独分发）
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/moyue .
./bin/moyue -addr :8080 -books ./books -data ./data
```

首次打开会要求创建管理员账号。`books/` 内附带八本演示书：两本 EPUB、一本 TXT、一本 PDF、一本 CBZ、一本 FB2、一本 AZW3、一段音频，会自动扫描进「默认书库」。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-addr` | `:8080` | 监听地址，局域网访问用 `0.0.0.0:8080` |
| `-books` | `books` | 默认书库目录（首次启动自动创建为「默认书库」） |
| `-data` | `data` | 数据目录（SQLite 数据库：账号、会话、进度、书签） |
| `-debug` | `false` | 调试日志 |
| `-trust-proxy` | `false` | 部署在受信任反向代理后，读取 `X-Forwarded-Proto/For/Host`（用于 Secure Cookie、真实 IP 与限流） |

## 使用流程

1. **创建管理员**：首次访问填写用户名与密码（至少 6 位）。
2. **添加书库**：头像菜单 → 管理控制台 → 书库 → 选择「本地目录」填写服务器路径（例如 `/srv/comics`），或选择「WebDAV」填写指向书库文件夹的地址与账号密码，创建后立即扫描。
3. **创建账号**：管理控制台 → 用户 → 填写账号密码，勾选可访问的书库；管理员默认拥有全部书库。
4. **开始阅读**：书架点击封面，进度会自动保存；目录、书签、划线笔记、主题与字号都在阅读器内调整。

> 本地书库路径是**服务器上的路径**，不是浏览器本机路径。Docker 部署时需要先把宿主目录挂载进容器。WebDAV 书库只需要容器能访问该地址。

### 语音朗读（TTS）

朗读由服务端代理调用语音合成接口，浏览器只拿到音频，不会接触 API Key。

1. 管理控制台 → **设置** → **语音朗读（TTS）**。
2. 默认服务商是 **小米 MiMo**，接口地址 `https://api.xiaomimimo.com/v1/chat/completions`，模型 `mimo-v2.5-tts`。
3. 在 [小米 MiMo 开放平台](https://platform.xiaomimimo.com) 创建 API Key，粘贴后勾选「启用语音朗读」，保存。
4. 点「测试朗读」，能听到示例语音即表示配置成功。

阅读器顶部耳机图标（快捷键 `R`）开始朗读。朗读条提供播放 / 暂停、上一句 / 下一句、音色与 0.75×–1.5× 语速；从当前阅读位置开始朗读，读完一章自动进入下一章，当前句高亮并自动翻页。

- 可选音色：`mimo_default`、`冰糖`、`茉莉`、`苏打`、`白桦`、`Mia`、`Chloe`、`Milo`、`Dean`
- 朗读按 300–400 字分块合成（块尾对齐句末），音频缓存在 `data/tts/`；同一音色 + 同一段只请求一次，改动模型或音色会生成新的缓存
- 也支持标准 OpenAI 风格的 `/v1/audio/speech` 接口（服务商选「OpenAI 兼容」），可接入自建或第三方兼容服务
- 仅文字书（EPUB / TXT / FB2 / MOBI / AZW3）支持朗读；PDF、漫画与有声书不显示朗读能力
- 正文会发送给你配置的语音服务商；API Key 以明文保存在服务器的 SQLite 中，请限制 `data/` 的访问权限

### TXT 分章说明

支持的行首格式包括：

```text
第一章 出港          第1章 出港          第十二回 夜雨
第二卷 归途          序章 潮声            番外 后日谈
Chapter 12  The Storm
```

- 章节标题需独占一行、长度不超过 42 字，且不含 `。！？；` 等句末标点，避免把正文误判为标题
- 标题前若有「正文」会被自动去除
- 序章之前的书名、作者等版权信息不会被当成「前言」
- 单个 TXT 上限 64MB；扫描时记录章节数，阅读时按字节区间切片

### FB2 / MOBI / AZW3 / 有声书说明

- **FB2**：解析 FictionBook 2 的章节、标题、强调、引用、诗歌与内嵌图片；脚注链接按普通文本处理，不做弹层。
- **MOBI / AZW3**：纯 Go 解析 PalmDB、MOBI 6 与 KF8，支持 PalmDOC 与 HUFF/CDIC 压缩；按 `<h2>` / `<h1>` / 分页符拆分章节。正文样式会统一为阅读器排版，原书复杂 CSS 不保留；MOBI6 的 `recindex` 与 KF8 的 `kindle:embed` 图片可显示，SVG flow 图片可能缺失。
- **有声书**：一个音频文件作为一本书，内置播放器支持进度拖动、±15 秒、播放进度保存；章节标记、ID3 封面与多文件合辑暂未支持。
- 加密 MOBI/AZW3（DRM）不支持，会给出解析失败提示。

### PDF 与漫画说明

- **PDF**：服务端用 pdfcpu 读取页数，浏览器端用 PDF.js 逐页渲染（只渲染当前页，适合大文档）；支持滚轮、方向键、左右滑动与底部按钮翻页，页码进度与书签可用。PDF 不提供文本选择、全文搜索、划线批注，也不读取 PDF 自带目录/书签。
- **CBZ / CBR**：按文件名自然排序（`page2` 在 `page10` 前），跳过非图片与隐藏文件；第一页作为书架封面。单页图片上限 64MB，页面按屏幕大小等比缩放。
- CBR 支持 RAR4 / RAR5；加密 RAR 不支持。加密 PDF 也不支持。

## 数据与备份

```text
data/moyue.db                SQLite：账号、会话、书库、书籍、进度、书签、划线笔记、每日时长、系统设置
data/tts/*.mp3               语音朗读缓存（按「服务商 + 模型 + 音色 + 文本」哈希命名）
data/webdav/<id>/*            WebDAV 已打开书籍的本地缓存（可删除，下次打开会重新下载）
books/*.epub|*.txt|*.pdf     默认书库文件（其他书库在你指定的目录）
books/*.cbz|*.cbr|*.fb2
books/*.mobi|*.azw3|*.mp3
```

- 数据库使用 WAL 模式（运行时会出现 `moyue.db-wal` / `moyue.db-shm`，正常关闭后自动合并）
- 数据库结构带版本号，启动时自动升级；新增字段或表不会丢失已有数据
- 密码使用 PBKDF2-SHA256（210,000 次迭代、随机盐）存储，会话只保存 token 的 SHA-256
- `data/tts/` 是可再生的朗读缓存，可随时删除；删除后首次朗读会重新合成
- `data/webdav/` 只保存已经打开过的 WebDAV 书籍，可随时删除；下次打开会重新下载
- WebDAV 地址、用户名与密码保存在 SQLite；密码当前为明文，请限制 `data/` 目录权限
- 会话表额外保存设备 UA、IP 与最后活跃时间，仅本人可见
- 书籍 ID 由「书库 ID + 相对路径」决定，文件内容更新后原阅读进度仍保留
- 备份时停止服务后复制 `data/` 与各个书库目录；已安装 sqlite3 时也可在运行中执行
  `sqlite3 data/moyue.db ".backup 'backup.db'"`（WAL 模式下不要直接拷贝运行中的 `.db` 文件）
- 直接拷贝进书库目录的新文件，在下次扫描（启动或手动）后出现
- 直接删除或移动书库内文件后，刷新书架会移除失效记录；移动后的新位置在下次扫描后出现
- 从磁盘删除文件不会自动移除记录，避免网络存储临时离线时误删；需要时在书架上删除

## HTTP API

除 `/api/auth/*` 与 `/api/health` 外均需登录；管理接口需管理员。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/auth/state` | 是否已完成初始化、当前登录用户 |
| `POST` | `/api/auth/setup` | 首次创建管理员（仅在无账号时可用） |
| `POST` | `/api/auth/login` / `logout` | 登录 / 退出 |
| `POST` | `/api/auth/password` | 修改自己的密码 |
| `GET` | `/api/auth/sessions` | 当前账号的登录设备列表 |
| `DELETE` | `/api/auth/sessions/{id}` | 撤销指定设备（当前设备会退出） |
| `POST` | `/api/auth/sessions/revoke-others` | 退出其它设备 |
| `GET` | `/api/libraries` | 当前用户可访问的书库 |
| `GET` | `/api/books` | 当前用户可见的书籍（含各自进度） |
| `POST` | `/api/books` | 上传 EPUB / TXT / PDF / CBZ / CBR / FB2 / MOBI / AZW3 / 音频（管理员） |
| `GET` | `/api/books/{id}` | 书籍详情、目录、书签、划线笔记、进度 |
| `DELETE` | `/api/books/{id}` | 删除书籍与文件（管理员） |
| `GET` | `/api/books/{id}/file` | 下载原文件 |
| `GET` | `/api/books/{id}/cover` | 封面（EPUB 内嵌封面 / 漫画第一页） |
| `GET` | `/api/books/{id}/chapter/{index}` | 文字书章节 HTML / 漫画单页 HTML |
| `GET` | `/api/books/{id}/page/{index}` | 漫画单页图片 |
| `GET` | `/api/books/{id}/pdf` | PDF 原始字节（支持 Range） |
| `GET` | `/api/books/{id}/pdfview` | PDF.js 单页阅读器页面 |
| `GET` | `/api/books/{id}/audioview` | 音频播放器页面 |
| `GET` | `/api/books/{id}/audio` | 音频原始字节（支持 Range） |
| `GET` | `/api/books/{id}/fb2image?name=` | FB2 内嵌图片 |
| `GET` | `/api/books/{id}/kindleimage/{index}` | MOBI/AZW3 内嵌图片 |
| `GET` | `/api/books/{id}/resource?path=` | EPUB 内部资源 |
| `PUT` | `/api/books/{id}/progress` | 保存进度 `{chapter, chapterPercent}` |
| `DELETE` | `/api/books/{id}/progress` | 清除自己的进度 |
| `POST` | `/api/books/{id}/read` | 累加阅读秒数 |
| `POST` / `DELETE` | `/api/books/{id}/bookmarks[/{bid}]` | 添加 / 删除书签 |
| `GET` | `/api/books/{id}/search?q=` | 整本书全文搜索（文字书） |
| `POST` | `/api/books/{id}/tts` | 合成一句朗读音频（文字书，返回 `audio/mpeg`，命中缓存时带 `X-Moyue-TTS-Cache: hit`） |
| `GET` | `/api/tts/status` | 当前语音朗读是否启用、可选音色 |
| `POST` | `/api/books/{id}/annotations` | 新建划线 / 笔记 |
| `PATCH` / `DELETE` | `/api/books/{id}/annotations/{aid}` | 修改 / 删除划线笔记 |
| `GET` | `/api/stats` | 当前用户的阅读统计 |
| `GET` / `POST` | `/api/admin/libraries` | 书库列表 / 新建 |
| `DELETE` | `/api/admin/libraries/{id}` | 删除书库记录（本地文件保留；WebDAV 缓存会删除） |
| `POST` | `/api/admin/libraries/{id}/scan` | 触发扫描 |
| `GET` / `POST` | `/api/admin/users` | 账号列表 / 新建 |
| `PATCH` / `DELETE` | `/api/admin/users/{id}` | 修改权限 / 删除账号 |
| `GET` / `PUT` | `/api/admin/settings/tts` | 读取 / 保存语音朗读配置（密钥只返回掩码） |
| `POST` | `/api/admin/settings/tts/test` | 用当前表单配置合成一句测试语音 |

`POST /api/admin/libraries` 的 `kind` 为 `local` 时使用 `path`；为 `webdav` 时使用 `url`、`username`、`password`。

## 目录结构

```text
.
├── .github/workflows/   # 发布流水线：测试、多架构镜像、GitHub Release
├── AGENTS.md            # 开发约定、实现要点与 Agent 提示
├── main.go
├── internal/
│   ├── auth/            # PBKDF2、会话 token、用户名/密码规则
│   ├── epub/            # EPUB 2/3 解析与资源读取
│   ├── txt/             # 编码识别、元数据、自动分章、HTML 渲染
│   ├── comic/           # CBZ（zip）与 CBR（rar）页面枚举与读取
│   ├── fb2/             # FictionBook 2 解析与 HTML 渲染
│   ├── kindle/          # MOBI / AZW3 / KF8 解析（MIT，见 LICENSE）
│   ├── store/           # SQLite 持久化（账号/书库/书籍/进度/书签/批注/会话/设置）
│   ├── tts/             # 小米 MiMo / OpenAI 兼容语音合成与本地缓存
│   ├── webdav/          # WebDAV 目录枚举与文件下载
│   └── api/             # 路由、权限、扫描、PDF/漫画渲染、语音代理
├── web/                 # 登录、书架、管理台、阅读器
│   ├── manifest.webmanifest  # PWA 清单
│   ├── sw.js            # Service Worker（仅缓存外壳）
│   ├── offline.html     # 离线说明页
│   ├── audioview.html   # 音频播放器页面
│   ├── pdfview.html     # PDF 阅读器（单页 / 双页）
│   └── assets/
│       ├── app.js / app.css / i18n.js
│       ├── icons/       # 192 / 512 / maskable / Apple 图标
│       └── pdfjs/       # PDF.js 运行时与字体
├── books/               # 默认书库 + 演示书
├── data/                # moyue.db（SQLite）与 tts/ 朗读缓存
└── docs/                # 关键截图
```

## 测试

```bash
go test ./...
```

覆盖 EPUB / TXT / CBZ / FB2 / MOBI 解析、密码哈希与会话、按账号隔离的进度 / 书签 / 批注 / 统计、设备会话撤销、SQLite 持久化与结构迁移、账号权限流程、全文搜索、CSRF Origin 校验、HTML 清洗与路径穿越防护、语音配置读写与 MiMo / OpenAI 接口解析、WebDAV 目录枚举与按需下载。浏览器端覆盖中/英/日/韩界面、移动端划线工具条、上传进度对话框、多种宽度下的单页 / 双页排版与朗读高亮。

开发约定、实现要点与 Agent 维护提示见 **[AGENTS.md](AGENTS.md)**。

## Docker 部署

```bash
docker build -t moyue .
docker run -d --name moyue -p 8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/books:/app/books" \
  -v /srv/comics:/media/comics:ro \
  moyue
```

- 基础镜像为 `alpine:latest`，容器内以 `root` 运行，挂载目录无需调整属主
- 额外的书库在管理台里填容器内路径，例如 `/media/comics`
- 镜像默认追加 `-trust-proxy`，适配 Nginx / Caddy 等反向代理；如果直接暴露 8080，请在启动命令末尾加 `-trust-proxy=false`，避免客户端伪造 `X-Forwarded-*`
- 反向代理需转发 `X-Forwarded-Proto`，否则 Cookie 不会带 `Secure`，真实 IP 也会变成代理地址
- PWA 安装与 Service Worker 需要 HTTPS（localhost 除外），公网部署请配置 TLS
- Nginx 反向代理需放宽上传体积：

```nginx
client_max_body_size 256m;
```

## 镜像发布

推送 `v*` 标签，或在 Actions 中手动触发 **Release** 工作流，会依次运行测试、构建多架构镜像并推送：

- `quay.io/<QUAY_USER>/moyue`
- `ghcr.io/<owner>/moyue`

镜像同时带有 `latest` 与版本号标签，支持 `linux/amd64`、`linux/arm64`。推送标签或手动触发时会生成 GitHub Release，附带各平台二进制包与自动生成的变更说明。

需要在仓库的 **Settings → Secrets and variables → Actions** 中配置：

| Secret | 说明 |
| --- | --- |
| `QUAY_USER` | Quay 用户名；留空则跳过 Quay 推送 |
| `QUAY_PASSWORD` | Quay 访问令牌 |

GHCR 使用内置的 `GITHUB_TOKEN`，无需额外配置；首次推送后可在包设置中调整可见性。

## 已知限制

- 不支持 DRM 加密书籍、加密 PDF 与加密 RAR
- PDF 为逐页渲染，双页模式只是并排显示相邻两页，没有跨页拼接；无文本选择/搜索/批注，不读取 PDF 内嵌目录
- PDF / 漫画 / 音频不支持全文搜索、划线与笔记；划线与笔记仅用于文字书
- 固定版式（fixed-layout）EPUB 按普通流式文档处理，效果可能不佳
- 漫画图片按屏幕等比缩放，超长条漫（webtoon）会偏小
- MOBI/AZW3 保留正文与主要图片，复杂 CSS、SVG flow 图片与部分脚注可能丢失
- 有声书按单个音频文件成书，不支持章节标记、多文件合辑与内嵌封面
- 四语界面覆盖主要界面与常见提示，书籍内容、书库名与部分服务端错误信息保持原文
- 语音朗读按 300–400 字分块请求，首次朗读每块有网络合成延迟；无网络时无法朗读，已缓存的块仍可播放
- 块内逐句高亮按音频时长与字数比例估算，语速或停顿变化时可能有一句以内的偏差
- 朗读仅支持文字书；PDF / 漫画 / 有声书没有朗读入口
- 语音设置中的 API Key 以明文保存在 SQLite，部署时请限制 `data/` 目录权限
- 权限按「书库」分配，不支持单本书级别的访问控制
- 没有 OAuth / LDAP / 公开注册，账号由管理员创建
- 从磁盘删除的文件不会自动清理记录；超大书库可手动触发扫描
- WebDAV 书库只缓存已打开书籍的正文；未打开的书在书架上只显示文件名、大小与类型，首次打开会有下载等待
- WebDAV 当前使用 Basic Auth，密码以明文保存在 SQLite；建议使用专用只读账号并启用 HTTPS
- SQLite 单文件存储，按行更新；按单机家庭 / 小团队规模设计，未做多实例共享写

演示用书籍《山间来信》《潮汐图书馆》《山海小记》《山与海 · 图像笔记》《纸上博物馆》《纸上四季》《夜航船》《海边夜航》均为项目自带原创内容，仅用于功能展示。内嵌的 MOBI/KF8 解析器源自 MIT 许可项目，见 `internal/kindle/LICENSE`。

## License

MIT（内嵌的 PDF.js 为 Apache-2.0，见 `web/assets/pdfjs/LICENSE`）
