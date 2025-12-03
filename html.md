# HTML 包使用指南

## 目录
- [html 包](#html-包)
  - [EscapeString](#escapestring)
  - [UnescapeString](#unescapestring)
  - [函数对比](#函数对比)
  - [不对称性说明](#不对称性说明)
- [html/template 包](#htmltemplate-包)
  - [包目的](#包目的)
  - [基本使用对比](#基本使用对比)
  - [上下文敏感转义](#上下文敏感转义)
  - [特殊属性处理](#特殊属性处理)
  - [不安全上下文处理](#不安全上下文处理)
  - [类型标记](#类型标记)
  - [安全模型](#安全模型)
- [安全用途](#安全用途)
- [自动转义流程图](#自动转义流程图)

---

## html 包

**导入路径**：`import "html"`

**作用**：提供 **HTML 转义**（escape）功能，防止 XSS 攻击。

### EscapeString

**函数签名**：`html.EscapeString(s string) string`

**作用**：将字符串中的 5 个 HTML 特殊字符转义成安全的实体形式。

**转义规则**：
| 原字符 | 转义后 |
|--------|--------|
| `<`    | `&lt;` |
| `>`    | `&gt;` |
| `&`    | `&amp;` |
| `'`    | `&#39;` |
| `"`    | `&#34;` |

**特点**：
- 只转义这五个常见的、可能影响 HTML 结构或导致 XSS 的字符
- 不会转义其他字符，比如 `á` 或 `©`

### UnescapeString

**函数签名**：`html.UnescapeString(s string) string`

**作用**：将转义的 HTML 字符实体还原成原始字符。

**支持类型**：
1. **命名实体**：
   - `&lt;` → `<`
   - `&gt;` → `>`
   - `&amp;` → `&`
   - `&aacute;` → `á`
   - `&copy;` → `©`

2. **数字实体**：
   - `&#225;` → `á`（十进制）
   - `&#xE1;` → `á`（十六进制）

### 函数对比

| 函数 | 转/解码范围 | 主要用途 |
|------|-------------|----------|
| `EscapeString` | `<, >, &, ', "` | 生成 HTML 安全输出，防止 XSS |
| `UnescapeString` | 所有 HTML 命名实体 + 数字实体 | 将 HTML 实体还原成原字符 |

### 不对称性说明

**为什么它们不完全对称？**

- `EscapeString` 只针对最常见、在 HTML 中需要立即处理的危险字符（防止 XSS）
- `UnescapeString` 则是"通用解码器"，会解码很多种合法的 HTML 实体

**验证**：
```go
html.UnescapeString(html.EscapeString(s)) == s   // ✅ 一定成立
html.EscapeString(html.UnescapeString(s)) == s   // ❌ 不一定成立
```

**原因**：假设 s 里包含 `&aacute;`，`UnescapeString` 会把它变成 `á`，但 `EscapeString` 不会把 `á` 转回 `&aacute;`，因为它只处理那 5 个字符。

---

## html/template 包

**导入路径**：`import "html/template"`

### 包目的

`html/template` 是 Go 官方提供的 **安全 HTML 模板引擎**，基于 `text/template` 增加了 **上下文敏感的自动转义**：

- **`text/template`**：仅模板解析执行，无 HTML 上下文转义
- **`html/template`**：增加 **上下文感知转义**，防止恶意数据注入（XSS 防御）

**核心目标**：模板作者可信，传入数据不可信，自动转义防止 XSS 攻击

### 基本使用对比

#### text/template（不安全）
```go
import "text/template"

t, _ := template.New("foo").Parse(`{{define "T"}}Hello, {{.}}!{{end}}`)
t.ExecuteTemplate(out, "T", "<script>alert('xss')</script>")
```
**输出**：`Hello, <script>alert('xss')</script>!` ❌ 存在 XSS 风险

#### html/template（安全）
```go
import "html/template"

t, _ := template.New("foo").Parse(`{{define "T"}}Hello, {{.}}!{{end}}`)
t.ExecuteTemplate(out, "T", "<script>alert('xss')</script>")
```
**输出**：`Hello, &lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;!` ✅ 自动转义

### 上下文敏感转义

`html/template` 识别多种上下文，自动选择合适转义方式：

| 上下文类型 | 转义方式 | 示例 |
|------------|----------|------|
| HTML 标签内容 | HTML 转义 | `<div>{{.}}</div>` |
| HTML 属性值 | 属性转义 | `<input value="{{.}}">` |
| URL 地址 | URL 编码 + 属性转义 | `<a href="/search?q={{.}}">` |
| JavaScript 代码 | JS 转义 | `<script>var x = {{.}};</script>` |
| CSS 样式 | CSS 转义 | `<style>body { color: {{.}}; }</style>` |

**示例**：
```html
<a href="/search?q={{.}}">{{.}}</a>
```
自动改写为：
```html
<a href="/search?q={{. | urlescaper | attrescaper}}">{{. | htmlescaper}}</a>
```

### 特殊属性处理

| 属性类型 | 处理规则 |
|----------|----------|
| 带 namespace（如 `my:href`） | 按普通属性处理 |
| data- 前缀（如 `data-href`） | 忽略 `data-` 前缀，按普通属性转义 |
| xmlns 前缀（如 `xmlns:title`） | 始终按 URL 处理 |

### 不安全上下文处理

当数据出现在不安全位置且协议不安全时（如 `javascript:`），输出特殊占位符：
```
#ZgotmplZ
```
表示该值被过滤，防止注入攻击

### 类型标记（Typed Strings）

默认所有数据按普通文本转义，明确标记安全类型可避免重复转义：

```go
// 安全 HTML，不转义
template.HTML("<b>World</b>")

// 安全 JavaScript
template.JS("console.log('hello')")

// 安全 URL
template.URL("https://example.com")
```

**示例**：
```go
tmpl.Execute(out, template.HTML("<b>World</b>"))
```
**输出**：`Hello, <b>World</b>!` ✅ 原样输出，不转义

### 安全模型

基于 Mike Samuel 的安全模板定义，具备三大安全属性：

| 安全属性 | 说明 |
|----------|------|
| **结构保持性** | 不可信数据不会破坏 HTML/JS/CSS 结构边界 |
| **代码效果属性** | 不引入模板作者未写的可执行代码 |
| **最小惊讶原则** | 开发者可直观理解 `{{.}}` 的转义行为 |

**特殊说明**：ES6 模板字符串（`` `...${...}` ``）默认禁用模板插值，可通过 `GODEBUG=jstmpllitinterp=1` 启用

---

## 安全用途

- **防止 XSS 攻击**（跨站脚本攻击）
- 在输出到 HTML 页面时避免 HTML 标签被浏览器解析成实际标签
- 适合处理用户输入的文本

---

## 自动转义流程图

### 转义流程

```
模板解析 → 上下文分析 → 选择转义器 → 协议检查 → 输出结果
```

### 详细流程图

```
┌─────────────────┐
│   模板解析       │ 基于 text/template 语法 + HTML 上下文分析
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   上下文分析     │ 判断 {{.}} 位置：HTML/属性/URL/JS/CSS
└────────┬────────┘
         │
    ┌────┼────┐
    ▼    ▼    ▼
┌──────┐ ┌──────┐ ┌──────┐
│HTML  │ │属性  │ │URL   │ 根据不同上下文
│内容  │ │值    │ │地址  │
└──────┘ └──────┘ └──────┘
    │      │      │
    ▼      ▼      ▼
┌──────┐ ┌──────┐ ┌──────┐
│HTML  │ │属性  │ │URL   │ 选择对应转义器
│转义  │ │转义  │ │编码  │
└──────┘ └──────┘ └──────┘
                   │
                   ▼
            ┌──────────────┐
            │ 协议安全检查 │ javascript: → #ZgotmplZ
            └──────────────┘
```

### 转义器类型

| 转义器 | 用途 | 处理字符 |
|--------|------|----------|
| `htmlescaper` | HTML 内容转义 | `<`, `>`, `&` 等 |
| `attrescaper` | HTML 属性转义 | 属性值特殊字符 |
| `urlescaper` | URL 编码 | 百分号编码 |
| `jsescaper` | JavaScript 转义 | 引号、反斜杠等 |
| `cssescaper` | CSS 样式转义 | CSS 特殊字符 |

---

✅ **核心理念**：模板结构保持完整，用户数据自动正确转义，防止 XSS 攻击