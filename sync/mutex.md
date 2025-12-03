# sync/mutex.md 目录

- [1. sync 包核心注释](#1-sync-包核心注释)
  - [1.1. 核心定位：基础同步原语](#11-核心定位基础同步原语)
  - [1.2. 目标用户：主要面向底层库开发者](#12-目标用户主要面向底层库开发者)
  - [1.3. Go 的并发哲学：通信优于共享](#13-go-的并发哲学通信优于共享)
  - [1.4. 铁律：不可复制](#14-铁律不可复制)
  - [1.5. 整体总结](#15-整体总结)
- [2. mutex 解析](#2-mutex-解析)
  - [2.1. mutex 公平性策略](#21-mutex-公平性策略)
    - [2.1.1. 核心思想：性能与公平的动态平衡](#211-核心思想性能与公平的动态平衡)
    - [2.1.2. 正常模式 (Normal Mode)：性能优先](#212-正常模式-normal-mode性能优先)
    - [2.1.3. 饥饿模式 (Starvation Mode)：公平优先](#213-饥饿模式-starvation-mode公平优先)
    - [2.1.4. 模式切换：回归高性能](#214-模式切换回归高性能)
  - [2.2. `state` 字段位图（Bitmask）描述](#22-state-字段位图bitmask描述)
    - [2.2.1. `L (Bit 0): mutexLocked`](#221-l-bit-0-mutexlocked)
    - [2.2.2. `W (Bit 1): mutexWoken`](#222-w-bit-1-mutexwoken)
    - [2.2.3. `S (Bit 2): mutexStarving`](#223-s-bit-2-mutexstarving)
    - [2.2.4. `Waiter Count (Bits 3-31)`](#224-waiter-count-bits-3-31)
  - [2.3. 详细解析](#23-详细解析)
    - [2.3.1. lockSlow 的核心设计思想](#231-lockslow-的核心设计思想)
    - [2.3.2. unlockSlow 的核心设计思想](#232-unlockslow-的核心设计思想)
    - [2.3.3. mutex 饥饿模式，如何保实现抢锁的 gouroutine 公平性](#233-mutex-饥饿模式如何保实现抢锁的-gouroutine-公平性)
    - [2.3.4. 允许 goroutine1 调用 Lock，gouroutine2 调用 Unlock 解锁](#234-允许-goroutine1-调用-lockgouroutine2-调用-unlock-解锁)
  - [2.4. 源码解析](#24-源码解析)

---

# 1. sync 包核心注释

> Package sync provides basic synchronization primitives such as mutual
> exclusion locks. Other than the Once and WaitGroup types, most are intended
> for use by low-level library routines. Higher-level synchronization is
> better done via channels and communication.
>
> Values containing the types defined in this package should not be copied.


## 1.1. 核心定位：基础同步原语
>Package sync provides basic synchronization primitives such as mutual exclusion locks.

-   **功能**: `sync` 包提供了并发编程中最基础、最核心的工具，即“同步原语”（Synchronization Primitives）。
-   **例子**: 注释明确举了互斥锁（`Mutex`）作为例子。这表明 `sync` 包是用来解决多 Goroutine 间因共享内存而产生的竞态条件问题的。它提供的工具包括但不限于：
    -   `Mutex`: 互斥锁，保证同一时间只有一个 Goroutine 能访问临界区。
    -   `RWMutex`: 读写锁，允许多个读或一个写，用于读多写少的场景。
    -   `Cond`: 条件变量，用于等待某个条件达成。
    -   `Pool`: 临时对象池，用于复用对象，减轻 GC 压力。
    -   `Map`: 并发安全的 map。



## 1.2. 目标用户：主要面向底层库开发者
>Other than the Once and WaitGroup types, most are intended for use by low-level library routines.

-   **分层**: Go 团队对 `sync` 包内部的工具做了明确的“分层”。
    -   **应用层原语 (`Once`, `WaitGroup`)**: `Once`（保证代码只执行一次）和 `WaitGroup`（等待一组 Goroutine 完成）被认为是“高级”或者说“应用友好”的。因为它们的使用场景非常明确、通用，可以直接用在业务逻辑代码中，且不易出错。
    -   **底层原语 (`most are intended for use by low-level library routines`)**: 除 `Once` 和 `WaitGroup` 外的大多数工具（如 `Mutex`, `Cond`, `Pool` 等），被期望主要由底层库的开发者使用。这意味着，当你在编写一个通用的、需要高性能并发控制的库（比如一个数据库驱动、一个 RPC 框架）时，直接使用这些工具是合适的。



## 1.3. Go 的并发哲学：通信优于共享
>Higher-level synchronization is better done via channels and communication.

-   **核心思想**: 这是 Go 并发编程的黄金法则，源自其核心的 CSP (Communicating Sequential Processes) 模型。这句注释强烈建议，在编写应用程序级别的并发逻辑时，应该优先使用 channel 来进行 Goroutine 之间的同步和通信。
-   **为什么？**
    -   “通过通信来共享内存，而不是通过共享内存来通信”。使用 channel 时，数据的传递和所有权转移是明确的。一个 Goroutine 将数据发送到 channel，另一个 Goroutine 从 channel 接收，这天然地避免了数据竞争。
    -   **代码更清晰**: 基于 channel 的设计往往能更清晰地表达业务流程和 Goroutine 之间的协作关系，降低了心智负担。
    -   **减少错误**: 直接使用锁（共享内存模型）非常容易出错，比如忘记加锁、解锁，或者出现死锁。Channel 在很多场景下能从根本上避免这些问题。
-   **总结**: `sync` 包是处理共享内存问题的“手术刀”，而 channel 是构建并发程序的“蓝图”。对于大多数应用开发者来说，应该首先思考如何用 channel 设计并发模型，只有在 channel 无法满足需求或性能成为瓶颈时，才应该考虑使用 `sync` 包里的底层原语。


## 1.4. 铁律：不可复制
>Values containing the types defined in this package should not be copied.



-   **原因**: `sync` 包里的所有类型（`Mutex`, `WaitGroup`, `Cond` 等）内部都包含着状态。例如，`Mutex` 内部有 `state` 字段记录锁的状态（是否被持有、等待者数量等）。
-   **复制的后果**: 如果你复制一个 `Mutex`，你得到的是一个全新的、状态独立的新锁。对副本的加锁、解锁操作，与原始的锁毫无关系。这完全违背了使用锁的初衷，会导致数据竞争，程序逻辑完全错乱。
-   **如何保证**: Go 语言通过 `go vet` 工具来静态检查这种错误。实现上，`sync` 包里的类型通常会内嵌一个 `noCopy` 结构体，这个结构体可以被 `go vet` 识别，任何尝试复制包含它的结构体的行为都会被报告为错误。

## 1.5. 整体总结

这段注释是 `sync` 包的“使用说明书”和“设计哲学宣言”。它告诉我们：

-   `sync` 包是 Go 并发工具箱的底层，提供了处理共享内存问题的必要工具。
-   Go 更推崇使用 channel 来构建高层级的并发程序，这通常更安全、更清晰。
-   `WaitGroup` 和 `Once` 是两个例外，它们是足够简单和安全的，可以广泛用于应用代码中。
-   绝对不要复制 `sync` 包里的任何类型的值，这是一个必须遵守的硬性规定。

作为资深专家，您可以将此理解为 Go 团队在引导开发者走向一种更现代化、更不易出错的并发编程范式，同时又为大家保留了处理极端性能场景和构建底层库所必需的传统工具。

# 2. mutex 解析
## 2.1. mutex 公平性策略

`sync.Mutex` 为了在 **性能（吞吐量）** 和 **公平性** 之间做出权衡而设计的两种核心工作模式：**正常模式 (Normal Mode)** 和 **饥饿模式 (Starvation Mode)**。

### 2.1.1. 核心思想：性能与公平的动态平衡

一个简单的锁可以做到绝对公平（严格的先进先出），但这通常会牺牲性能。反之，一个只追求性能的锁可能会让某些goroutine永远等待，即“饿死”。Go的 Mutex 通过在这两种模式间动态切换，试图在大部分时间里提供最高性能，同时在检测到可能发生不公平时，切换到公平模式来保障所有goroutine都能最终获得锁。

### 2.1.2. 正常模式 (Normal Mode)：性能优先

这是 Mutex 的默认和常规工作模式。

#### 工作机制:

- 等待锁的goroutine会进入一个FIFO（先进先出）队列。
- 当锁被释放时，队列头部的goroutine会被唤醒。
- **关键点**：被唤醒的goroutine并 **不直接** 获得锁。它必须与那些 **新到达的**、正要尝试加锁的goroutine 进行竞争。

#### 性能优势 (为什么这样设计？):

- 新到达的goroutine（我们称之为“闯入者”）具有天然优势：它们已经在CPU上运行，而刚被唤醒的goroutine需要进行一次上下文切换才能开始运行。
- 因此，“闯入者”有很大概率能抢在被唤醒的等待者之前获得锁。
- 这种机制使得一个goroutine可以连续、快速地多次获取和释放同一个锁（只要中间没有其他goroutine成功抢占），极大地提高了吞吐量。这对于锁竞争不激烈或锁持有时间极短的场景非常高效。

#### 潜在问题:

- **不公平**：如果“闯入者”源源不断，那么排在等待队列头部的goroutine可能会反复竞争失败，导致其等待时间过长。

### 2.1.3. 饥饿模式 (Starvation Mode)：公平优先

当系统检测到不公平可能正在发生时，就会切换到此模式。

#### 触发条件:

- 当一个等待的goroutine发现自己的等待时间已经超过了 **1毫秒** (`starvationThresholdNs`)，它就会在尝试获取锁时，将该 Mutex 的状态切换为“饥饿模式”。

#### 工作机制:

- **所有权直接交接**: 在此模式下，锁的所有权不再通过竞争获得。当一个goroutine解锁时，它会 **直接将锁的所有权移交给等待队列的头部goroutine**。
- **禁止“闯入”**: 新到达的goroutine不被允许尝试获取锁（即使锁看起来是可用的），它们也不能进行“自旋”优化，必须直接进入等待队列的末尾。

#### 设计目的:

- **保证公平性**: 确保等待队列中的goroutine能严格按照FIFO顺序获得锁，杜绝任何goroutine被“饿死”的可能性。
- **防止极端尾部延迟 (Tail Latency)**: 这是该模式最重要的作用。它确保了即使在最坏的情况下，锁的获取延迟也有一个可预测的上限，避免了极少数请求耗时过长而影响整个系统的稳定性。

### 2.1.4. 模式切换：回归高性能

系统不会一直停留在效率较低的饥饿模式。

#### 切换回正常模式的条件:

当一个在饥饿模式下获得锁的goroutine完成其工作后，它会检查两个条件：
- 它自己是等待队列中的最后一个等待者。
- 或者，它自己的等待时间其实并未超过1毫秒。

满足以上任一条件，这个goroutine就会负责将锁切换回 **正常模式**。这表明“饥饿”状态已经缓解，系统可以重新回到性能优先的模式。

---
**一句话总结**：它不是一个简单的互斥锁，而是一个 **自适应的、智能的并发原语**。它默认采用乐观的、性能优先的策略，但在实践中通过一个简单的超时阈值，优雅地解决了可能出现的公平性问题。

## 2.2. `state` 字段位图（Bitmask）描述
`state` 是一个 `int32` 类型的变量。我们可以将其想象成一个由32个比特位组成的数组。Go的 Mutex 设计者巧妙地为其中几个比特位赋予了特殊含义，而剩下的大部分位则组成了一个计数器。

其结构如下所示：

```
  31                                 3  2  1  0
+------------------------------------+--+--+--+
|          Waiter Count              | S| W| L|
+------------------------------------+--+--+--+
```

---

### 2.2.1. `L (Bit 0): mutexLocked`

-   **含义**: 锁的锁定状态。
-   **值**:
    -   `1`: 互斥锁已被某个 goroutine 持有（已锁定）。
    -   `0`: 互斥锁当前可用（未锁定）。
-   **作用**: 这是最基本的状态位。`Lock()` 的快速路径就是尝试将这一位从 `0` 原子地变为 `1`。

---

### 2.2.2. `W (Bit 1): mutexWoken`

-   **含义**: 唤醒标记。
-   **值**:
    -   `1`: 表示已经有一个等待中的 goroutine 被唤醒了。
    -   `0`: 没有 goroutine 被标记为“已唤醒”。
-   **作用**: 这是一个非常重要的 **通信与优化** 标记。它用于 `Lock` 和 `Unlock` 之间的协作。当一个 goroutine 在自旋（spinning）时，它会尝试设置此位，告诉 `Unlock` 操作：“我马上就要拿到锁了，你不必再费力去唤醒其他沉睡的 goroutine 了”。这避免了不必要的上下文切换，提升了性能。

---

### 2.2.3. `S (Bit 2): mutexStarving`

-   **含义**: 饥饿模式标记。
-   **值**:
    -   `1`: 互斥锁当前处于 **饥饿模式**。
    -   `0`: 互斥锁当前处于 **正常模式**。
-   **作用**: 这是实现公平性的关键。当一个 goroutine 等待锁的时间超过1ms，它就会将锁切换到饥饿模式。在此模式下，锁的所有权会直接从解锁者传递给等待队列的队头，新来的 goroutine 不允许抢占，必须排队。

---

### 2.2.4. `Waiter Count (Bits 3-31)`

-   **含义**: 等待者计数器。
-   **描述**: 这部分的所有位组成一个整数，表示 **当前有多少个 goroutine 正在阻塞等待这个锁**。
-   **操作**: `state >> mutexWaiterShift` (即 `state >> 3`) 就可以得到等待者的数量。
-   **作用**: `Unlock` 操作会检查这个计数器，如果大于0，就知道需要唤醒一个等待者。

## 2.3. 详细解析
### 2.3.1. lockSlow 的核心设计思想
`lockSlow` 函数在 `Lock()` 的快速路径（一次 CAS 尝试）失败后被调用。它的核心任务是管理等待锁的 goroutine，并在合适的时机获取锁。其设计围绕以下几个关键概念展开：

- **自旋（Spinning）**：在进入完全阻塞休眠之前，进行短暂的、耗费 CPU 的循环等待。
- **两种模式（Modes）**：正常模式（Normal Mode）和饥饿模式（Starvation Mode）。
- **状态机（State Machine）**：通过对 `state` 字段的原子操作，在不同状态间转换。
- **与 Unlock 的协作**：通过 `mutexWoken` 标记与解锁操作进行高效通信。

#### lockSlow 的详细执行流程
`lockSlow` 的逻辑可以分解为一个大的 `for` 循环，每次循环都代表一次获取锁的尝试。

##### 1. 自旋（Spinning）
> **条件**: `if old&(mutexLocked|mutexStarving) == mutexLocked && runtime_canSpin(iter)`

- `old&mutexLocked == mutexLocked`: 锁必须是已被锁定状态。如果锁未被锁定，我们应该立即尝试获取，而不是自旋。
- `old&mutexStarving == 0`: 必须是正常模式。在饥饿模式下，为了保证公平性，不允许新来的 goroutine 通过自旋来“插队”，必须排队。
- `runtime_canSpin(iter)`: 这是一个运行时函数，它决定了是否值得自旋。它会考虑几个因素：
    - `iter`: 当前已经自旋的次数。自旋不能无限进行。
    - `GOMAXPROCS > 1`: 如果只有一个处理器，自旋的 goroutine 会阻塞持有锁的 goroutine 运行，毫无意义。
    - 处理器的负载情况。

**目的**: 如果锁可能在极短时间内被释放，通过自旋（消耗CPU）来等待，可以避免 goroutine 进入休眠和被唤醒的巨大开销（两次上下文切换）。这是一种用 CPU 时间换取延迟的优化。在自旋期间，它还会尝试设置 `mutexWoken` 标志，通知 `Unlock` 操作不必唤醒其他goroutine，因为自己马上就要拿到锁了。

##### 2. 正常模式（Normal Mode）下的排队
> **条件**: `if old&mutexStarving == 0` (即 `!starving`)

**行为**:

1.  **增加等待者计数**: `new += 1 << mutexWaiterShift`。原子地将 `state` 中的等待者数量加一。
2.  **尝试获取锁**: `new |= mutexLocked`。在增加等待者数量的同时，也尝试将锁状态位设置为 `mutexLocked`。
3.  **执行CAS**: `atomic.CompareAndSwapInt32(&m.state, old, new)`。
    - **如果CAS成功**:
        - 如果 `old` 状态表明锁之前是未锁定的 (`old&(mutexLocked|mutexStarving) == 0`)，这意味着本次 CAS 不仅成功增加了等待者计数，还顺便获取了锁。这是非常幸运的情况，可以直接返回。为什么要检查 `mutexStarving = 0` 呢，因为只有在正常模式下，自旋的 goroutine 才能去抢锁；如果是饥饿模式，要优先让队头的 goroutine 获取锁。
        - 否则，意味着只是成功地将自己加入了等待队列。此时，调用 `runtime_SemacquireMutex(&m.sema, ...)`，goroutine 将会 **休眠** 在信号量 `m.sema` 上。
    - **如果CAS失败**: 意味着在计算 `new` 和执行 CAS 之间，`m.state` 被其他 goroutine（通常是 `Unlock` 操作）改变了。此时，循环重新开始，用最新的 `state` 再次尝试。

**吞吐量优先**: 在正常模式下，一个被唤醒的 goroutine (`Unlock` 唤醒的) **不会** 直接获得锁。它需要和新到达的 goroutine（那些正在 `lockSlow` 里自旋或尝试 CAS 的）竞争锁。这种策略被称为 **“Barging”（闯入）**。这允许新来的、CPU正热的 goroutine 有机会直接抢到锁，避免了上下文切换，从而最大化吞吐量。但代价是，等待队列中的 goroutine 可能会被一再“插队”，导致延迟。

##### 3. 切换到饥饿模式（Starvation Mode）
**触发条件**: 当一个 goroutine 从休眠中被唤醒后，它会检查自己的等待时间。如果等待时间超过了 `starvationThresholdNs` (1毫秒)，它就会决定将锁切换到饥饿模式。
> `starving = starving || runtime_nanotime()-waitStartTime > starvationThresholdNs`

**行为**:

- 在下一次循环中，`starving` 标志位为 `true`。
- 计算新状态时，会加上 `mutexStarving` 位：`new |= mutexStarving`。
- 通过 CAS 将这个新状态写入 `m.state`。

**公平性优先**: 一旦锁进入饥饿模式：
- **锁的所有权直接交接**: `Unlock` 操作会直接将锁交给等待队列的第一个 goroutine，而不是简单地唤醒它让它去竞争。
- **禁止闯入**: 新来的 goroutine 在 `lockSlow` 中看到 `mutexStarving` 位被设置，会禁止自旋，并且不会尝试去获取锁，而是直接把自己加到等待队列的末尾。

##### 4. 从饥饿模式切换回正常模式
**触发条件**:

- 当一个 goroutine 在饥饿模式下获得了锁，如果它是等待队列中的 **最后一个等待者** (`old` state 的等待者计数为1)，它会负责将 `mutexStarving` 标志位清除，使锁回到正常模式。
- 或者，当一个 goroutine 在饥饿模式下获得锁，它会重新评估自己的等待时间，如果发现等待时间其实并未超过阈值（这可能发生在它被唤醒和它实际运行之间有延迟），它也会将锁切回正常模式。

**目的**: 饥饿模式虽然保证了公平，但牺牲了吞吐量。一旦饥饿问题得到缓解（等待队列变空），就应该立即回到性能更高的正常模式。

#### 设计精髓
`lockSlow` 是一个精妙的自适应系统：

- **乐观启动**: 它首先假设竞争不激烈，尝试用最高性能的 **自旋** 来解决问题。
- **优雅降级**: 当自旋失败，它进入 **正常模式**，通过允许“闯入”来优先保证吞吐量。
- **公平保障**: 当检测到有 goroutine 等待时间过长（可能发生“饥饿”），系统切换到 **饥饿模式**，严格保证先来后到。
- **饥饿解除**: 一旦饥饿情况解除，系统立即切回 **正常模式**，恢复高性能吞吐。

---

#### Q1: 为什么 `awoke` 状态的 goroutine 要清除 `mutexWoken` 状态？
这涉及到 `Lock` 和 `Unlock` 之间的一种高效的“通信协议”。

- **`mutexWoken` 的角色**: 它是 `state` 中的一个 **全局信号**，用于避免不必要的唤醒。当一个 goroutine 在自旋（spinning）或被唤醒后，它会尝试设置 `mutexWoken` 位。这等于在告诉 `Unlock` 操作：“别费力去唤醒信号量上休眠的 goroutine 了，因为我已经醒了，并且正在积极地尝试获取锁”。这避免了多个 goroutine 被唤醒后激烈竞争，导致性能下降（即所谓的“惊群效应”）。
- **`awoke` 的角色**: 这是当前 goroutine 的一个 **局部状态** 变量。`awoke = true` 表示“我”这个 goroutine 是被 `Unlock` 操作从休眠中唤醒的。

**清除 `mutexWoken` 的原因：**

可以把 `mutexWoken` 想象成一个 **一次性的通知**。

1.  **接收通知**: 当 `Unlock` 唤醒一个 goroutine 时，它会同时设置 `mutexWoken` 标志位（或者说，被唤醒的 goroutine 在醒来后发现这个标志被设置了）。我们的 goroutine 醒来后，将自己的局部变量 `awoke` 设为 `true`，表示已经收到了这个“唤醒通知”。
2.  **消耗/清除通知**: 现在，这个 goroutine 已经醒了，轮到它来尝试获取锁了。它在准备通过 CAS 更新 `state` 时，必须 **“消耗”** 掉这个 `mutexWoken` 信号，也就是将它从 `state` 中清除。

**如果不清除会发生什么？**

`mutexWoken` 标志会一直留在 `state` 中。后续的 `Unlock` 操作每次看到 `mutexWoken` 被设置，都会误以为“已经有人醒了在忙了”，于是就决定不再唤醒任何其他正在休眠的 goroutine。结果就是，等待队列中的 goroutine 将永远无法被唤醒，导致 **死锁**。

因此，被唤醒的 goroutine 有责任在自己采取行动时，清除掉这个全局的 `mutexWoken` 信号，让状态机恢复正常，确保后续的 `Unlock` 操作可以正确地唤醒其他等待者。

> 正是这个逻辑的体现：我（`awoke` 的 goroutine）已经被唤醒，现在我来处理后续事宜，这个“唤醒”信号可以熄灭了。

---

#### Q2: `delta := int32(mutexLocked - 1<<mutexWaiterShift)` 这段代码作用？
这是一段极其精妙的位运算，目的是 **通过一次原子的 `Add` 操作，同时完成两项状态更新**，从而达到极高的效率。

**背景**: 这段代码位于饥饿模式下，一个被唤醒的 goroutine 刚刚获得了锁的所有权。此时 `state` 的状态是：`mutexLocked` 位为0，`waiter` 计数器包含着当前的 goroutine。现在，这个 goroutine 需要更新 `state` 来反映新的现实：

- 锁应该被标记为“已锁定”（设置 `mutexLocked` 位）。
- 自己不再是“等待者”，所以“等待者计数器”应该减一。

**代码拆解**:

- `mutexLocked`: 这个常量的值是 `1`。
- `1 << mutexWaiterShift`: `mutexWaiterShift` 的值是 `3`。所以 `1 << 3` 的结果是 `8`。在位图中，8 正好是“等待者计数器”的最低位，代表一个等待者。
- `delta := int32(1 - 8)`: 所以 `delta` 的值是 `-7`。

**神奇之处在于 `atomic.AddInt32(&m.state, delta)`：**

给 `state` 加上 `-7`，在二进制层面发生了什么？

> 假设 `state` 当前的值是 `S`。`S + (-7)` 等价于 `S + 1 - 8`。

- **`S + 1`**: `1` 正好是 `mutexLocked` 位。加 1 的效果就是 **将 `mutexLocked` 位设置为 1**。
- **`S - 8`**: `8` 正好是等待者计数的单位值。减 8 的效果就是 **将等待者计数器减 1**。

通过一个 `delta = -7`，`atomic.AddInt32` 这个 **单一的原子指令** 就同时完成了“加锁”和“等待者减一”两个逻辑操作。这比使用需要循环尝试的 CAS 操作来先加锁、再减计数器要高效得多。

**进一步扩展**:

- 后面的代码 `delta -= mutexStarving` 也是同样的道理。如果需要退出饥饿模式，就在 `delta` 的基础上再减去 `mutexStarving` 的值（即 4）。这样 `delta` 就变成 `-11` (`-7 - 4`)。给 `state` 加上 `-11` 就相当于同时完成了 **加锁、等待者减一、退出饥饿模式** 三个操作。

> 这正是 Go `sync.Mutex` 设计的精髓：将复杂的并发状态管理，浓缩到对一个整型变量的几次原子操作上，将硬件性能压榨到极致。

### 2.3.2. unlockSlow 的核心设计思想
#### `unlockSlow` 的目的与触发时机
`Unlock` 操作会首先尝试一个快速路径：通过一次原子的 `AddInt32(&m.state, -mutexLocked)` 操作来释放锁，那么 `mutexLocked` 位一定是 0 了。如果这个操作的结果不为零，就意味着 `state` 中还包含着其他信息（比如有等待者、或者设置了特殊标记位），这时就必须调用 `unlockSlow` 来处理这些复杂情况。

`unlockSlow` 必须处理两种主要场景，正好对应 Mutex 的两种工作模式：

1.  **正常模式 (Normal Mode)**: 唤醒一个等待者，但优先保证吞吐量。
2.  **饥饿模式 (Starvation Mode)**: 将锁的所有权直接移交给下一个等待者，以保证公平性。

#### `unlockSlow` 的详细执行流程

##### 1. 正常模式 (`if m.state&mutexStarving == 0`)
此模式的目标是 **最大化吞吐量**。

- **初始检查**: 它首先会检查是否真的有必要唤醒一个 goroutine。如果没有等待者 (`old>>mutexWaiterShift == 0`)，或者看起来有其他 goroutine 已经在积极地尝试获取锁（比如它正在自旋，并设置了 `mutexWoken` 标记），那么 `unlockSlow` 就只会简单地移除 `mutexLocked` 标记位然后返回。它让那些活跃的 goroutine 去竞争，这比唤醒一个沉睡的 goroutine 更快。
- **核心逻辑**: 如果确定有沉睡的等待者，并且没有其他 goroutine 在积极竞争，`unlockSlow` 就会决定唤醒一个。这时，关键代码行就登场了：
  > `new = (old - 1<<mutexWaiterShift) | mutexWoken`
  - **`old - 1<<mutexWaiterShift`**: 这个操作以原子方式 **将等待者计数器减一**。因为我们即将唤醒一个等待者，所以需要先从等待队列的计数中“消耗”掉一个名额。
  - **`| mutexWoken`**: 这个操作同时 **设置 `mutexWoken` 标志位**。
  这个组合操作的意义，可以理解为一个单一的、原子性的消息：“我在此正式将等待者数量减一，并点亮‘已唤醒’信号灯。下一个被唤醒的 goroutine 将知道是我唤醒了它。”

- **唤醒操作**: 在状态成功更新后，代码会调用 `runtime_Semrelease(&m.sema, false, 1)`。
  - 这里的第二个参数 `handoff` 被设为 `false`，这一点至关重要。它告诉 Go 运行时：“从 sema 信号量的等待队列中唤醒一个 goroutine，但不要把锁直接给他。仅仅是让他恢复运行。”

  - 被唤醒的 goroutine 必须在 `lockSlow` 中重新进入对锁的竞争。这种机制允许一个新来的、正在“闯入”（Barging）的 goroutine 有机会抢先获得锁，从而避免了被唤醒 goroutine 的上下文切换开销，进而提升了整体吞吐量。

##### 2. 饥饿模式 (`else`)
此模式的目标是 **保证公平性**。逻辑更简单，但意义却截然不同。

- **直接交接 (Direct Handoff)**: 它直接调用 `runtime_Semrelease(&m.sema, true, 1)`。
  - 这里的第二个参数 `handoff` 被设为 `true`，是实现公平性的关键。它是在给运行时下一个明确的命令：“去 sema 的等待队列，从队头取出那个等待时间最长的 goroutine，然后将锁的所有权直接转移给它。”

- **此处不改变状态**: 请注意，在此模式下，`unlockSlow` 本身并不会修改 `state`。它不会清除 `mutexLocked` 位。锁从未被真正地“释放”到公共池中，而是像接力棒一样，从一个持有者直接传递给下一个。

被唤醒的 goroutine，在 `lockSlow` 中从沉睡中返回时，会发现锁处于饥饿模式，并知道自己已被授予了所有权。接下来将由它自己负责更新 `state`（设置 `mutexLocked`、将等待者计数减一，并可能最终退出饥饿模式）。

#### 总结
`unlockSlow` 是 `lockSlow` 智能的另一半。它利用同一个 `state` 变量来决定采取何种策略：

- 在 **正常模式** 下，它就像赛跑的发令枪：它唤醒一个选手，但允许所有人（包括新来的）为奖品冲刺。这很快，但可能不公平。`new = (old - 1<<mutexWaiterShift) | mutexWoken` 这行代码，就是扣动发令枪前的原子化准备动作。
- 在 **饥饿模式** 下，它扮演着一个纪律严明的队列管理员：它亲手将锁交给队伍中的下一个人，确保每个人都有机会。这很公平，但有更高的协调开销。

---

#### `Unlock` 操作的原子性保证

> **`UnkLock` 一定会把 `state` 的 lock 位（也就是最低位）从 1 重置为 0 吗？**

是的，`new := atomic.AddInt32(&m.state, -mutexLocked)` **百分之百保证** 会将 `state` 的 `lock` 位（最低位）从 1 重置为 0。

这背后的原理既巧妙又简单，是基于二进制的数学特性：

1.  **`mutexLocked` 的值**: 这个常量的值是 1。所以这行代码等价于 `atomic.AddInt32(&m.state, -1)`，也就是对 `state` 进行原子减一操作。
2.  **调用 `Unlock` 的前提**: `Unlock` 必须在互斥锁已被锁定的情况下调用。这意味着 `state` 的 `mutexLocked` 位（最低位）必然是 1。
3.  **二进制魔法**:
    - 任何一个整数，如果它的最低位是 1，那么它一定是一个 **奇数**。
    - 任何一个整数，如果它的最低位是 0，那么它一定是一个 **偶数**。
    - 由于 `state` 的 `lock` 位是 1，所以 `state` 在解锁前的值一定是一个奇数。

> 一个奇数减去 1，必然会得到一个偶数。
> 而一个偶数的二进制表示，其最低位永远是 0。

`atomic.AddInt32` 会返回减一之后的 **新值 `new`**。

- **最理想的情况**: 如果 `new` 的值等于 0，这意味着旧的 `state` 值正好就是 1 (`mutexLocked`)。这说明当时只有 `lock` 位被设置，没有等待者，没有饥饿，也没有唤醒标记。通过一次减法，锁被完美释放，一切都结束了。这是最快的解锁路径。
- **需要慢速处理的情况**: 如果 `new` 的值不等于 0，这意味着旧的 `state` 中除了 `lock` 位，还有其他信息（比如等待者计数器不为零）。虽然 `lock` 位已经被成功清除了，但还有后续工作要做（比如唤醒一个等待者）。这时，就需要进入 `unlockSlow` 函数，并把这个 `new` 状态传递给它，由它来完成剩下的复杂工作。
#### 正常模式下的唤醒判断

> 怎么理解 `old&(mutexLocked|mutexWoken|mutexStarving) != 0` 这个条件？

以下三个位运算条件用于 `unlockSlow` 在正常模式决定是否“唤醒一个等待者”。  
只要 **任一条件为真**，当前 goroutine 就放弃唤醒，直接 `return`，把后续工作留给下一次 `Unlock` 或饥饿模式路径。

| 条件 | 位运算 | 含义与解释 |
|---|---|---|
| ① | `old & mutexLocked != 0` | **“锁是不是又被别人抢走了？”**<br>`Unlock` 先把 `mutexLocked` 清零，再进入 `unlockSlow`。在这极短的时间窗里，某个自旋的“闯入者”可能已通过 CAS 再次上锁。若发现该位为 1，说明锁已重新被占用，此时唤醒一个睡眠 goroutine 只会让它醒来再睡，毫无意义，因为它醒来后发现锁还是被占着，还得继续睡。所以，不如直接返回，把唤醒的责任留给新的持锁者。 |
| ② | `old & mutexWoken != 0` | **“已经有别人在负责唤醒了吗？”**<br>`mutexWoken` 是“唤醒进行中”的全局标记。前一个 `Unlock` 在决定唤醒时会把该位置 1，防止多 goroutine 同时唤醒导致惊群。若当前 goroutine 看到这一位已置位，便知“唤醒任务已被认领”，自己无需重复劳动，立即返回。同一时间只有一个 Unlock 在执行唤醒。 |
| ③ | `old & mutexStarving != 0` | **“锁已经进入饥饿模式了吗？”**<br>这段代码只处理“正常模式”的唤醒逻辑。然而并发过程中，某个等待超过 1 ms 的 goroutine 可能刚好把 `mutexStarving` 置 1。一旦检测到这一位为 1，说明环境已变，必须改用饥饿模式的“直接交接”策略；当前逻辑立即终止，跳到 `unlockSlow` 末尾的饥饿模式分支，保证公平性。 |

为什么条件 3 可以直接返回？

##### 为什么 `unlockSlow` 在饥饿模式下直接 `return` 不会导致死锁？

在正常模式的 `unlockSlow` 中，如果发现 Mutex 突然变成了饥饿模式，当前的 goroutine 确实会放弃唤醒任务直接 return。表面上看，这似乎会留下一个无人唤醒的、正在休眠的等待者，从而导致死锁。

但实际上，这是绝对安全的。原因在于：`mutexStarving` 标志位不会凭空出现。

要理解这一点，我们需要分析一个关键的竞态条件（Race Condition）：

- 一个 goroutine（我们称之为 G1）正在执行 `unlockSlow` 的正常模式路径；
- 同时另一个等待了很久的 goroutine（G2）正在 `lockSlow` 中决定将 Mutex 切换到饥饿模式。


这里的关键在于，G2 在 `lockSlow` 中切换到饥饿模式的这个动作，并不是孤立的。它会和 G1 的 `unlockSlow` 竞争去修改 `state`。而这个竞争的获胜方，会承担起锁的后续责任。

让我们来详细推演一下这个过程：

1. **初始状态**  
   - G1 持有锁。  
   - G2 在等待队列中，即将因超时（等待超过 1 ms）而触发饥饿模式。  
   - `Mutex` 处于正常模式。

2. **G1 开始解锁**  
   - G1 调用 `Unlock()`，通过原子减法将 `mutexLocked` 位清零。  
   - 因为还有等待者（G2），G1 进入 `unlockSlow` 的正常模式路径。

3. **竞态发生**  
   就在 G1 进入 `unlockSlow` 之后、执行其内部的 CAS 操作之前，G2 的定时器到期了。

4. **G2 的行动 (`lockSlow`)**  
   - G2 发现锁是空闲的（因为 G1 已经清零了 `mutexLocked` 位）。  
   - G2 决定切换到饥饿模式。  
   - G2 会尝试执行一个非常关键的原子 CAS 操作，这个操作试图“一石三鸟”：  
     - 将 `mutexStarving` 位置为 1（进入饥饿模式）；  
     - 将等待者计数器加 1；  
     - 因为发现锁是空闲的，它会同时尝试将 `mutexLocked` 位置为 1（直接抢占锁）！

5. **G1 的行动 (`unlockSlow`)**  
   - G1 也在准备一个 CAS 操作，它想做的是：  
     - 将等待者计数器减 1；  
     - 将 `mutexWoken` 位置为 1。

6. **竞争结果**  
   G1 和 G2 只有一个能 CAS 成功。由于 G2 的行动包含了加锁，它一旦成功，就意味着它已经成为了新的持锁者。

   - **如果 G2 的 CAS 获胜**  
     - `state` 现在变为：`locked=1`, `starving=1`, `waiters` 被更新。  
     - G2 成功获取了锁，从 `lockSlow` 返回，继续执行它的临界区代码。  
     - 现在轮到 G1，它的 CAS 会失败，因为它基于的旧 `state` 值已经过时了。  
     - G1 的 `unlockSlow` 循环会重新读取 `state`。此时它会读到 `locked=1` 和 `starving=1`。  
     - 于是，`if ... || old&(mutexLocked|...|mutexStarving) != 0` 这个条件成立了。  
     - G1 执行 `return`。

在这个时间点，G1 的 `return` 是完全安全的。因为它返回的前提是它在竞争中“输了”，而“输”的这个事实本身就证明了另一个 goroutine（G2）已经成功地接管了锁。

系统的状态是：

- 锁没有丢失，它被 G2 合法持有。  
- 唤醒等待者的责任，现在也自然而然地落在了新的持锁者 G2 身上（当 G2 未来调用 `Unlock` 时）。

因此，这个 `return` 语句是一个优雅的“退出机制”。它让在竞态中失败的一方（G1）能够干净地放弃任务，因为系统的活性（Liveness）已经由竞态的胜利者（G2）保证了。这避免了死锁，并确保了锁的控制权始终有明确的归属。


### 2.3.3. mutex 饥饿模式，如何保实现抢锁的 gouroutine 公平性
饥饿模式下的公平性，是通过 **Mutex** 的状态机与 **Go** 运行时调度器之间的一个精巧协作来实现的。

其核心可以概括为三点：

1. 一个严格的 FIFO（先进先出）等待队列。
2. 禁止“插队”（Barging）。
3. 锁所有权的 “直接交接”（Direct Handoff）。

---

#### 等待队列的实现：`m.sema`

`Mutex` 结构体内部包含一个 `sema` 字段，它是一个信号量。当一个 goroutine 在 `lockSlow` 中无法获取锁而需要休眠时，它会调用：

```go
runtime_SemacquireMutex(&m.sema, ...)
```

这个调用会请求 Go 的运行时系统：

> “请将我（当前 goroutine）暂停，并放入与 `m.sema` 这个信号量关联的等待队列中。”

Go 运行时的调度器会为每个这样的信号量维护一个 **严格的先进先出（FIFO）队列**。  
这意味着，哪个 goroutine 先调用 `runtime_SemacquireMutex` 进入休眠，它就在队列的更前端。

---

#### 饥饿模式：禁止“插队”

当 `Mutex` 进入饥饿模式后（`state` 的 `mutexStarving` 位置为 1），`lockSlow` 的行为会发生改变：

- **禁止自旋**：新到来的 goroutine 即使满足自旋条件，也会被禁止。
- **必须排队**：新来的 goroutine 不会尝试去竞争锁，而是直接计算新的 `state`（将等待者计数加一），然后调用 `runtime_SemacquireMutex` 将自己添加到等待队列的 **末尾**。

这样确保在饥饿模式下，不会有“幸运”的后来者通过自旋等方式抢先获得锁，所有 goroutine 都必须按先来后到的顺序排队。

---

#### 公平性的核心：锁所有权的 “直接交接”

这是实现公平唤醒的关键。当持有锁的 goroutine 调用 `Unlock`，并且发现 `Mutex` 处于饥饿模式时，`unlockSlow` 函数会执行一个特殊的操作：

- **不完全解锁**：`unlockSlow` 不会将 `mutexLocked` 位置为 `0` 然后就结束。它只是在计算新状态时减去了 `mutexLocked`，真正的魔法在下一步。
- **调用 `runtime_Semrelease` 并请求“交接”**：  

```go
runtime_Semrelease(&m.sema, true, 1)
```

注意第二个参数 `handoff` 被设置为了 `true`。

这个 `handoff = true` 是一个给 Go 运行时调度器的明确指令：

> “我正在释放一个处于饥饿模式的锁。请不要简单地唤醒一个等待者让它去竞争。请直接从 `m.sema` 等待队列的 **队头（Head）** 取出那个等待时间最长的 goroutine，并将锁的所有权直接交给他。”

---

#### 交接过程

1. 调度器收到指令，从 FIFO 队列的头部取出等待最久的 goroutine。
2. 调度器将这个 goroutine 标记为“可运行”状态，并放入处理器的运行队列中。
3. **重要的是**，这个被唤醒的 goroutine 从 `runtime_SemacquireMutex` 调用中返回时，它 **已经被“钦定”** 为新的锁持有者，不需要再次竞争。
4. 被唤醒的 goroutine 继续在 `lockSlow` 中执行，它会进入：

```go
if old & mutexStarving != 0 { ... }
```

然后通过：

```go
delta := int32(mutexLocked - 1<<mutexWaiterShift)
```

这行代码来“修正” `state` —— 即正式将 `mutexLocked` 位置为 `1`，并将等待者计数减 `1`，宣告自己成为新的锁持有者。

---

#### 总结

在饥饿模式下，`Mutex` 从一个“谁快谁先得”的广场，变成了一个纪律严明的银行柜台：

- **排队叫号**：所有想获取锁的 goroutine 都必须去 `m.sema` 队列的末尾排队。
- **柜员服务**：`Unlock` 操作就像柜员办完一个业务，它不会把窗口开放给所有人，而是按下叫号器。
- **精准通知**：调度器作为叫号系统，精确地通知队列最前面的那个人（等待最久的 goroutine）“轮到你了”。
- **直接办理**：被叫到号的 goroutine 直接获得服务（锁的所有权），无需再和别人争抢。

通过这种 **FIFO 队列 + 禁止插队 + 所有权直接交接** 的机制，`sync.Mutex` 在饥饿模式下实现了完美的公平性，确保没有 goroutine 会被无限期地延迟。

### 2.3.4. 允许 goroutine1 调用 Lock，gouroutine2 调用 Unlock 解锁

`Mutex` 的核心代码（`Lock`/`Unlock`）本身，确实没有一个机制去记录和检查“当前是哪个 goroutine 在调用我”。

让我们来分析为什么会这样：

- **`state` 是唯一的事实来源**  
  `Mutex` 的所有操作都围绕着 `state` 这个 `int32` 变量。`Lock` 就是尝试原子地修改 `state`，`Unlock` 也是。这些原子操作本身是独立于 goroutine ID 的。它们只关心 `state` 的当前值和期望的新值。

- **性能考量**  
  如果 `Mutex` 每次加锁时都需要记录当前 goroutine 的 ID，然后在解锁时进行比对，这将引入额外的开销：
  - 需要一个额外的字段来存储 goroutine ID。
  - 每次 `Lock` 和 `Unlock` 都需要调用运行时来获取 goroutine ID。
  
  这会使 `Mutex` 变重，违背了其作为轻量级、高性能同步原语的设计初衷。

所以，您的判断是正确的：从 `Mutex` 自身代码的原子操作层面，它并没有显式地阻止一个 goroutine B 去修改一个由 goroutine A 设置的 `state`。

#### 但是，为什么我们说“不允许”？

这里的“不允许”更多是来自 **Go 语言的设计规范**、**运行时协作** 以及 **逻辑正确性** 这三个层面，而不是 `Mutex` 源码中的一个 `if` 判断。

- **规范层面**  
  Go 语言的内存模型和 `sync` 包的文档都明确或隐含地规定了 `Mutex` 的正确用法是“谁加锁，谁解锁”。滥用它属于“未定义行为”（Undefined Behavior）。

- **运行时协作层面**  
  正如我们之前讨论的，`unlock of unlocked mutex` 这个 `fatal` 错误就是一个例子。虽然 `Unlock` 本身不检查 goroutine ID，但滥用导致的**状态不一致**会被运行时捕获。比如，goroutine A 加锁，goroutine B 解锁，然后 goroutine B 又尝试解锁一次，就会触发这个 `fatal`。这种状态不一致在复杂的滥用场景下几乎必然发生。

- **逻辑正确性层面**  
  这是最重要的一点。`Mutex` 的目的是保护临界区。如果 goroutine A 还在临界区内执行，而 goroutine B 却把门锁打开了，那么 `Mutex` 就完全失去了其存在的意义。程序的正确性将荡然无存。

#### 一个比喻

您可以把 `Mutex` 的 `state` 想象成一个物理的门锁。`Lock` 是用钥匙锁门，`Unlock` 是用钥匙开门。

- `Mutex` 的实现，相当于只关心“有没有一把正确的钥匙能打开或锁上这个锁”，它不关心“拿钥匙的人是谁”。
- 而 Go 的编程规范则告诉你：“只有锁门的那个人，才有资格去开门。你不能把你的钥匙给别人让他去开门，否则屋里的东西丢了（数据竞争），后果自负。”

#### 结论

您的问题非常深刻，它区分了“物理上能否做到”和“规范上是否允许”。

- **物理上**：是的，因为 `Mutex` 的核心是无状态的原子操作，它本身不记录 goroutine ID，所以一个 goroutine B 可以执行一个 `Unlock` 操作，去修改一个由 goroutine A `Lock` 过的 `state`。
- **规范和逻辑上**：这种行为是**绝对禁止**的。它破坏了互斥锁的根本目的，会导致程序状态不可预测，并很可能在运行时的某个环节因状态不一致而崩溃。

因此，我们可以得出结论：Go 的设计者相信程序员会遵守规范，为了追求极致的性能，而没有在 `Mutex` 中加入 goroutine ID 的检查。他们把保证正确使用的责任交给了开发者。

## 2.4. 源码解析
```golang
const (
	mutexLocked = 1 << iota // mutex is locked， value is 1
	mutexWoken              // 2
	mutexStarving           // 4
	mutexWaiterShift = iota // 3
    starvationThresholdNs = 1e6
)    

type Mutex struct {
	state int32
	sema  uint32
}

// Lock 会锁住互斥锁 m。
// 如果该锁已经被使用，调用的 goroutine 会阻塞，直到互斥锁可用。
func (m *Mutex) Lock() {
	// 快速路径（Fast Path）：尝试通过一次原子的“比较并交换”（CAS）操作直接获取锁。
	// 这是针对无竞争或低竞争场景的极致优化。如果 m.state 是 0（未锁定），
	// 则将其原子地设置为 mutexLocked (1)，表示加锁成功。
	// 这个操作非常快，因为它不涉及复杂的调度逻辑。
	if atomic.CompareAndSwapInt32(&m.state, 0, mutexLocked) {
		if race.Enabled {
			race.Acquire(unsafe.Pointer(m))
		}
		return
	}
	// 慢速路径（Slow Path）：如果 CAS 失败，说明锁已被持有，或者存在等待者，
	// 进入一个独立的函数处理复杂的加锁逻辑。
	// 将慢速路径分离出去，有助于编译器对 Lock 函数本身进行内联，从而最大化快速路径的性能。
	m.lockSlow()
}

// TryLock 尝试锁定 m 并报告是否成功。
// 它是一个非阻塞的操作。
//
// 注意：虽然 TryLock 存在正确的使用场景，但这些场景很少见。
// 使用 TryLock 通常是程序设计中存在更深层次问题的信号。
func (m *Mutex) TryLock() bool {
	// 读取当前锁的状态。
	old := m.state
	// 如果锁已被锁定（mutexLocked）或处于饥饿模式（mutexStarving），则立即失败。
	// 在饥饿模式下，新来的 goroutine 必须排队，不能抢锁。
	if old&(mutexLocked|mutexStarving) != 0 {
		return false
	}

	// 尝试通过 CAS 获取锁。即使可能有等待者，当前正在运行的 goroutine
	// 也可以尝试在等待者被唤醒前“闯入”并抢到锁（这属于正常模式的行为）。
	if !atomic.CompareAndSwapInt32(&m.state, old, old|mutexLocked) {
		return false
	}

	if race.Enabled {
		race.Acquire(unsafe.Pointer(m))
	}
	return true
}

// lockSlow 包含了获取互斥锁的复杂逻辑，在快速路径失败时被调用。
func (m *Mutex) lockSlow() {
	var waitStartTime int64 // 开始等待的时间戳，用于计算是否进入饥饿模式
	starving := false      // 当前 goroutine 是否处于饥饿状态
	awoke := false         // 当前 goroutine 是否是被唤醒的
	iter := 0              // 自旋的次数
	old := m.state         // 锁的当前状态

	for {
		// 阶段一：自旋（Spinning）。
		// 条件：锁处于正常模式（非饥饿）、已被锁定，并且 runtime 认为自旋是有意义的。
		// 自旋是一种优化：如果锁很快被释放，忙等待的开销远小于 goroutine 挂起和唤醒的上下文切换开销。
		if old&(mutexLocked|mutexStarving) == mutexLocked && runtime_canSpin(iter) {
			// 尝试设置 mutexWoken 标记，这是一个对 Unlock 的提示：
			// “我（自旋者）将要获取锁，请不要唤醒其他等待者了”，以避免不必要的唤醒。
			if !awoke && old&mutexWoken == 0 && old>>mutexWaiterShift != 0 &&
				atomic.CompareAndSwapInt32(&m.state, old, old|mutexWoken) {
				awoke = true
			}
			runtime_doSpin() // 执行一小段忙等待。
			iter++
			old = m.state // 重新读取状态，继续下一轮循环。
			continue
		}

		// 阶段二：计算新状态并尝试 CAS 更新。
		new := old
		// 如果锁不处于饥饿模式，新状态中要尝试加上锁定标记。
		if old&mutexStarving == 0 {
			new |= mutexLocked
		}
		// 如果锁已被持有（无论何种模式），说明我们将要进入等待，所以等待者计数加一。
		if old&(mutexLocked|mutexStarving) != 0 {
			new += 1 << mutexWaiterShift
		}
		// 如果当前 goroutine 已确认自己处于饥饿状态，并且锁确实还被占着，
		// 那么它有责任将整个互斥锁切换到饥饿模式。
		if starving && old&mutexLocked != 0 {
			new |= mutexStarving
		}
		// 如果当前 goroutine 是被唤醒的，那么它必须清除 mutexWoken 标记。
		if awoke {
			if new&mutexWoken == 0 {
				throw("sync: inconsistent mutex state")
			}
			new &^= mutexWoken
		}

		// 阶段三：执行 CAS 并处理结果。
		if atomic.CompareAndSwapInt32(&m.state, old, new) {
			// CAS 成功，我们成功预留了状态。

			// 如果旧状态是未锁定，说明我们通过 CAS 直接获得了锁，加锁成功，退出。
			if old&(mutexLocked|mutexStarving) == 0 {
				break // locked the mutex with CAS
			}

			// 如果 CAS 成功但锁仍被占用，说明我们已成功将自己计为等待者，现在需要挂起。
			// 如果之前就在等待，则以 LIFO 方式排队（插到队首），以期能更快被唤醒。
			queueLifo := waitStartTime != 0
			if waitStartTime == 0 {
				waitStartTime = runtime_nanotime()
			}
			// 通过信号量将当前 goroutine 挂起。
			runtime_SemacquireMutex(&m.sema, queueLifo, 1)

			// --- 从这里开始，是 goroutine 被唤醒后的逻辑 ---

			// 检查等待时间是否超过阈值，如果超过，则将本 goroutine 标记为饥饿状态。
			starving = starving || runtime_nanotime()-waitStartTime > starvationThresholdNs
			old = m.state // 重新读取锁状态

			// 如果被唤醒时，锁已处于饥饿模式。
			if old&mutexStarving != 0 {
				// 在饥饿模式下，锁的所有权是直接移交的。
				// 此时 state 状态有些不一致：mutexLocked 未设置，但我们已被计为等待者。
				// 这里需要修复状态：加上 mutexLocked，减去一个等待者计数。
				if old&(mutexLocked|mutexWoken) != 0 || old>>mutexWaiterShift == 0 {
					throw("sync: inconsistent mutex state")
				}
				delta := int32(mutexLocked - 1<<mutexWaiterShift)
				// 如果本 goroutine 不再饥饿，或者队列里只剩它一个等待者，
				// 它就有责任将锁切换回正常模式。
				if !starving || old>>mutexWaiterShift == 1 {
					delta -= mutexStarving
				}
				atomic.AddInt32(&m.state, delta)
				break // 成功获取锁，退出。
			}
			awoke = true // 标记自己是被唤醒的。
			iter = 0     // 重置自旋计数。
		} else {
			// CAS 失败，说明有其他 goroutine 修改了状态，重新读取并循环。
			old = m.state
		}
	}

	if race.Enabled {
		race.Acquire(unsafe.Pointer(m))
	}
}

// Unlock 解锁 m。
// 如果 m 在进入 Unlock 时未被锁定，会引发一个运行时错误。
//
// 一个锁定的 Mutex 不与特定的 goroutine 关联。
// 允许一个 goroutine 锁定 Mutex，然后由另一个 goroutine 解锁。
func (m *Mutex) Unlock() {
	if race.Enabled {
		_ = m.state
		race.Release(unsafe.Pointer(m))
	}

	// 快速路径（Fast Path）：通过原子减法，尝试移除锁的 mutexLocked 标记。
	new := atomic.AddInt32(&m.state, -mutexLocked)
	// 如果 new 不为 0，说明还有等待者，或者锁处于其他状态，需要进入慢速路径处理。
	if new != 0 {
		// 慢速路径被分离出去，以利于快速路径的内联。
		m.unlockSlow(new)
	}
}

// unlockSlow 处理解锁时的慢速路径逻辑。
func (m *Mutex) unlockSlow(new int32) {
	// 健壮性检查：不能解锁一个未被锁定的互斥锁。
	if (new+mutexLocked)&mutexLocked == 0 {
		fatal("sync: unlock of unlocked mutex")
	}

	// 如果锁处于正常模式。
	if new&mutexStarving == 0 {
		old := new
		for {
			// 如果没有等待者，或者已经有 goroutine 被唤醒/正在自旋抢锁，
			// 那么就无需再唤醒任何 goroutine。直接返回即可。
			if old>>mutexWaiterShift == 0 || old&(mutexLocked|mutexWoken|mutexStarving) != 0 {
				return
			}
			// 准备唤醒一个等待者：等待者计数减一，并设置唤醒标记。
			new = (old - 1<<mutexWaiterShift) | mutexWoken
			// 通过 CAS 尝试更新状态，如果成功，则通过信号量唤醒一个等待者。
			if atomic.CompareAndSwapInt32(&m.state, old, new) {
				runtime_Semrelease(&m.sema, false, 1)
				return
			}
			// CAS 失败，则重试。
			old = m.state
		}
	} else {
		// 如果锁处于饥饿模式：
		// 直接将锁的所有权移交给下一个等待者。
		// 注意：这里不会设置 mutexLocked，被唤醒的 goroutine 会自己设置。
		// 但因为 mutexStarving 标志存在，新来的 goroutine 不会尝试获取锁。
		// 第二个参数 `true` 告知 runtime 直接将等待者调度到可运行队列。
		runtime_Semrelease(&m.sema, true, 1)
	}
}
```
