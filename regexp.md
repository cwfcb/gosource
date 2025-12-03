# Go regexp 包参考

本文档分为五个部分，详细介绍了 Go `regexp` 包的使用：
1.  **Regexp 介绍与方法总结**：涵盖 `regexp` 的设计原理、完整的核心方法、使用示例和注意事项。
2.  **Expand 方法**：深入解析如何使用模板和捕获组来构建新字符串。
3.  **QuoteMeta 方法**：介绍如何安全地在正则表达式中使用字面量字符串。
4.  **Split 方法**：讲解如何使用正则表达式来分割字符串。
5.  **Syntax 详解**：详细解析 `regexp/syntax` 包定义的正则表达式语法。

---

## 一、Regexp 介绍与方法总结

### 1. 设计原理

- **语法标准**
  - Go 的 `regexp` 基于 **RE2** 正则引擎（Google 开源的高性能正则实现），语法与 Perl/Python 相似，但不支持 `\\C`。
  - 语法规范参考：[RE2 Syntax](https://golang.org/s/re2syntax)。

- **性能保障**
  - 与传统的 backtracking（回溯）型引擎不同，`regexp` 保证 **线性时间复杂度**（O(n)），避免了“正则灾难”（Regex DoS）。
  - 原理是使用自动机（NFA/DFA）进行匹配，不会陷入指数级回溯。

- **UTF-8 支持**
  - 所有字符按 UTF-8 编码处理。
  - 如果遇到非法 UTF-8 序列，会当作 `utf8.RuneError`（U+FFFD）处理。

### 2. Go `regexp` 包方法速查表

#### 2.1. 核心命名规则：`Find(All)?(String)?(Submatch)?(Index)?`

`regexp` 包提供了 16 个核心的匹配方法，它们的名称都遵循统一的模式，理解这个模式是掌握 `regexp` 的关键。

- **`Find`**: 这是基本名称。所有这些函数都用于查找正则表达式的匹配项。
- **`All`**: 如果名称中包含 `All` (例如 `FindAllString`)，函数会连续查找所有不重叠的匹配项。如果名称中不含 `All` (例如 `FindString`)，则只查找第一个匹配项。
- **`String`**: 如果名称中包含 `String` (例如 `FindString`)，则输入参数为字符串 `string`。如果不含，则输入参数为字节切片 `[]byte`。
- **`Submatch`**: 如果名称中包含 `Submatch` (例如 `FindStringSubmatch`)，函数的返回值不仅会包含完整的匹配项，还会包含所有捕获组（即正则表达式中用括号 `()` 包裹的子表达式）的匹配项。如果不含，则只返回完整的匹配项。
- **`Index`**: 如果名称中包含 `Index` (例如 `FindStringIndex`)，函数会返回匹配项在输入字符串中的起始和结束字节索引，而不是匹配的文本本身。

#### 2.2. 方法详解

**1. 编译和初始化 (Compilation and Initialization)**

在使用正则表达式进行任何操作之前，必须先将正则表达式的字符串“编译”成一个 `Regexp` 对象。

| 方法/函数 | 功能 | 返回值类型 | 说明 |
|---|---|---|---|
| `Compile(expr)` | 编译一个正则表达式 | `(*Regexp, error)` | 最常用的编译函数。如果表达式无效，返回 `error`。 |
| `MustCompile(expr)` | 编译一个正则表达式，但如果失败则会 `panic` | `*Regexp` | 用于在全局变量中安全地初始化已知的有效正则表达式，简化错误处理。 |
| `CompilePOSIX(expr)` | 以 POSIX ERE 语法编译表达式，并使用最左最长匹配 | `(*Regexp, error)` | 用于需要 POSIX 兼容性的场景。 |
| `MustCompilePOSIX(expr)` | `CompilePOSIX` 的 `panic` 版本 | `*Regexp` | 用于在全局变量中安全地初始化 POSIX 正则表达式。 |

**2. 匹配检查 (Match Checking)**

这类函数只关心输入中是否存在匹配，返回一个布尔值，是性能最高的检查方式。

| 方法 | 功能 | 返回值类型 | 说明 |
|---|---|---|---|
| `Match(b []byte)` | 检查字节切片 `b` 中是否包含任何匹配项 | `bool` | 只返回是否存在匹配，不关心匹配内容和位置。 |
| `MatchString(s string)` | 检查字符串 `s` 中是否包含任何匹配项 | `bool` | `Match` 的字符串版本，最为常用。 |
| `MatchReader(r io.RuneReader)` | 检查 `io.RuneReader` 中是否包含任何匹配项 | `bool` | 用于处理流数据，例如文件。 |

**3. 查找与提取 (Finding and Extracting)**

这是 `regexp` 包中最核心和强大的功能。所有这些函数都遵循统一的命名模式。

**命名模式解析：**
- **`Find`**：只查找第一个匹配项。
- **`All`**：查找所有不重叠的匹配项。它接受一个整数 `n` 作为额外参数：
  - `n < 0`：查找所有匹配项
  - `n >= 0`：最多查找 `n` 个匹配项
- **`String`**：输入是 `string`，返回值也是 `string` 或与 `string` 相关的类型。如果无此后缀，则输入和输出都是 `[]byte`。
- **`Submatch`**：不仅返回整个匹配，还返回所有捕获组（即正则表达式中用括号 `()` 包裹的子匹配）的内容。如果无此后缀，则只返回完整匹配。
- **`Index`**：返回匹配项的字节索引（`[start, end]` 对），而不是匹配的文本内容。如果无此后缀，则返回文本内容。

**常用方法示例（以 String 版本为例）**

| 方法 | 功能 | 返回值类型 |
|---|---|---|
| `FindString(s)` | 在字符串 `s` 中查找第一个匹配的子串 | `string` |
| `FindAllString(s, n)` | 在字符串 `s` 中查找最多 `n` 个匹配的子串 | `[]string` |
| `FindStringSubmatch(s)` | 在字符串 `s` 中查找第一个匹配及其所有捕获组 | `[]string` |
| `FindAllStringSubmatch(s, n)` | 在字符串 `s` 中查找最多 `n` 个匹配及其所有捕获组 | `[][]string` |
| `FindStringIndex(s)` | 在字符串 `s` 中查找第一个匹配的起始和结束索引 | `[]int` |
| `FindAllStringSubmatchIndex(s, n)` | 在字符串 `s` 中查找最多 `n` 个匹配及其捕获组的索引 | `[][]int` |

**4. 替换 (Replacing)**

这类函数用于查找匹配项并将其替换为新的内容。

| 方法 | 功能 | 返回值类型 | 说明 |
|---|---|---|---|
| `ReplaceAllString(src, repl)` | 将 `src` 中所有匹配项替换为 `repl` | `string` | `repl` 中的 `$` 符号会被 `Expand` 规则解释，用于引用捕获组（如 `$1`, `${name}`）。 |
| `ReplaceAllLiteralString(src, repl)` | 将 `src` 中所有匹配项替换为 `repl` | `string` | `repl` 被作为**纯文本**直接替换，`$` 没有特殊含义。 |
| `ReplaceAllStringFunc(src, repl)` | 使用函数 `repl` 的返回值替换 `src` 中所有匹配项 | `string` | `repl` 函数的入参是匹配到的子串，其返回值被直接替换。 |
| `ExpandString(dst, template, src, match)` | 根据模板 `template` 和 `FindStringSubmatchIndex` 的结果 `match` 生成新字符串 | `[]byte` | 高级替换功能，用于构建复杂的输出字符串。 |

**5. 分割 (Splitting)**

| 方法 | 功能 | 返回值类型 | 说明 |
|---|---|---|---|
| `Split(s string, n int)` | 使用正则表达式的匹配项作为分隔符来切割字符串 `s` | `[]string` | `n` 控制切割的片段数：`n < 0` 表示全部分割；`n > 0` 表示最多 `n` 个片段，最后一个片段包含剩余所有内容。 |

**6. 其他工具 (Other Utilities)**

| 方法 | 功能 | 返回值类型 | 说明 |
|---|---|---|---|
| `NumSubexp()` | 返回正则表达式中捕获组的数量 | `int` | |
| `SubexpNames()` | 返回所有捕获组的名称 | `[]string` | 索引 `i` 对应第 `i` 个捕获组的名称。`names[0]` 总是空字符串。对于匿名捕获组，其名称也是空字符串。 |
| `String()` | 返回编译该 `Regexp` 对象的原始正则表达式字符串 | `string` | |
| `LiteralPrefix()` | 返回一个所有匹配项都必须拥有的公共前缀字符串 | `(prefix string, complete bool)` | `complete` 为 `true` 表示整个正则表达式就是一个字面量字符串，可用于快速失败优化。 |

### 3. 使用方式示例

```go
package main

import (
    "fmt"
    "regexp"
)

func main() {
    // 编译正则表达式
    re := regexp.MustCompile(`(\w+)@(\w+\.\w+)`)

    // 1. 检查匹配
    fmt.Println(re.MatchString("test@example.com")) // true

    // 2. 获取第一个匹配
    fmt.Println(re.FindString("Contact me: test@example.com hello")) // "test@example.com"

    // 3. 获取匹配的索引位置
    fmt.Println(re.FindStringIndex("Contact me: test@example.com hello")) // [12 29]

    // 4. 获取捕获组
    fmt.Println(re.FindStringSubmatch("Contact me: test@example.com hello"))
    // ["test@example.com", "test", "example.com"]

    // 5. 获取多个匹配
    text := "a@b.com c@d.net e@f.org"
    fmt.Println(re.FindAllString(text, -1))
    // ["a@b.com", "c@d.net", "e@f.org"]

    // 6. 获取多个匹配及子匹配
    fmt.Println(re.FindAllStringSubmatch(text, -1))
    // [
    //   ["a@b.com", "a", "b.com"],
    //   ["c@d.net", "c", "d.net"],
    //   ["e@f.org", "e", "f.org"]
    // ]
}
```

### 4. 注意事项

1.  **编译正则表达式**
    -   使用 `regexp.Compile(pattern)` 会返回 `(Regexp, error)`，如果正则不合法会报错。
    -   使用 `regexp.MustCompile(pattern)` 会 panic，适合常量模式。

2.  **性能**
    -   RE2 保证线性时间，但复杂正则仍然可能消耗较多 CPU，尤其在大文本中多次匹配时。
    -   对高频调用场景，**提前编译**正则，避免多次解析模式。

3.  **捕获组与索引**
    -   `Submatch` 方法返回第0个为整个匹配，后面依次是每个括号捕获。
    -   `Index` 方法返回的是字节索引，不是 rune 索引，处理多字节字符时要小心。

4.  **输入类型**
    -   `String` 方法适合 `string` 类型，非 `String` 方法适合 `[]byte`，避免不必要的类型转换。

5.  **空匹配**
    -   如果模式可以匹配空字符串（如 `.*`），`FindAll` 会忽略与上一个匹配相邻的空匹配。

6.  **RuneReader 支持**
    -   `MatchReader`、`FindReaderIndex`、`FindReaderSubmatchIndex` 适合从流式输入中匹配（如文件、网络流），但可能会读取超出匹配范围的内容。

### 5. 总结建议

-   对**固定模式**，用 `MustCompile` 并复用 `Regexp` 对象；
-   优先使用 **精确模式**，避免过度贪婪；
-   注意 `Index` 返回的是**字节索引**；
-   如果处理大文件或流，考虑 RuneReader 方法；
-   不要在高频场景下反复 `Compile`，会浪费性能。

---

## 二、Expand 方法

`Expand` 是一个基于正则表达式匹配结果的模板替换函数。当您从一个字符串中成功匹配并捕获了多个部分（“子匹配”或“捕获组”）后，`Expand` 可以让您按照一个预先定义好的“模板”，将这些捕获到的部分重新组合，从而生成一个全新的字符串。

### 1. 作用与应用场景

`Expand` 主要用于字符串的重新格式化和内容提取转换的场景。

-   **日期/时间格式转换**：从 `2023-10-27` 格式中提取年、月、日，重组成 `10/27/2023`。
-   **日志信息重构**：从日志行 `[ERROR] user 'admin' failed to login` 中提取关键信息，生成告警 `Warning: User 'admin' login failed.`。
-   **数据迁移和转换**：将自定义的文本格式转换为 Markdown 或 HTML。

### 2. 使用注意事项

1.  **输入源必须是 FindSubmatchIndex 的结果**
    -   `Expand` 的 `match` 参数需要的是匹配项的**字节索引切片**（`FindSubmatchIndex` 的结果），而不是字符串切片。

2.  **模板变量语法**
    -   **数字引用**：`$1`, `$2` 代表第 1、2 个捕获组。`$0` 代表完整匹配。
    -   **命名引用**：在正则中使用 `(?P<name>...)` 命名捕获组，在模板中用 `${name}` 引用。**强烈推荐使用花括号 `${name}`**。

3.  **消除歧义**
    -   当变量名后紧跟其他字符时，务必使用花括号 `{}`。例如，`${1}0` 表示第一个捕获组后跟一个字符 `0`，而 `$10` 会被误解为第十个捕获组。

4.  **字面量 `$` 符号**
    -   要在结果中包含 `$` 字符本身，需在模板中使用 `$$` 进行转义。

5.  **未匹配的捕获组**
    -   如果一个可选的捕获组（如 `(...)?`）没有匹配到内容，它在模板中会被替换为空字符串。

6.  **目标缓冲区 `dst`**
    -   `Expand` 会将结果**追加**到 `dst` 字节切片后面。传入 `nil` 会让函数自动创建新切片。

### 3. `(?P<name>...)` 命名捕获组

在 `(?P<year>\\d{4})` 这个语法中，`P` 的意思是 **命名捕获组**（Named Capturing Group）。

-   **语法结构**：`(?P<name>...)` 捕获 `...` 匹配的文本，并将其命名为 `name`。
    -   `(...)`: 这是一个标准的捕获组 (Capturing Group)。它会捕获括号内表达式匹配到的文本。通常，我们通过数字（`$1`, `$2`, ...）来引用这些捕获组，顺序从左到右。
    -   `?:`: 当 `?` 紧跟在开括号 `(` 后面时，它表示这是一个特殊的组，而不是一个普通的捕获组。
    -   `P<name>`: 这是 `?` 后面紧跟的指令。`P` 告诉正则表达式引擎，这是一个命名捕获组，而 `<name>` (在您的例子中是 `<year>`) 就是您为这个捕获组指定的名字。
    -   所以，`(?P<year>\d{4})` 的完整含义是： “捕获四个数字 `\d{4}`，并将这个捕获组命名为 `year`。”
-   **优势**：
    1.  **可读性**：使用 `${year}` 比 `$1` 更清晰易懂，尤其在复杂正则中。
    2.  **健壮性**：修改正则表达式（如增删捕获组）时，数字编号会变，但命名引用保持不变，代码更可靠。

### 4. 代码示例

下面是一个综合性的例子，演示了如何使用 `Expand` 将日期格式从 `YYYY-MM-DD` 转换为 `MM/DD/YYYY`。

```go
package main

import (
        "fmt"
        "regexp"
)

func main() {
        // 正则表达式，使用命名捕获组 'year' 和常规数字捕获组
        re := regexp.MustCompile(`(?P<year>\d{4})-(\d{2})-(\d{2})`)
        src := []byte("File created on 2023-10-27.")

        // 1. 获取匹配项的索引，这是 Expand 的关键输入
        matchIndexes := re.FindSubmatchIndex(src)
        // matchIndexes 的内容会是类似 [16 26 16 20 21 23 24 26] 这样的索引

        // 2. 定义模板
        // - $2 代表第2个捕获组 (月份)
        // - $3 代表第3个捕获组 (日期)
        // - ${year} 代表名为 'year' 的捕获组
        template := []byte("US Date: $2/$3/${year}")

        // 3. 调用 Expand
        // 传入 nil 作为第一个参数，让函数为我们创建新的结果切片
        result := re.Expand(nil, template, src, matchIndexes)

        fmt.Println(string(result))
        // 输出: US Date: 10/27/2023

        // --- 演示 $$ 转义 ---
        // 假设我们要生成一个带价格的字符串
        priceTemplate := []byte("Price: $$${2}.${3}") // 模板意为 "Price: $MM.DD"

        priceResult := re.Expand(nil, priceTemplate, src, matchIndexes)
        fmt.Println(string(priceResult))
        // 输出: Price: $10.27
}
```

---

## 三、QuoteMeta 方法

### 1. 作用

`QuoteMeta` 函数会**对传入的字符串中所有正则元字符（metacharacters）进行转义**，返回的新字符串可以直接安全地放入正则表达式中，用于匹配原始字符串的**字面值**。

-   **方法签名**：`func QuoteMeta(s string) string`

### 2. 背景

在正则表达式中，`. ^ $ * + ? ( ) [ ] { } | \\` 等字符具有特殊含义。如果要匹配它们的字面值，必须进行转义。`QuoteMeta` 自动处理了这个过程。

### 3. 示例

假设需要匹配字符串 `a+b*c` 的字面值。

```go
package main

import (
    "fmt"
    "regexp"
)

func main() {
    text := "a+b*c"
    safe := regexp.QuoteMeta(text)
    fmt.Println(safe) // 输出: a\+b\*c

    re := regexp.MustCompile(safe)
    fmt.Println(re.MatchString("a+b*c")) // true
    fmt.Println(re.MatchString("aaabbbccc")) // false
}
```
`QuoteMeta` 返回 `a\\+b\\*c`，确保 `+` 和 `*` 被当作普通字符处理。

### 4. 典型使用场景

1.  **用户输入安全**：防止用户输入的内容被当作正则语法解析，避免注入。
2.  **匹配固定文本**：当要匹配的文本本身可能包含元字符时。
3.  **搜索功能**：将用户输入的搜索关键字安全地嵌入到更复杂的正则表达式中。

### 5. 小结

-   **功能**：转义所有正则元字符，使其失去特殊意义。
-   **结果**：返回一个能匹配原始文本字面含义的正则表达式字符串。
-   **安全性**：避免用户输入或动态内容破坏正则结构。

---

## 四、Split 方法

### 1. 作用

`Split` 方法使用正则表达式的匹配项作为分隔符来切割字符串。它比 `strings.Split` 更灵活，因为分隔符可以是复杂的模式。

### 2. 方法签名

```go
func (re *Regexp) Split(s string, n int) []string
```

-   **`s`**: 原始字符串。
-   **`n`**: 控制返回的子串数量：
    -   `n > 0`：最多返回 `n` 个子串，最后一个是剩余未分割的部分。
    -   `n == 0`：返回 `nil`。
    -   `n < 0`：返回所有子串（无限制）。

### 3. 工作原理

-   正则表达式匹配到的部分会被**丢掉**，作为分隔符。
-   返回的是匹配项之间的内容。
-   如果字符串以分隔符开头，则结果的第一个元素是空字符串。

### 4. 示例

**例 1：按逗号和分号分割**
```go
re := regexp.MustCompile(`[;,]`)
parts := re.Split("apple;banana,orange;pear", -1)
fmt.Println(parts) // [apple banana orange pear]
```

**例 2：按连续的空白符分割**
```go
re := regexp.MustCompile(`\s+`)
parts := re.Split("Go   is   fun", -1)
fmt.Println(parts) // [Go is fun]
```

**例 3：限制分割次数**
```go
re := regexp.MustCompile(`\s+`)
parts := re.Split("Go is very fun", 2)
fmt.Println(parts) // [Go is very fun]  // 最多两段
```

**官方示例分析**
```go
s := regexp.MustCompile("a*").Split("abaabaccadaaae", 5)
// s: ["", "b", "b", "c", "cadaaae"]
```
分析：
-   正则 `a*` 匹配零个或多个 `a` 作为分隔符。
-   `abaabaccadaaae` 被 `a`、`a`、`aa` 分割，得到 `""`、`"b"`、`"b"`、`"c"`。
-   因为 `n=5`，分割 4 次后，剩余的 `"cadaaae"` 作为最后一段。

### 5. 总结

-   **功能**：用正则匹配的内容作为分隔符，返回分隔符之外的子串。
-   **注意**：
    -   匹配到的分隔符不会出现在结果中。
    -   如果开头就匹配到分隔符，第一段是空字符串。
    -   `n` 控制返回的子串数量，负数表示无限制。

---

## 五、Syntax 详解

`regexp/syntax` 包的文档是 Go 语言正则表达式语法的“圣经”。下面我们逐一解析其中定义的所有语法组件，并附上可以直接运行的 Go 代码示例。

### 1. Single characters (单个字符)

这些是构成正则表达式的最基本元素，用于匹配单个字符。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `.` | 匹配任意单个字符，但默认不匹配换行符 `\n`。 | `fmt.Println(regexp.MatchString(".", "a"))` |
| `[xyz]` | 字符集：匹配方括号中列出的任意一个字符。 | `fmt.Println(regexp.MatchString("gr[ae]y", "grey"))` |
| `[^xyz]` | 反向字符集：匹配任意一个未在方括号中列出的字符。 | `fmt.Println(regexp.MatchString("gr[^ae]y", "gruy"))` |
| `\d` | 匹配一个数字 (等同于 `[0-9]`)。 | `fmt.Println(regexp.MatchString("\\d", "page-5"))` |
| `\D` | 匹配一个非数字 (等同于 `[^0-9]`)。 | `fmt.Println(regexp.MatchString("\\D", "5.0"))` |
| `[[:alpha:]]` | 匹配一个 ASCII 字母 (等同于 `[a-zA-Z]`)。 | `fmt.Println(regexp.MatchString("[[:alpha:]]", "User1"))` |
| `[[:^alpha:]]` | 匹配一个 非 ASCII 字母。 | `fmt.Println(regexp.MatchString("[[:^alpha:]]", "User1"))` |
| `\p{Greek}` | Unicode 字符类：匹配指定 Unicode 属性或脚本的字符。 | `fmt.Println(regexp.MatchString("\\p{Greek}", "αβγ"))` |
| `\P{Greek}` | 反向 Unicode 字符类：匹配非指定 Unicode 属性的字符。 | `fmt.Println(regexp.MatchString("\\P{Greek}", "abc"))` |

### 2. Composites (组合)

将简单的部分组合成更复杂的模式。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `xy` | 序列：x 后面必须紧跟着 y。 | `fmt.Println(regexp.MatchString("go lang", "go language"))` |
| `x\|y` | 选择：匹配 x 或者 y。引擎会优先尝试匹配 x。 | `fmt.Println(regexp.MatchString("cat|dog", "cat"))` |

### 3. Repetitions (重复)

定义一个模式可以连续出现多少次。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `x*` | 零次或多次 (贪婪模式)。 | `re := regexp.MustCompile("a*"); fmt.Println(re.FindString("baaacon"))` |
| `x+` | 一次或多次 (贪婪模式)。 | `re := regexp.MustCompile("a+"); fmt.Println(re.FindString("baaacon"))` |
| `x?` | 零次或一次 (贪婪模式)。 | `re := regexp.MustCompile("colou?r"); fmt.Println(re.MatchString("color"))` |
| `{n,m}` | n 到 m 次 (贪婪模式)。 | `re := regexp.MustCompile("a{2,3}"); fmt.Println(re.FindString("baaaac"))` |
| `{n,}` | 至少 n 次 (贪婪模式)。 | `re := regexp.MustCompile("a{2,}"); fmt.Println(re.FindString("baaaac"))` |
| `{n}` | 恰好 n 次。 | `re := regexp.MustCompile("a{3}"); fmt.Println(re.FindString("baaaac"))` |
| `x*?` | 零次或多次 (非贪婪模式)。 | `re := regexp.MustCompile("a*?"); fmt.Println(re.FindString("baaacon"))` |

**实现限制**: `{n,m}` 这种计数形式，其最小或最大重复次数不能超过 1000。`*` 或 `+` 等无限重复量词则不受此限制。

### 4. Grouping (分组)

用于将多个部分视为一个整体，并可以捕获匹配到的内容。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `(re)` | 捕获组：将 `re` 作为一个整体，并“捕获”这部分匹配到的内容。 | `re := regexp.MustCompile("(\\w+)-(\\d+)"); fmt.Println(re.FindStringSubmatch("file-101"))` |
| `(?P<name>re)` | 命名捕获组：与捕获组功能相同，但为它分配一个名字 `name`。 | `re := regexp.MustCompile("(?P<word>\\w+)-(?P<num>\\d+)"); fmt.Println(re.FindStringSubmatch("file-101"))` |
| `(?:re)` | 非捕获组：只用于分组，不创建子匹配项。 | `re := regexp.MustCompile("(?:go)lang"); fmt.Println(re.FindString("golang"))` |
| `(?flags)` | 设置标志（非捕获）。 | `re := regexp.MustCompile("(?i)go"); fmt.Println(re.MatchString("Go"))` |

### 5. Flags (标志)

标志可以改变正则表达式的默认行为。

| 标志 | 名称 | 说明 | Go 示例 |
|---|---|---|---|
| `i` | 不区分大小写 | 使匹配忽略字母的大小写。 | `re := regexp.MustCompile("(?i)go"); fmt.Println(re.MatchString("Go"))` |
| `m` | 多行模式 | 使 `^` 和 `$` 匹配行首和行尾。 | `re := regexp.MustCompile("(?m)^line2$"); fmt.Println(re.MatchString("line1\\nline2"))` |
| `s` | 点号匹配换行符 | 使 `.` 元字符可以匹配 `\n`。 | `re := regexp.MustCompile("(?s)a.b"); fmt.Println(re.MatchString("a\\nb"))` |
| `U` | 全局非贪婪 | 交换所有量词的贪婪性：`*` 变 `*?`，`*?` 变 `*`。 | `re := regexp.MustCompile("(?U)a+"); fmt.Println(re.FindString("aaab"))` |

### 6. Empty strings (空字符串/位置锚定)

这些元字符不匹配任何字符，而是匹配文本中的特定“位置”。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `^` | 匹配文本或行的开头（受 `m` 标志影响）。 | `fmt.Println(regexp.MatchString("^go", "go lang"))` |
| `$` | 匹配文本或行的结尾（受 `m` 标志影响）。 | `fmt.Println(regexp.MatchString("lang$", "go lang"))` |
| `\A` | 只匹配整个文本的开头。 | `fmt.Println(regexp.MatchString("\\Ago", "go\\nlang"))` |
| `\z` | 只匹配整个文本的结尾。 | `fmt.Println(regexp.MatchString("lang\\z", "go\\nlang"))` |
| `\b` | 单词边界：`\w` 和 `\W` 之间的位置。 | `re := regexp.MustCompile("\\bcat\\b"); fmt.Println(re.MatchString("a cat!"))` |
| `\B` | 非单词边界。 | `re := regexp.MustCompile("\\Bcat\\B"); fmt.Println(re.MatchString("tomcat"))` |

### 7. Escape sequences (转义序列)

用于表示特殊字符或字面值。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `\t`, `\n`, `\r` | 匹配制表符、换行符、回车符等。 | `fmt.Println(regexp.MatchString("a\\tb", "a\tb"))` |
| `\*`, `\.`, `\+` | 转义元字符，使其被当作普通字符对待。 | `fmt.Println(regexp.MatchString("c\\*", "a+b=c*"))` |
| `\123` | 八进制字符码（最多三位）。 | `fmt.Println(regexp.MatchString("\\141", "a"))` |
| `\x7F` | 十六进制字符码（两位）。 | `fmt.Println(regexp.MatchString("\\x61", "a"))` |
| `\Q...\E` | 将 `...` 之间的所有内容都视为普通文本。 | `re := regexp.MustCompile("\\Q(.*\\w+)\\E"); fmt.Println(re.MatchString("(.*\\w+)"))` |

### 8. Character Class Elements (字符类内部元素)

定义了在 `[...]` 内部可以使用的元素。

| 语法 | 说明 | Go 示例 |
|---|---|---|
| `x` | 单个字符。 | `[abc]` |
| `A-Z` | 字符范围。 | `[a-z0-9]` |
| `\d` | Perl 字符类。 | `[\d\s]` |
| `[:foo:]` | ASCII 字符类。 | `[[:punct:]a-z]` |
| `\p{Foo}` | Unicode 字符类。 | `[\p{Han}\p{Latin}]` |

### 9. Perl, ASCII, Unicode 字符类

Go 支持丰富的预定义字符类，极大地方便了模式书写。

| 类别 | 语法 | 说明 |
|---|---|---|
| Perl | `\d` / `\D` | 数字 / 非数字 |
| Perl | `\s` / `\S` | 空白字符 / 非空白字符 |
| Perl | `\w` / `\W` | 单词字符 (`[0-9A-Za-z_]`) / 非单词字符 |
| ASCII | `[[:alnum:]]` | 字母和数字 |
| ASCII | `[[:punct:]]` | 标点符号 |
| ASCII | `[[:space:]]` | 所有空白字符 |
| Unicode | `\p{L}` | 任何语言的字母 (Letter) |
| Unicode | `\p{N}` | 任何语言的数字 (Number) |
| Unicode | `\p{Han}` | 汉字 |
| Unicode | `\p{Hiragana}` | 日语平假名 |
