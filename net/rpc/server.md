# 目录

- [1. `net/rpc` 设计总览](#1-netrpc-设计总览)
  - [1.1. 设计理念](#11-设计理念)
  - [1.2. 设计哲学](#12-设计哲学)
  - [1.3. 架构图](#13-架构图)
- [2. 核心组件与方法签名](#2-核心组件与方法签名)
  - [2.1. 核心组件](#21-核心组件)
  - [2.2. 方法签名约束](#22-方法签名约束)
- [3. RPC 调用流程](#3-rpc-调用流程)
  - [3.1. 同步调用 (Call)](#31-同步调用-call)
  - [3.2. 异步调用 (Go)](#32-异步调用-go)
- [4. 核心代码解析](#4-核心代码解析)
  - [4.1. `ServeConn` 与 `ServeCodec`](#41-serveconn-与-servecodec)
  - [4.2. `service.call`](#42-servicecall)
- [5. 总结](#5-总结)
  - [5.1. 设计要点](#51-设计要点)
  - [5.2. 优缺点分析](#52-优缺点分析)

---

## 1. `net/rpc` 设计总览

### 1.1. 设计理念

`net/rpc` 的目标是**将本地方法调用抽象成远程调用**，做到网络透明化（Remote Procedure Call）。它的核心思想是：

- **Server**：暴露一组符合一定签名规范的方法。
- **Client**：通过一个统一的调用接口 (`Call` / `Go`) 来访问这些方法，无需关心底层网络细节。
- **Codec**：负责序列化/反序列化请求和响应（默认使用 `encoding/gob`）。

这其实是 Go 在标准库里对 RPC 的一个最小可用实现，偏向简单和安全，而不是追求灵活性（因此它是 **frozen** 的，不再加新功能）。

### 1.2. 设计哲学

`net/rpc` 的实现遵循 Go 标准库的哲学：

- **简单优先**：功能够用即可，不追求覆盖所有复杂场景。
- **接口解耦**：通过 `Codec` 接口将编码层与调用层分离，允许用户替换默认的 `gob` 编码或网络传输方式。
- **反射驱动**：避免代码生成（stub），直接通过 `reflect` 实现动态方法查找和调用。
- **导出限制**：利用 Go 语言的导出规则（首字母大写）来确定 API 的公共边界，确保类型安全。

### 1.3. 架构图

```
        ┌──────────────────────────────────┐
        │              Client               │
        │──────────────────────────────────│
        │ Call(method, args, reply)         │
        │ Go(method, args, reply, doneChan) │
        └───────────────┬───────────────────┘
                        │
                        ▼
                ┌────────────────┐
                │   ClientCodec  │  ← 默认 encoding/gob
                └────────────────┘
                        │
                        ▼
                ┌────────────────┐
                │   net.Conn     │ ← TCP / HTTP
                └────────────────┘
                        │
                        ▼
                ┌────────────────┐
                │   ServerCodec  │  ← 默认 encoding/gob
                └────────────────┘
                        │
                        ▼
        ┌──────────────────────────────────┐
        │              Server               │
        │──────────────────────────────────│
        │ serviceMap: map[string]*service   │
        │                                    │
        │ ServeConn(conn)                   │
        │ Accept(listener)                   │
        └───────────────┬───────────────────┘
                        │
                        ▼
            ┌─────────────────────────────┐
            │ service (name: "Arith")      │
            │  methods: map[string]*method │
            │   - "Multiply"               │
            │   - "Divide"                 │
            └─────────────────────────────┘
                        │
                        ▼ (reflect.Call)
        ┌──────────────────────────────────┐
        │ Arith.Multiply(args, reply)       │
        │ Arith.Divide(args, reply)         │
        └──────────────────────────────────┘
```

## 2. 核心组件与方法签名

### 2.1. 核心组件

`net/rpc` 主要由以下几个核心结构组成，实现了良好的解耦：

- **`Server`**
  - 管理所有已注册的服务。内部通过 `map[string]*service` 存储，每个 `service` 包含接收者对象和其可调用的方法集。

- **`Client`**
  - 封装了请求发送和响应接收的逻辑，提供同步 (`Call`) 和异步 (`Go`) 两种调用方式。

- **`Codec` 接口**
  - 定义了 `ReadRequestHeader`、`ReadRequestBody`、`WriteResponse` 等方法，将编码/解码逻辑抽象出来，允许替换底层的序列化协议。

- **`Call` 结构**
  - 代表一次 RPC 调用的完整上下文，包括方法名、参数、返回值、错误以及用于异步通知的 `Done` channel。

这种分层设计使得网络层 (`net.Conn`)、编码层 (`Codec`) 和调用层 (`Server`/`Client`) 相互独立，易于扩展和替换。

### 2.2. 方法签名约束

`net/rpc` 对服务端方法的签名有严格的约束，这是其能够通过反射实现动态调用的基础。

#### 约束规则

方法必须满足以下签名：

```go
func (t *T) MethodName(argType T1, replyType *T2) error
```

并且：
- 接收者 `t` 的类型 `T` 必须是**导出的**（首字母大写）。
- 方法 `MethodName` 必须是**导出的**。
- 参数 `argType` 和 `replyType` 的类型 `T1`、`T2` 必须是**导出的或内置的**。
- 第二个参数 `replyType` 必须是**指针类型**。
- 返回值必须是 `error` 类型。

#### 设计原因

1.  **类型安全与反射可访问性**
    Go 的 `reflect` 包只能访问导出的类型和方法。RPC 框架在运行时需要通过反射来查找和调用服务方法，因此所有相关类型和方法都必须是公共的。

2.  **明确的输入输出语义**
    - **`argType T1`**：定义了客户端传入的请求参数。
    - **`replyType *T2`**：定义了服务端返回的响应数据。必须使用指针，这样服务端才能修改客户端传入的 `reply` 变量并返回结果。
    - **`error`**：作为唯一的返回值，用于传递 RPC 调用过程中的业务错误或框架错误。

3.  **序列化兼容性**
    默认的 `gob` 编码器要求所有被编码的类型其字段都是导出的。此约束确保了参数和返回值可以被正确序列化和反序列化。

4.  **简化调用约定**
    统一的方法签名极大地简化了服务端的反射调用逻辑，使其无需处理各种复杂的函数签名，保持了框架的简洁性。

## 3. RPC 调用流程

### 3.1. 同步调用 (Call)

以 `client.Call("Arith.Multiply", args, &reply)` 为例，流程如下：

```
客户端：
1. Call("Arith.Multiply", args, &reply)
2. 生成 RequestHeader{ ServiceMethod: "Arith.Multiply" }
3. gob 编码 RequestHeader + args
4. 通过 net.Conn 发送到服务端

服务端：
5. 从 net.Conn 读取数据
6. gob 解码 RequestHeader，找到 service "Arith" 和方法 "Multiply"
7. 反射调用 Arith.Multiply(args, reply)
8. 方法执行完成后返回 error（nil 表示成功）
9. gob 编码 ResponseHeader + reply
10. 通过 net.Conn 发送到客户端

客户端：
11. 接收并 gob 解码 ResponseHeader + reply
12. 返回给调用方，错误通过 error 传递
```

### 3.2. 异步调用 (Go)

异步调用 `Go` 与同步 `Call` 的核心区别在于客户端的等待方式：

- `Client.Go` 在发送请求后会**立即返回**一个 `*Call` 对象。
- 这个 `*Call` 对象包含一个 `Done` channel。
- 服务端处理完成后，`net/rpc` 框架会通过关闭此 `Done` channel 来通知客户端。
- 调用方可以自行决定是阻塞等待 `<-call.Done`，还是结合 `select` 实现超时控制。

## 4. 核心代码解析

服务端的请求处理主要由 `ServeConn`、`ServeCodec` 和 `service.call` 等函数协同完成。

### 4.1. `ServeConn` 与 `ServeCodec`

`ServeConn` 是处理单个网络连接的入口，它会创建一个默认的 `gobServerCodec`，然后调用 `ServeCodec` 进入主循环。

```golang
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	buf := bufio.NewWriter(conn)
	srv := &gobServerCodec{
		rwc:    conn,
		dec:    gob.NewDecoder(conn),
		enc:    gob.NewEncoder(buf),
		encBuf: buf,
	}
	server.ServeCodec(srv)
}

// ServeCodec 使用指定的 codec 来解码请求和编码响应。
func (server *Server) ServeCodec(codec ServerCodec) {
	sending := new(sync.Mutex)
	wg := new(sync.WaitGroup)
	for {
        // 1. 读取并解析请求
		service, mtype, req, argv, replyv, keepReading, err := server.readRequest(codec)
		if err != nil {
			if debugLog && err != io.EOF {
				log.Println("rpc:", err)
			}
			if !keepReading {
				break
			}
			// send a response if we actually managed to read a header.
			if req != nil {
				server.sendResponse(sending, req, invalidRequest, codec, err.Error())
				server.freeRequest(req)
			}
			continue
		}
		wg.Add(1)
        // 2. 异步处理请求
		go service.call(server, sending, wg, mtype, req, argv, replyv, codec)
	}
	// 等待所有响应都发送完毕
	wg.Wait()
	codec.Close()
}
```

`ServeCodec` 的主循环持续从连接中读取请求，并为每个请求启动一个 goroutine (`go service.call(...)`) 来进行处理，实现了高并发的服务能力。

### 4.2. `service.call`

此函数是真正执行 RPC 方法调用的地方。

```golang
func (s *service) call(server *Server, sending *sync.Mutex, wg *sync.WaitGroup, mtype *methodType, req *Request, argv, replyv reflect.Value, codec ServerCodec) {
	if wg != nil {
		defer wg.Done()
	}
	mtype.Lock()
	mtype.numCalls++
	mtype.Unlock()
	function := mtype.method.Func
	// 1. 通过反射调用注册的 receiver 及其 method
	returnValues := function.Call([]reflect.Value{s.rcvr, argv, replyv})
	// 2. 获取返回值（error）
	errInter := returnValues[0].Interface()
	errmsg := ""
	if errInter != nil {
		errmsg = errInter.(error).Error()
	}
    // 3. 将结果和错误信息编码后发送回客户端
	server.sendResponse(sending, req, replyv.Interface(), codec, errmsg)
	server.freeRequest(req)
}
```

核心步骤是通过 `function.Call` 执行反射调用，然后将 `replyv`（响应数据）和 `errmsg`（错误信息）打包发送回客户端。

## 5. 总结

### 5.1. 设计要点

- **Client / Server 解耦**  
  通过 `Codec` 接口隔离传输层，可以替换为 JSON、Protobuf 等。
  
- **serviceMap**  
  服务端维护一个 map，key 是类型名，value 是反射解析出的可调用方法表。

- **反射调用**  
  不生成 stub 代码，直接用 `reflect.Value.Call` 动态执行方法。

- **错误处理**  
  如果方法返回非 nil 的 `error`，服务端不会发送 `reply` 内容，只会在响应头中返回错误字符串。

### 5.2. 优缺点分析

**优点:**
- **简单易用**：API 非常简单，学习成本极低，无需定义 IDL 文件。
- **调用逻辑简洁**：基于反射和统一签名，框架内部实现简洁明了。
- **开箱即用**：默认使用 `gob` 编码，对于纯 Go 环境的内部服务通信非常方便。
- **支持同步/异步**：提供了 `Call` 和 `Go` 两种模式，满足不同场景的需求。

**缺点:**
- **方法签名限制严格**：不够灵活，无法支持流式 RPC、通知（无返回值）等高级模式。
- **跨语言兼容性差**：`gob` 编码是 Go 特有的，这使得 `net/rpc` 几乎无法用于多语言系统之间的通信。
- **性能非最优**：反射调用和 `gob` 序列化相比 Protobuf 等方案存在性能差距。
- **错误处理和超时机制较原始**：需要用户自行封装更复杂的错误传递和超时控制逻辑。