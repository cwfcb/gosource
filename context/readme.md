---

## 1. 设计目的

Go 的 `context` 包是为了解决 **跨 API 边界的取消信号、截止时间和共享数据传递** 问题而设计的。

主要目的：
- **统一取消机制**：在多个 goroutine 或函数调用链中，能够优雅地通知下游停止工作。
- **超时控制**：给请求或任务设定截止时间或超时限制，过时即自动取消。
- **跨进程/跨 API 传递与请求相关的值**：比如 trace ID、用户信息等。
- **链式传递**：一个 context 可以派生出子 context，取消父 context 会自动取消所有子 context。

目标是：
> **在 Go 程序中形成一种标准化的方式来管理生命周期、超时和请求范围数据，避免资源泄漏和手动管理复杂的取消逻辑。**

---

## 2. 主要使用场景

官方推荐的典型场景：

### （1）服务器处理请求
- 每个请求都创建一个 `Context`，并在调用链中传递。
- 如果客户端断开连接或超时，整个调用链都能收到取消信号。

### （2）超时控制
- 调用外部服务或执行耗时操作时，用 `WithTimeout` 或 `WithDeadline` 控制最长执行时间。

### （3）批量任务取消
- 启动多个 goroutine 执行任务，当其中一个失败时，取消整个任务组。

### （4）传递请求范围的元数据
- 比如：用户信息、认证 token、trace ID 等。
- 用 `WithValue` 存储，不用来传递可选参数或业务配置，防止滥用。

---

## 3. 核心 API

源码中实现的几个关键函数：

| 函数 | 功能 |
|------|------|
| `Background()` | 返回一个永不取消、无值、无截止时间的空 context，一般作为根 context |
| `TODO()` | 类似 Background，用于暂时不确定使用哪个 context |
| `WithCancel(parent)` | 创建一个可手动取消的子 context |
| `WithCancelCause(parent)` | 与 `WithCancel` 类似，但可以设置取消原因（cause） |
| `WithDeadline(parent, time)` | 添加截止时间，时间到自动取消 |
| `WithDeadlineCause` | 与 `WithDeadline` 类似，同时记录超时原因 |
| `WithTimeout(parent, duration)` | 截止时间 = 当前时间 + duration |
| `WithValue(parent, key, value)` | 在 context 中存储键值对（仅用于请求范围数据） |
| `Cause(ctx)` | 获取取消原因 |
| `AfterFunc(ctx, func)` | ctx 完成后执行给定函数 |
| `WithoutCancel(parent)` | 创建一个不受 parent 取消影响的 context |

---

## 4. 设计原理

### （1）接口定义
```go
type Context interface {
    Deadline() (time.Time, bool)
    Done() <-chan struct{}
    Err() error
    Value(key any) any
}
```
- **Deadline**：返回任务的截止时间，如果没有配置截止时间，返回 `false`
- **Done**：返回一个 channel，关闭表示 context 已取消
- **Err**：返回取消原因（`Canceled` 或 `DeadlineExceeded`）
- **Value**：获取存储在 context 中的值

---

### （2）实现结构
主要结构体：
- **emptyCtx**：永不取消，无值，无截止时间（Background/TODO 基于它）
- **cancelCtx**：可取消，内部持有 `done` channel 和子 context 列表
- **timerCtx**：带定时器的 cancelCtx，用于超时/截止时间
- **valueCtx**：带键值存储的 context
- **withoutCancelCtx**：屏蔽父 context 的取消信号
- **afterFuncCtx**：在完成时执行一个函数

---

### （3）取消传播机制
- 子 context 持有父 context 的引用。
- `propagateCancel` 会将子 context 注册到父 context 的 `children` map 中。
- 父 context 被取消时，会遍历 `children` 调用它们的 `cancel()`
- 父 context 的 Done channel 被关闭时，下游可以在 select 中收到信号。

---

### （4）资源释放
- 调用返回的 `CancelFunc` 或超时后，会：
    1. 关闭 `done` channel（通知所有监听的 goroutine）
    2. 取消子 context
    3. 从父 context 的 children 中移除（避免泄漏）

> **注意**：如果不调用 `CancelFunc`，子 context 会一直存活直到父 context 被取消或超时，可能导致资源泄漏。`go vet` 会检查 CancelFunc 的调用路径。

---

### （5）Value 查找链
- `Value` 会沿着 context 链向上查找，直到找到匹配的 key 或到达根 context。
- key 必须可比较（comparable），且建议用自定义类型避免冲突。
- 不建议用 `Value` 传递可选参数或控制逻辑。

---
## 5. Go `context` 设计总览图

```
┌───────────────────────────────┐
│           Context              │  ← 接口定义
│ Deadline() (time.Time, bool)   │
│ Done() <-chan struct{}         │
│ Err() error                    │
│ Value(key any) any             │
└───────────────────────────────┘
            ▲
            │ 实现
──────────────────────────────────────────────────────────────
结构类型层次：
[永不取消]
   emptyCtx
     ├─ backgroundCtx  ← context.Background()
     └─ todoCtx        ← context.TODO()

[可取消]
   cancelCtx
     ├─ timerCtx       ← WithTimeout / WithDeadline
     ├─ afterFuncCtx   ← AfterFunc
     └─ withoutCancelCtx ← WithoutCancel

[带值]
   valueCtx ← WithValue

──────────────────────────────────────────────────────────────
派生 API：
Background() / TODO() → emptyCtx
WithCancel(parent) → cancelCtx
WithCancelCause(parent) → cancelCtx + cause
WithDeadline(parent,d) / WithTimeout → timerCtx
WithValue(parent,k,v) → valueCtx
WithoutCancel(parent) → withoutCancelCtx
AfterFunc(ctx,f) → afterFuncCtx

──────────────────────────────────────────────────────────────
取消传播流程：
parent.CancelFunc() / parent 超时触发
        │
        ▼
关闭 parent.done channel
        │
遍历 parent.children:
    child.cancel(...)
        │
        ▼
关闭 child.done channel
递归通知所有下游
        │
监听 <-ctx.Done() 的 goroutine 被唤醒 → 执行清理退出

──────────────────────────────────────────────────────────────
时间顺序示例（超时）：
t0: 创建父 ctx (WithTimeout=5s)
t1: 派生子 ctx
t5: 定时器触发 → 父 cancel(err=DeadlineExceeded)
    关闭父 done → 取消所有子
t5+ε: 子关闭 done → 通知 goroutine
t5+δ: goroutine 收到信号 → 退出
```
---
1. **接口定义** — Context 的四个核心方法。
2. **类型结构层次** — emptyCtx / cancelCtx / timerCtx / valueCtx 等的定位。
3. **API 返回类型** — 各个 WithXXX 系列函数返回的具体实现类型。
4. **取消传播链** — 父取消如何递归传播到所有子。
5. **时间顺序** — 超时触发、信号传播到 goroutine 的全流程。
---
### (1) Context 链式取消传播流程

```
       ┌──────────────────────┐
       │  context.Background  │  ← 永不取消的根 Context
       └─────────┬────────────┘
                 │
          WithCancel / WithTimeout / WithDeadline
                 │
       ┌─────────▼────────────┐
       │      cancelCtx       │  ← 可取消的中间节点
       │  (持有 children map)  │
       └─────────┬────────────┘
                 │
        多个子 Context（派生出来）
                 │
    ┌────────────┴─────────────┐
    │                          │
┌───▼─────────┐         ┌──────▼─────────┐
│  timerCtx   │         │   valueCtx     │
│ (带定时器)    │        │ (带键值对)       │
└─────────────┘         └────────────────┘
```

---

#### **取消信号传播路径**
1. **父 context** 调用 `CancelFunc` 或超时/截止时间到达 → 执行 `cancel()`：
    - 设置 `err` 为 `Canceled` 或 `DeadlineExceeded`
    - 关闭 `done` channel
    - 遍历 `children` 调用它们的 `cancel()`（递归传播）

2. **子 context** 收到父的取消信号：
    - 自己的 `done` channel 关闭
    - 继续通知它的子 context

3. **监听方**（调用方的 goroutine）在 `select { case <-ctx.Done(): ... }` 中收到取消通知，做清理或退出。

---

### (2) 事件触发示意图

```
[父 cancelCtx 超时/手动取消]
        |
        v
关闭父 done channel
        |
        v
for child in children:
    child.cancel(...)
        |
        v
关闭子 done channel
        |
        v
通知所有在 <-ctx.Done() 上阻塞的 goroutine
```

---

#### 关键源码对应点
- **注册子 Context**：`propagateCancel` 中 `p.children[child] = struct{}{}`  
- **传播取消**：`cancel()` 中的 `for child := range c.children { child.cancel(...) }`
- **Done 信号**：`done` 是 `chan struct{}`，第一次 cancel 时关闭
- **Cause**：记录取消原因（`Canceled`、`DeadlineExceeded` 或自定义）
---

### (3) 带时间线的取消时序图

假设我们有一个父 `Context`（带超时），派生出一个子 `Context`（也可手动取消），子 Context 下还有多个 goroutine 在运行。

```
时间轴（从上到下）:

t0 ──┐  创建父 Context（WithTimeout = 5s）
     │
     │  propagateCancel 注册子 Context 到父的 children
t1 ──┐  派生出子 Context（WithCancel）
     │
     │  多个 goroutine 开始 select { case <-ctx.Done(): ... }
t2 ──┐  [手动取消父 Context]
     │
     │  父 cancel():
     │    - 设置 err = Canceled
     │    - 关闭父 done channel
     │    - 遍历 children 调用 child.cancel()
     │
     │  子 cancel():
     │    - 设置 err = Canceled
     │    - 关闭子 done channel
     │    - 通知下游 goroutine
t3 ──┐  所有监听 <-ctx.Done() 的 goroutine 收到信号，执行清理退出
     │
     ▼
```

---

### (4) 超时情况下的时序

假设父 Context 是 `WithTimeout(parent, 5s)`，没有手动取消：

```
t0 ──┐  创建父 Context（超时 5s）
     │
     │  propagateCancel 注册子 Context
t1 ──┐  派生子 Context，启动 goroutine
     │
t5 ──┐  父 Context 定时器触发：
     │    - cancel(err=DeadlineExceeded)
     │    - 关闭父 done channel
     │    - 遍历 children 调用它们的 cancel()
t5+ε ─┐ 子 Context cancel()
     │    - err = DeadlineExceeded
     │    - 关闭子 done channel
     │    - 通知 goroutine
t5+δ ─┐ 所有监听 <-ctx.Done() 的 goroutine 收到信号，退出
```

---

### 关键点回顾
- **Done channel** 是取消信号的核心，关闭即表示 Context 不再可用。
- **propagateCancel** 负责建立父子关系，确保父取消时子也被取消。
- **CancelFunc** 或 **定时器** 都会调用 `cancel()`，触发信号链式传播。
- **Cause** 记录具体原因，`Err()` 返回通用错误（Canceled / DeadlineExceeded）。

---

## 6. 注意事项

1. **不要把 Context 存在 struct 里**  
   应作为函数第一个参数显式传递，方便工具分析。

2. **不要传 nil Context**  
   如果不确定用哪个，传 `context.TODO()`。

3. **及时调用 CancelFunc**  
   避免资源（goroutine、定时器）泄漏。

---