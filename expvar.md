## 1. `expvar` 包的用途

**核心功能**：  
`expvar` 用来在运行中的 Go 程序中**暴露内部状态变量**（例如计数器、统计信息、命令行参数、内存使用情况等），并通过 HTTP 以 JSON 格式访问这些变量。常用于服务的**运行时监控**和**调试**。

**默认行为**：
- 当你 `import _ "expvar"`，它会注册一个 HTTP handler，在 `/debug/vars` 路径返回所有已注册的变量。
- 同时默认注册两个变量：
  - `cmdline`：记录 `os.Args`（启动命令行参数）
  - `memstats`：记录 `runtime.MemStats`（Go 运行时内存统计信息）

---

## 2. 工作原理

### 变量注册
`expvar` 提供了几种变量类型：
- **Int**：原子整型计数器
- **Float**：原子浮点计数器
- **String**：字符串变量
- **Map**：并发安全的 map（键值是 `expvar.Var`）

你可以通过 `expvar.NewInt("name")` 或 `expvar.Publish("name", var)` 来注册变量。

被注册的变量会存储在一个全局 map 中。

---

### HTTP 暴露
`expvar` 在 `init()` 中自动调用：

```go
http.Handle("/debug/vars", expvar.Handler())
```

这样，访问 `http://localhost:8080/debug/vars` 时，会返回所有注册变量的 JSON：

```json
{
    "cmdline": ["./myserver", "-port=8080"],
    "memstats": { ... Go 内存统计 ... },
    "myCounter": 42,
    "myMap": {"foo": 1, "bar": 2}
}
```

---

## 3. 使用场景

- **服务运行状态监控**：
  直接用浏览器、`curl` 或监控系统抓取 `/debug/vars` 数据，分析 QPS、错误率、内存使用等。
  
- **调试**：
  在开发或测试环境快速查看程序内部状态，而无需额外打印日志。

- **与监控系统集成**：
  例如 Prometheus 可以通过 HTTP 抓取这些 JSON，然后转换成监控指标。

---

## 4. 简单示例

```go
package main

import (
    "expvar"
    "net/http"
)

var requests = expvar.NewInt("requests")

func handler(w http.ResponseWriter, r *http.Request) {
    requests.Add(1)
    w.Write([]byte("Hello, World!"))
}

func main() {
    http.HandleFunc("/", handler)
    // expvar 在 init() 时已经注册了 /debug/vars
    http.ListenAndServe(":8080", nil)
}
```

运行后：
- 访问 `http://localhost:8080/` 会增加计数
- 访问 `http://localhost:8080/debug/vars` 会看到：
```json
{
    "cmdline": ["./main"],
    "memstats": {...},
    "requests": 3
}
```

---

✅ **总结**：
- `expvar` 是 Go 自带的轻量级运行时监控工具，主要通过 `/debug/vars` 暴露 JSON 格式的内部变量。
- 适合做简单的服务统计、调试和基础监控。
- 默认会暴露命令行参数和内存信息，用户可以自行添加更多变量。