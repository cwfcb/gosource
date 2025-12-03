## 1. 整体结构
主要角色：

- **`Client`**：代表一个 RPC 客户端实例，管理连接、请求序号、pending calls 等。
- **`Call`**：一次 RPC 调用的封装，包含方法名、参数、返回值、错误和完成通知 channel。
- **`ClientCodec`**：抽象编码/解码请求和响应的接口，`gobClientCodec` 是默认实现。
- **网络连接**：底层 `io.ReadWriteCloser`（通常是 TCP 连接）。

`Client` 的两个主要 goroutine：
1. **调用方 goroutine**（用户调用 `Go` 或 `Call`）  
   负责发起请求，写数据到连接。
2. **`input()` goroutine**（由 `NewClientWithCodec` 启动）  
   负责从连接读取响应，并分发到对应的 `Call`。

## 2. 调用流程解析

### 步骤 1：用户调用 `Call()` 或 `Go()`
- `Call()` 是同步调用，会内部调用 `Go()`，然后阻塞等待 `Done` channel。
- `Go()` 会：
  1. 创建一个 `Call` 对象，设置 `ServiceMethod`、`Args`、`Reply`。
  2. 如果 `done` channel 为 nil，则分配一个有缓冲的 channel。
  3. 调用 `client.send(call)` 发起请求。

---

### 步骤 2：`send()` 写请求
`send(call)` 具体流程：

1. **加 `reqMutex`**（保证写请求是串行的，防止编码顺序错乱）。
2. **加 `mutex`**（保护 `pending` map 和状态标志）。
   - 检查 `shutdown` 或 `closing` 状态，如果已关闭，直接返回错误。
   - 分配一个新的序号 `seq`（自增）。
   - 将 `call` 存入 `pending[seq]`。
3. **构造 Request**：
   - 设置 `Seq` 和 `ServiceMethod`。
4. **编码发送**：
   - 调用 `codec.WriteRequest(&request, call.Args)`：
     - 用 `gob` 编码 request header 和参数。
     - 刷新写缓冲。
   - 如果写失败：
     - 从 `pending` 删除该 `seq` 对应的 call。
     - 设置错误，调用 `call.done()` 通知调用方。

此时，请求已经通过网络连接发出。

---

### 步骤 3：`input()` 读取响应
`NewClientWithCodec` 会启动一个 goroutine：

```go
go client.input()
```

`input()` 循环：
1. 创建一个空的 `Response` 结构。
2. 调用 `codec.ReadResponseHeader(&response)`：
   - 从网络读取响应头（包含序号 `Seq`、错误信息 `Error`）。
3. 根据 `Seq` 找到对应的 `Call`：
   - 从 `pending` 中删除。
4. 分情况处理：
   - **`call == nil`**：说明之前写请求失败了，但服务器返回了错误，这里读取并丢弃响应体。
   - **`response.Error != ""`**：服务器返回错误，设置 `call.Error`，读取并丢弃响应体。
   - **正常情况**：调用 `codec.ReadResponseBody(call.Reply)` 解码到 `Reply`。
5. 调用 `call.done()` 把 `Call` 对象送入 `Done` channel，通知调用方。
6. 如果读取响应头出错，跳出循环，标记 `shutdown`，终止所有 `pending` calls。

---

### 步骤 4：调用方获取结果
- 对于同步 `Call()`：
  ```go
  call := <-client.Go(...).Done
  return call.Error
  ```
  会阻塞直到 `input()` 调用 `call.done()` 发送结果。
- 对于异步 `Go()`：
  用户自己从 `done` channel 读取 `Call`，检查 `Reply` 和 `Error`。

---

## 3. 网络通信细节

一次正常的 RPC 调用的网络数据流：

1. **客户端写**：
   - `Request`（包含序号、方法名）
   - `Args`（方法参数）
   编码后通过 TCP 发送。

2. **服务器读**：
   - 解码 Request 和 Args。
   - 调用对应方法，生成 Reply。
   - 编码 Response（包含相同序号、错误字符串（如果有）、返回值）。
   - 发送回客户端。

3. **客户端读**：
   - 解码 Response header（取序号，匹配 pending call）。
   - 解码 Response body 到 `Reply`。


## 4. 核心实现解析

### Client 结构体
```golang
type Client struct {
	codec ClientCodec

	reqMutex sync.Mutex // protects following
	request  Request

	mutex    sync.Mutex // protects following
	seq      uint64
	pending  map[uint64]*Call
	closing  bool // user has called Close
	shutdown bool // server has told us to stop
}
```
- **`pending` map**：用于匹配请求和响应的核心数据结构。
- **`input()` goroutine**：在后台异步处理所有来自服务端的响应。
- **有缓冲的 `Done` channel**：`Call.Done` 使用缓冲 channel，防止 `input()` goroutine 在通知调用方时被阻塞。

### 并发控制：`reqMutex` 与 `mutex`

`net/rpc` 的 `Client` 设计了两个互斥锁来管理并发，这是保证其在多 goroutine 环境下安全、高效运行的核心。

#### `reqMutex`：写请求序列化锁

- **作用**：保证 **同一个 `Client` 实例在并发写请求时是串行化的**，不会有多个 goroutine 同时执行编码和写入操作。
- **位置**：在 `client.send(call)` 的开头加锁，结尾解锁。
- **目的**：
    1. **防止编码串流被打乱**：`gob.Encoder` 本身不是并发安全的，并发调用 `Encode()` 会损坏数据流。`reqMutex` 保证了同一时间只有一个 goroutine 能使用 `codec` 进行写操作。每个请求的 header 和 body 在字节流中是连续且完整的
    2. **保证请求的原子性**：一个 RPC 请求包含 Header 和 Body 两部分。此锁确保一个请求的 Header 和 Body 是连续写入的，不会被其他请求的字节流插入，从而防止数据混淆。
    3. **避免半写状态被另一个请求打断**：
       写网络数据是分多次调用的（先编码 header，再编码 body，再 flush 缓冲）。
       如果中途被另一个 goroutine插入写操作，两个请求就会互相污染。

#### `mutex`：客户端状态锁

- **作用**：保护 `Client` 的内部共享状态。
- **保护对象**：
    - `seq`：请求序号的自增。
    - `pending` map：`map` 本身不是并发安全的，任何并发读写都会导致 panic。
    - `closing` / `shutdown`：关闭状态的标志位。
- **使用场景**：
    - **写请求时** (`send` goroutine): 需要加锁来安全地分配 `seq`、将 `call` 存入 `pending` map。
    - **读响应时** (`input` goroutine): 需要加锁来安全地从 `pending` map 中取出并删除 `call`。

#### 为什么要用两个锁，而不是一个？

这是为了 **分离关注点，减小锁的粒度，提升并发性能**。

1.  **职责不同**：
    - `reqMutex` 负责 **I/O 操作** 的串行化，保证网络数据包的完整性。
    - `mutex` 负责 **内存状态** 的一致性，保护共享数据结构。

2.  **性能优化**：
    - 网络写入 (`codec.WriteRequest`) 是一个相对耗时的操作。如果用一个大锁同时锁住 I/O 和状态，那么在进行网络 I/O 时，读响应的 `input` goroutine 就无法访问 `pending` map，导致不必要的阻塞。
    - 通过分离两个锁，`input` goroutine 在处理响应时，只需要获取 `mutex`，而不需要等待写请求的 `reqMutex`。这使得 **读响应** 和 **写请求** 的大部分操作可以并行进行，只有在访问 `pending` map 的短暂瞬间才会发生锁竞争。

**总结**：`reqMutex` 保证了 **写操作的串行和原子性**，而 `mutex` 保证了 **内部状态的并发安全**。这种设计最大化了读写操作的并行能力，是 Go 并发编程中一个经典的性能优化实践。