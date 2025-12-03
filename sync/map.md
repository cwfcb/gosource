# sync.Map

**目录**
- [1. 概览](#1-概览)
  - [核心功能与设计哲学](#核心功能与设计哲学)
  - [两大优化场景](#两大优化场景)
  - [内部实现简介（“空间换时间”思想）](#内部实现简介空间换时间思想)
  - [对比：sync.Map vs map + sync.RWMutex](#对比syncmap-vs-map--syncrwmutex)
  - [总结](#总结)
- [2. 实现原理](#2-实现原理)
  - [一、核心数据结构](#一核心数据结构)
  - [二、操作流程与生命周期](#二操作流程与生命周期)
  - [三、核心机制：dirty map 的提升 (Promotion)](#三核心机制dirty-map-的提升-promotion)
  - [四、Range 的特殊优化](#四range-的特殊优化)
  - [总结与权衡](#总结与权衡)
- [3. read 和 dirty](#3-read-和-dirty)
  - [1. read 和 dirty 的关系](#1-read-和-dirty-的关系)
  - [2. read 和 dirty 存储的是什么数据？](#2-read-和-dirty-存储的是什么数据)
  - [3. 操作流程的精确描述](#3-操作流程的精确描述)
- [4. 为什么需要 expunged 哨兵指针](#4-为什么需要-expunged-哨兵指针)
- [5. 为什么适合多 Goroutine 操作不相交的键集合场景](#5-为什么适合多-goroutine-操作不相交的键集合场景)
  - [关键机制：对 entry 指针的原子操作](#关键机制对-entry-指针的原子操作)
  - [场景分解：不相交的键集合](#场景分解不相交的键集合)
  - [为什么性能好？](#为什么性能好)
  - [对比 map + sync.RWMutex](#对比-map--syncrwmutex)
  - [总结](#总结-1)
- [6. 源码解读](#6-源码解读)

## 1. 概览

`sync.Map` 是 Go 语言 `sync` 包提供的一个原生支持并发安全的 map 类型。你可以把它理解为一个内置了锁机制的 `map[any]any`，允许多个 Goroutine 同时对它进行读写操作，而不需要开发者手动添加互斥锁（`sync.Mutex`）或读写锁（`sync.RWMutex`）。

### 核心功能与设计哲学

*   **并发安全**：这是它最核心的特性。普通的 Go map 在并发读写时会直接 panic，而 `sync.Map` 内部处理了所有同步细节，保证了原子性和数据一致性。

*   **专门化而非通用**：官方注释明确指出，`sync.Map` 不是用来替代所有 `map` + `Mutex` 场景的。在大多数情况下，使用一个普通的 map 配上一个 `sync.RWMutex` 是更好的选择，因为：
    *   **类型安全**：普通 map 可以指定具体的键值类型（如 `map[string]int`），在编译期就能进行类型检查。而 `sync.Map` 的键和值都是 `any` (interface{}) 类型，存取时都需要进行类型断言，这会增加运行时开销和出错的风险。
    *   **维护关联状态**：当你需要维护一个 map 和其他变量之间的不变性关系时，使用一个统一的锁来保护所有相关的变量会更简单、更清晰。

### 两大优化场景

`sync.Map` 的性能优势主要体现在以下两个特定的高并发场景中，其内部实现也正是为此而优化，旨在减少锁竞争：

1.  **读多写少（特别是只增缓存）**：当一个键值对被写入一次后，会被大量地读取。例如，一个只增不减的缓存系统。在这种场景下，`sync.Map` 内部有一个优化的只读数据结构，可以让多个 Goroutine 同时无锁读取已存在的键，性能极高。只有在写入新键或删除键时才需要加锁操作。

2.  **多 Goroutine 操作不相交的键集合**：当不同的 Goroutine 各自操作自己负责的一组键，这些键的集合互不重叠。在这种情况下，不同 Goroutine 的写操作因为操作的是不同的内部数据分片，所以很少会发生锁竞争。（为什么适用这种场景，[下面第5章节有详细说明](#5-为什么适合多-goroutine-操作不相交的键集合场景)）

### 内部实现简介（“空间换时间”思想）

为了理解它的性能优势，可以简单了解其内部的“读写分离”机制：

`sync.Map` 内部实际上维护了两个 map：

*   `read`：一个只读的 map (`readOnly`)，主要用于并发读取，访问它通常不需要加锁。
*   `dirty`：一个可读写的 map，包含了 `read` 中没有的最新数据。访问 `dirty` 需要加锁。

*   **读取 (Load)**：优先无锁地从 `read` 中查找。如果找不到，再加锁去 `dirty` 中查找。
*   **写入 (Store)**：必须加锁，写入到 `dirty` 中。当 `dirty` 中的数据积累到一定程度或 `read` 中数据过时，会触发一次将 `dirty` 数据“提升”到 `read` 的过程。
*   **删除 (Delete)**：同样需要加锁，在 `dirty` 中标记删除。

正是这种机制，使得在“读多写少”的场景下，绝大多数 `Load` 操作可以无锁完成，从而大大减少了锁竞争，提升了并发性能。

### 对比：sync.Map vs map + sync.RWMutex

| 特性       | sync.Map                               | map + sync.RWMutex                               |
| :--------- | :------------------------------------- | :----------------------------------------------- |
| 并发安全   | 内置，开箱即用                         | 需要手动加锁/解锁                                |
| 类型安全   | 否 (key/value 均为 `any`)              | 是 (可指定具体类型)                              |
| 性能       | 在特定场景下极高（读多写少、键不相交） | 在写操作频繁或锁竞争激烈的场景下，性能可能下降   |
| 易用性     | API 简单（Load, Store, Delete, Range） | 需要开发者自己管理锁的生命周期，容易出错（如忘记解锁） |
| 适用场景   | 只增缓存、分片任务处理等               | 通用业务逻辑，需要维护复杂状态和类型安全的场景   |

### 总结

`sync.Map` 是一个为解决特定高并发性能问题而设计的、高度优化的工具。当你遇到的场景完全符合它所优化的那两种情况（特别是读远多于写），并且可以接受其非类型安全的缺点时，使用 `sync.Map` 可能会带来显著的性能提升。

对于绝大多数常规的并发编程任务，`map` + `sync.RWMutex` 依然是更推荐、更安全、更通用的首选方案。

## 2. 实现原理

`sync.Map` 并非一个简单的 map 加上一个 Mutex，它的目标是在特定的高并发场景下，最大限度地减少锁的竞争。为了实现这一点，它内部设计了相当复杂的机制。

### 一、核心数据结构

`sync.Map` 的 struct 定义是理解一切的起点：

```go
type Map struct {
    mu     Mutex
    read   atomic.Pointer[readOnly]
    dirty  map[any]*entry
    misses int
}
```

这四个字段构成了 `sync.Map` 的骨架：

*   `mu Mutex`: 一个标准的互斥锁。但请注意，它并不保护所有的读写操作。它的核心职责是保护 `dirty` map 的访问和 `read` map 的“指针”更新。

*   `read atomic.Pointer[readOnly]`: 这是实现高性能并发读取的关键。它是一个原子指针，指向一个 `readOnly` 结构体。`readOnly` 内部包含一个普通的 `map[any]*entry`。因为 `read` 是一个指针，所以可以原子地（无锁地）读取和替换它指向的 `readOnly` 实例。所有读操作（`Load`）会首先尝试访问这里的数据，这个过程是完全无锁的，允许多个 Goroutine 同时进行，极大地提高了读性能。

*   `dirty map[any]*entry`: 这是一个普通的 Go map，所有写操作（`Store`/`Delete`）都发生在这里。对 `dirty` map 的任何访问（读或写）都必须持有 `mu` 锁。`dirty` map 就像是 `read` map 的一个“增量更新”或“草稿区”。

*   `misses int`: 一个有趣的性能调优计数器。它记录了“读操作在 `read` map 中未命中，不得不去 `dirty` map 中查找”的次数。当这个计数值达到某个阈值时，就会触发一次重要的数据同步。

### 二、操作流程与生命周期

#### 1. 读取 (Load)

`Load` 操作完美地体现了读写分离的思想：

**快速路径 (Fast Path):**

1.  原子地加载 `read` 指针，获取当前的 `readOnly` map。
2.  直接在这个 `readOnly.m` 中查找 key。
3.  如果找到了，原子地加载 entry 中的值并返回。整个过程完全无锁。

**慢速路径 (Slow Path):**

1.  如果在 `read` map 中没有找到 key，并且 `read.amended` 标记为 true（表示 `dirty` map 中可能有新数据），则进入慢速路径。
2.  加锁 `m.mu.Lock()`。
3.  **双重检查 (Double-Checking)**：为了防止在等待锁的过程中 `dirty` map 已经被提升（promoted）为新的 `read` map，代码会再次从 `read` map 中查找一次。如果找到了，就说明数据已经同步，可以解锁返回了。
4.  如果双重检查后仍然没有，就去 `dirty` map 中查找。
5.  无论在 `dirty` map 中是否找到，都调用 `missLocked()`，将 `misses` 计数器加一。
6.  解锁 `m.mu.Unlock()` 并返回结果。

#### 2. 写入 (Store)

`Store` 操作总是作用于 `dirty` map，逻辑相对直接：

1.  加锁 `m.mu.Lock()`。
2.  检查 `dirty` map 是否为 nil。如果是，说明这是自上次 `dirty` map 被清空后的第一次写入。此时会调用 `dirtyLocked()`。
3.  `dirtyLocked()` 会基于当前的 `read` map 创建一个新的 `dirty` map，将 `read` map 中所有未被删除的条目浅拷贝过来。
4.  同时，原子地更新 `read` 指针，将其中的 `amended` 标志位设为 `true`，告知所有 `Load` 操作：“`read` map 的数据已不完整，你们可能需要检查 `dirty` map”。
5.  在 `dirty` map 中存入新的键值对。如果 key 已存在，则更新；如果不存在，则创建新的 `entry`。
6.  解锁 `m.mu.Unlock()`。

#### 3. 删除 (Delete)

删除是一个逻辑上的概念，通过特殊的指针状态实现，避免了物理上的 map 元素移除带来的性能抖动。

`entry` 内部的 `p` 指针有三种状态：

*   指向实际值：正常状态。
*   `nil`: 表示条目已被删除，但仍在 `read` map 中。
*   `expunged`: 一个特殊的哨兵指针，表示条目不仅被删除，而且已不在 `dirty` map 中。

`Delete` 操作会原子地将 `entry` 的 `p` 指针设置为 `nil`。这个条目在物理上仍然存在于 `read` map 中，但 `load` 操作会将其识别为已删除。

当 `dirtyLocked()` 从 `read` map 拷贝数据创建新的 `dirty` map 时，它会跳过那些 `p` 指针为 `nil` 或 `expunged` 的条目，从而实现了物理上的清理。

### 三、核心机制：dirty map 的提升 (Promotion)

这是 `sync.Map` 的精髓所在，由 `missLocked()` 函数实现。

*   **触发时机**: 当 `misses` 计数器的值增长到等于或超过 `dirty` map 的长度时 (`m.misses >= len(m.dirty)`)。
*   **核心思想**: 这个条件意味着，为了查找 `dirty` map 中的新数据，慢速路径的加锁开销已经累积得足够多，足以“摊销”掉一次性将 `dirty` map 整体同步到 `read` map 的成本了。
*   **执行过程** (在 `mu` 锁的保护下):
    1.  将当前的 `dirty` map 直接变成新的 `readOnly` map。
    2.  通过 `m.read.Store()` 原子地将 `read` 指针指向这个新的 `readOnly` map。
    3.  将 `m.dirty` 设置为 `nil`。
    4.  将 `m.misses` 计数器重置为 0。

完成这次提升后，之前所有在 `dirty` map 中的新数据，现在都可以在 `read` map 中被快速、无锁地访问了。下一次 `Store` 操作会再次触发 `dirtyLocked()`，开启新一轮的循环。

### 四、Range 的特殊优化

`Range` 操作有一个非常聪明的优化。当它发现 `read.amended` 为 `true` 时，它会意识到 `dirty` map 中有必须遍历的数据。`Range` 操作本身的时间复杂度就是 O(N)，这个成本足以覆盖一次 map 的复制。因此，它会立即加锁，将 `dirty` map 提升为新的 `read` map，然后对这个最新的、完整的 `read` map 进行遍历。这相当于搭了一次“顺风车”，在遍历的同时完成了数据同步。

### 总结与权衡

`sync.Map` 的实现原理是一个典型的用空间换时间、读写分离、乐观锁与悲观锁结合的范例。

**优点:**

*   在读多写少或多 Goroutine 操作不相交键集合的场景下，由于大量读操作可以无锁进行，极大地减少了锁竞争，并发性能远超 `map` + `RWMutex`。
*   内部的 `misses` 计数器和提升机制实现了自适应的性能调优。

**缺点/权衡:**

*   **空间换时间**: 同时维护 `read` 和 `dirty` 两个 map，带来了更高的内存占用。
*   **类型不安全**: key 和 value 都是 `any` 类型，需要类型断言，牺牲了编译期类型检查。
*   **更复杂的逻辑**: 相比简单的 `map` + `Mutex`，其内部实现复杂，且在某些场景下（如写多读少）性能可能更差，因为写操作的路径更长，且 `dirty` map 的提升也有开销。

因此，`sync.Map` 是一个为特定场景打造的“手术刀”，而不是一把通用的“瑞士军刀”。

## 3. read 和 dirty

### 1. read 和 dirty 的关系

`read` 和 `dirty` 是 `sync.Map` 实现“读写分离”策略的两个核心组件，它们的关系可以概括为：`read` 提供无锁的快速读取路径，`dirty` 处理所有写入和慢速读取路径，并通过一个“提升”过程将变更同步给 `read`。

*   **`read` 字段**
    *   **角色**: “只读”数据副本，专为快速、无锁的并发读取设计。
    *   **结构**: 它是一个 `atomic.Pointer`，指向一个 `readOnly` 结构体。`readOnly` 内部包含一个普通的 `map[any]*entry`，存储了 map 的一部分或全部数据。
    *   **工作方式**: `Load` 操作会首先原子性地加载这个 `readOnly` 指针，并直接访问其内部的 map。因为 Go 保证 map 的读取在没有写入的情况下是并发安全的，且 `read` 指向的 map 永不被修改，所以这个过程无需加锁，速度极快。

*   **`dirty` 字段**
    *   **角色**: “读写”数据副本，是所有变更的权威来源。
    *   **结构**: 这是一个常规的 `map[any]*entry`。
    *   **工作方式**: 所有的写入操作（`Store`, `Delete` 等）都必须先获取互斥锁 `mu`，然后对 `dirty` map 进行修改。如果一个 `Load` 操作在 `read` 中没有找到键（称为 "miss"），它也必须获取锁 `mu`，然后再次从 `dirty` 中查找。

**两者关系与数据流**

*   **写入**: `Store` 操作总是在 `mu` 锁的保护下写入 `dirty`。如果 `dirty` 为 `nil`（通常在一次提升之后），它会先将 `read` 中的数据完整地复制过来，然后再执行写入。
*   **读取**: `Load` 操作首先无锁地访问 `read`。如果命中，直接返回（快速路径）。如果未命中，则进入慢速路径：加锁 `mu`，然后从 `dirty` 中查找。
*   **同步（提升）**: 当慢速路径的读取次数（由 `misses` 字段统计）积累到一定阈值（等于 `dirty` map 的长度）时，意味着 `read` 已经“过时”了，大量读取都不得不走慢速路径，抵消了 `read` 带来的性能优势。此时会触发一次“提升”：
    1.  `dirty` map 会被原子性地存储到 `read` 字段中，成为新的“只读”副本。`m.read.Store(&readOnly{m: m.dirty})` 正是这个关键步骤。
    2.  旧的 `read` map 会被垃圾回收。
    3.  `dirty` 字段被置为 `nil`。
    4.  `misses` 计数器清零。

**总结**: `read` 是 `dirty` 在某个时间点的一个不可变的快照。`dirty` 负责累积所有修改，`read` 负责提供高效的并发读取。通过 `misses` 计数器驱动的“提升”机制，`dirty` 中的新数据会周期性地发布为新的 `read` 快照，从而在“读多写少”的场景下，将加锁的成本分摊到少数写操作和周期性的一次提升操作上，最大化了读操作的性能。

### 2. read 和 dirty 存储的是什么数据？

这并不是一个简单的“各存一部分”的关系。更准确的描述是：

*   **`read`**: 存储的是一个只读的、可能过时的数据快照。一旦一个 map 被赋给 `read`，它就永远不会被修改。这正是它可以被无锁并发读取的根本原因。
*   **`dirty`**: 存储的是最新的、完整的数据。所有的写入操作（增、删、改）都发生在这里。当 `dirty` map 存在时，它包含了 `read` map 中的所有数据，以及自上次 `read` map 更新以来的所有新变更。因此，`dirty` 才是数据的“权威来源”。

所以，它们不是各存一部分拼成一个整体，而是 `read` 是 `dirty` 在某个过去时间点的只读副本。

### 3. 操作流程的精确描述

**读取 (Load):**

*   **快速路径 (Fast Path)**: 优先无锁地从 `read` 中读取。如果找到了，并且这个条目没有被标记为 `expunged`（已删除），就直接返回。这是最快的情况。
*   **慢速路径 (Slow Path)**: 如果在 `read` 中没找到，才会去加锁（`m.mu.Lock()`），然后从 `dirty` map 中再次查找。这是为了获取最新的数据。同时，`misses` 计数器会加一，用于判断 `read` 是否“过时”得太厉害。

**写入 (Store / Delete):**

*   **从不操作 `read`**: 写入操作永远不会直接修改 `read` 指向的 map。
*   **必须操作 `dirty`**: 写入操作总是先加锁（`m.mu.Lock()`），然后对 `dirty` map 进行修改。
*   **`dirty` 的初始化**: 如果此时 `dirty` map 是 `nil`（意味着刚刚完成了一次“提升”，或者这是第一次写入），它会先将 `read` 中的所有数据完整地复制一份过来作为自己的初始内容，然后再应用本次的写入。

#### 总结一下:

`read` 的存在就是为了实现无锁的快速读取。但关键点在于，这种快速读取是以数据可能不是最新的为代价的。`sync.Map` 的整个设计就是一场精妙的平衡：

*   通过“读写分离”，让绝大多数的读操作（假设是读多写少的场景）能够走上无锁的快车道。
*   写操作和少数“穿透”到 `dirty` 的读操作则承担了加锁的成本。
*   通过 `misses` 计数器和“提升”机制，动态地用包含新数据的 `dirty` map去更新 `read` map，防止 `read` 过度陈旧，保证了性能不会在持续写入后无限退化。

**如果 `dirty` map 存在（不为 `nil`），那么 `read` map 中的所有未被删除的键值对，也必然存在于 `dirty` map 中**。

`sync.Map` 的 `Load` 操作正是依赖这个逻辑：

1.  先无锁查 `read`。
2.  如果 `read` 中没有，则加锁。
3.  加锁后，为了防止在加锁过程中 `read` 已经被替换，它会再次检查一遍 `read`。
4.  如果新的 `read` 还是没有，它才会去查 `dirty` map（`e, ok = m.dirty[key]`）。此时，如果 `dirty` 中也没有，那它就可以确认这个 key 是真的不存在了。

## 4. 为什么需要 expunged 哨兵指针

`expunged` 是一个特殊的哨兵值（tombstone），用于解决 `Delete` 操作中的一个棘手的并发问题，即如何在一个双 map 结构中安全地删除一个可能同时存在于 `read` 和 `dirty` 中的键。

**问题场景**: 假设一个键 `k` 同时存在于 `read` 和 `dirty` 中（因为 `dirty` 是从 `read` 复制而来的）。现在一个 Goroutine 调用 `Delete(k)`。

`Delete` 操作会加锁 `mu` 并操作 `dirty` map。

如果我们直接从 `dirty` 中删除这个键（`delete(m.dirty, k)`），会产生一个竞态条件：

1.  一个并发的 `Load(k)` 操作，它在 `read` 中未命中（可能因为 `read` 还没更新）。
2.  `Load` 接着加锁 `mu` 去检查 `dirty`。
3.  如果在 `Load` 加锁之前，`Delete` 操作已经完成了 `delete(m.dirty, k)` 并释放了锁。
4.  那么 `Load` 在 `dirty` 中也找不到 `k`，它会错误地返回 `(nil, false)`，即键不存在。
5.  但此时，其他的 Goroutine 仍然可以从旧的 `read` map 中无锁地读到 `k` 的旧值！这就造成了数据不一致。

**`expunged` 的解决方案**: `expunged` 作为 `entry` 的值指针，优雅地解决了这个问题。它是一个全局唯一的指针，`var expunged = new(any)`。

*   **逻辑删除**: 当 `Delete(k)` 发现键 `k` 存在于 `read` map 中时，它不会从 `dirty` map 中物理删除这个键。相反，它会通过原子操作将 `dirty` map 中该键对应的 `entry` 的值指针 `p` 设置为 `expunged`。这相当于给这个 `entry` 打上了一个“已删除”的逻辑标记。

*   **最终的物理删除——通过“提升”完成**: 在 `dirty` map “提升”为新的 `read` map 的过程中，所有贴着 `expunged` 标签的条目都会被忽略和过滤掉。这样，被删除的键就彻底从新的只读快照中消失了，完成了垃圾回收。

**协同工作:**

*   **`Load` (慢速路径)**: 当 `Load` 在 `dirty` 中找到一个 `entry` 时，它会检查其值指针 `p`。如果 `p` 指向 `expunged`，`Load` 就知道这个键已经被删除了，即使它还存在于 `read` 中。此时 `Load` 会正确地返回 `(nil, false)`。
*   **`Store`**: 如果 `Store` 操作要更新一个键，而这个键在 `dirty` 中被标记为 `expunged`，`Store` 可以安全地用新值的指针替换掉 `expunged` 指针，相当于“复活”了这个 `entry`。
*   **提升过程**: 在 `dirty` map 提升为新的 `read` map 时，遍历 `dirty` 的代码会跳过所有值为 `expunged` 的 `entry`。这样，被删除的键就自然地从新的 `read` map 中消失了，完成了最终的物理删除。

**总结**: `expunged` 是一个逻辑删除标记，它充当了 `Delete` 操作和并发 `Load` 操作之间的同步信使。它确保了即使在 `read` 和 `dirty` 存在短暂数据不一致的窗口期内，对键状态（是否存在）的判断依然是正确的，直到下一次 map 提升完成数据同步。这是实现 `sync.Map` 并发正确性的一个精妙设计。

## 5. 为什么适合多 Goroutine 操作不相交的键集合场景

“多 Goroutine 操作不相交的键集合”这个场景，性能好的核心原因是：一旦键被“预热”并进入 `read` map，后续对这些不同键的更新操作将几乎完全在无锁状态下，通过对各个键独立的原子操作完成，从而实现了真正的并行写入。

让我们深入到实现细节来理解“为什么”：

### 关键机制：对 entry 指针的原子操作

我们之前讨论了 `read` 和 `dirty`。但还有一个关键结构是 `entry`，它是一个指针，指向实际存储的值。

```go
type entry struct {
    // p指向一个any类型的值。
    // 如果p被标记为expunged，表示这个条目已被删除，并且m.dirty不为nil。
    // 如果p为nil，表示这个条目已被删除，并且m.dirty为nil。
    // 否则，p指向key对应的值。
    p atomic.Pointer[any]
}
```

`sync.Map` 的 `Store` 方法有一个非常重要的快速路径：如果一个键已经存在于 `read` map 中，它不会去加 `mu` 锁，而是尝试直接通过原子操作更新这个 `entry` 的指针 `p`。

### 场景分解：不相交的键集合

假设我们有两个 Goroutine：

*   Goroutine A 只操作键 `"user:1"`
*   Goroutine B 只操作键 `"user:2"`

它们的执行流程会是这样：

1.  **冷启动阶段（首次写入）**
    *   A 调用 `Store("user:1", dataA)`。`read` 中没有这个键。
    *   A 获得 `mu` 锁，将 `"user:1"` 和对应的 `entry` 写入 `dirty` map。然后释放锁。（这里是慢的，因为加锁了）
    *   B 调用 `Store("user:2", dataB)`。`read` 中也没有。
    *   B 获得 `mu` 锁，将 `"user:2"` 和对应的 `entry` 写入 `dirty` map。然后释放锁。（这里也是慢的）

2.  **“提升”阶段**
    *   经过几次 `Load` 操作的 "miss" 之后，`dirty` map 被提升为新的 `read` map。
    *   现在，`read` map 中同时包含了 `"user:1"` 和 `"user:2"` 的 `entry`。

3.  **热路径阶段（后续写入，性能优势的来源）**
    *   A 再次调用 `Store("user:1", newDataA)`。
    *   它在 `read` map 中无锁地找到了 `"user:1"` 对应的 `entry`。
    *   它直接对这个 `entry` 的 `p` 指针执行原子性的 `CompareAndSwap` 操作，将指针从旧的 `dataA` 地址换成新的 `newDataA` 地址。
    *   **全程没有使用 `mu` 锁！**
    *   与此同时，B 再次调用 `Store("user:2", newDataB)`。
    *   它也在 `read` map 中无锁地找到了 `"user:2"` 对应的 `entry`。
    *   它也对它自己的 `entry` 的 `p` 指针执行原子性的 `CompareAndSwap`。
    *   **全程也没有使用 `mu` 锁！**

### 为什么性能好？

因为 Goroutine A 和 Goroutine B 操作的是不同的 `entry`，它们各自的原子操作是针对不同的内存地址（`entry1.p` 和 `entry2.p`）进行的。在现代多核 CPU 架构下，这种对不同内存地址的原子操作基本上可以无冲突地并行执行。

它们避免了所有 Goroutine 都必须争抢同一个全局 `mu` 锁的巨大瓶颈。

### 对比 map + sync.RWMutex

现在对比一下传统的 `map` + `sync.RWMutex` 方案：

*   A 要写 `"user:1"`，它必须调用 `mu.Lock()`。
*   B 要写 `"user:2"`，它也必须调用 `mu.Lock()`。

因为锁是全局的，A 和 B 的写操作必须串行执行，即使它们操作的是完全不相干的数据。一个必须等待另一个完成。

### 总结

“多 Goroutine 操作不相交的键集合”之所以在 `sync.Map` 中性能优越，是因为 `sync.Map` 将全局锁的竞争转化为了对各个键值对 `entry` 内部指针的原子操作竞争。当键集合不相交时，这种竞争就完全消失了，使得写操作得以在多个 Goroutine 间实现高度并行化。

这正是 `sync.Map` 作为一个“特种兵”而非“通用兵”的体现，它为这种特殊的并发写入模式提供了极致的优化。

`sync.Map` 在这个场景下高性能的本质。

*   **全局悲观锁**: `map` + `sync.RWMutex` 方案就是一把全局的“悲观锁”。它假设总会有并发冲突，所以任何写入（无论是否冲突）都必须先排队拿到唯一的锁。

*   **每个 Key 的乐观锁**: `sync.Map` 则乐观地假设：对于一个已经存在于 `read` map 中的 key，可以直接通过 CAS 原子操作更新它的值，而不会和其它 key 的更新产生冲突。
    *   **乐观尝试**: `Store` 操作首先尝试这个无锁的原子更新。
    *   **成功**: 如果键存在且未被修改，CAS 成功，操作完成。这就是乐观锁的成功路径。
    *   **失败/回退**: 如果键不存在于 `read` map 中（需要创建新键），或者 CAS 失败（虽然在 `Store` 中不常见，但在 `CompareAndSwap` 等方法中是核心），它才会回退到加全局 `mu` 锁的“悲观”路径，去操作 `dirty` map。

将复杂的实现细节提炼成了“从全局大锁降级为每个 key 的乐观锁”，这完全抓住了 `sync.Map` 针对“不相交键集合”场景进行优化的核心思想。当 Goroutine 操作的键不相交时，它们各自的“乐观锁”（CAS操作）永远不会互相干扰，从而实现了接近理想的并行写入，性能自然得到巨大提升。

## 6. 源码解读
基于 go1.22.12
```go
package sync

import (
	"sync/atomic"
)

// Map 类似于 Go 的 map[any]any，但可以安全地被多个 goroutine 并发使用，
// 无需额外的锁或协调。
// Load、Store 和 Delete 操作在摊销后的常数时间内完成。
//
// Map 类型是高度特化的。大多数代码应该使用普通的 Go map，并辅以独立的
// 锁或协调机制，这样可以获得更好的类型安全性，也更容易维护 map 内容之外的
// 其他不变量。
//
// Map 类型针对两种常见用例进行了优化：
// 1. 当一个给定的键的条目只被写入一次但被读取多次时，例如只增的缓存。
// 2. 当多个 goroutine 读取、写入和覆盖不同键集合的条目时。
// 在这两种情况下，使用 Map 可以显著减少与使用普通 map 加上一个独立的
// Mutex 或 RWMutex 相比的锁竞争。
//
// 零值的 Map 是空的，并且可以直接使用。Map 在首次使用后不能被复制。
//
// 在 Go 内存模型的术语中，Map 会安排一个写操作“同步于”任何观察到该写操作
// 效果的读操作之前。读写操作定义如下：
// Load, LoadAndDelete, LoadOrStore, Swap, CompareAndSwap, 和 CompareAndDelete 是读操作；
// Delete, LoadAndDelete, Store, 和 Swap 是写操作；
// 当 LoadOrStore 返回的 loaded 为 false 时，它是一个写操作；
// 当 CompareAndSwap 返回的 swapped 为 true 时，它是一个写操作；
// 当 CompareAndDelete 返回的 deleted 为 true 时，它是一个写操作。
type Map struct {
	// mu 是一个互斥锁，用于保护 dirty map。
	mu Mutex

	// read 存储了 map 内容中可以安全地进行并发访问的部分（无论是否持有 mu 锁）。
	// read 字段本身总是可以安全地加载，但只能在持有 mu 锁的情况下进行存储。
	// 存储在 read 中的条目可以在没有 mu 锁的情况下并发更新，但更新一个
	// 之前被标记为“已清除”（expunged）的条目需要先将该条目复制到 dirty map 中，
	// 并在持有 mu 锁的情况下取消其“已清除”状态。
	read atomic.Pointer[readOnly]

	// dirty 包含了 map 内容中需要持有 mu 锁才能访问的部分。
	// 为了确保 dirty map 可以被快速地提升为 read map，它也包含了 read map 中
	// 所有未被清除的条目。
	//
	// 已清除的条目不会存储在 dirty map 中。在 clean map（即 read map）中一个
	// 已清除的条目必须先被取消清除状态并添加到 dirty map 中，然后才能为其存储新值。
	//
	// 如果 dirty map 是 nil，下一次对 map 的写操作将通过对 clean map 进行浅拷贝
	// 来初始化它，并忽略掉过时的条目。
	dirty map[any]*entry

	// misses 记录了自 read map 上次更新以来，需要锁定 mu 来确定键是否存在的
	// Load 操作的次数。
	//
	// 一旦发生了足够多的 misses，足以覆盖复制 dirty map 的成本，dirty map 就
	// 会被提升为 read map（在未修改状态下），并且下一次对 map 的 Store 操作
	// 将会创建一个新的 dirty map 副本。
	misses int
}

// readOnly 是一个不可变的结构体，原子地存储在 Map.read 字段中。
type readOnly struct {
	// m 是一个普通的 map，用于快速、无锁的读取。
	m map[any]*entry
	// amended 为 true 表示 dirty map 中包含了一些 m 中没有的键。
	amended bool
}

// expunged 是一个任意的指针，用于标记那些已经从 dirty map 中删除的条目。
// 它是一个哨兵值，表示一个条目已被删除，并且该条目不在 dirty map 中。
var expunged = new(any)

// entry 是 map 中对应于特定键的一个槽位。
type entry struct {
	// p 指向为该条目存储的 interface{} 值。
	//
	// 如果 p == nil，表示该条目已被删除，并且此时要么 m.dirty == nil，要么 m.dirty[key] 就是 e。
	//
	// 如果 p == expunged，表示该条目已被删除，m.dirty != nil，并且该条目不在 m.dirty 中。
	//
	// 否则，该条目是有效的，并记录在 m.read.m[key] 中，如果 m.dirty != nil，也记录在 m.dirty[key] 中。
	//
	// 一个条目可以通过原子地替换 p 为 nil 来删除：当 m.dirty 下次被创建时，它会原子地
	// 将 nil 替换为 expunged，并保持 m.dirty[key] 未设置。
	//
	// 只要 p != expunged，条目的关联值就可以通过原子替换来更新。如果 p == expunged，
	// 只有在首先设置 m.dirty[key] = e 之后，才能更新条目的关联值，这样使用 dirty map
	// 的查找才能找到该条目。
	p atomic.Pointer[any]
}

// newEntry 创建一个新的 entry，并将值 i 存入其中。
func newEntry(i any) *entry {
	e := &entry{}
	e.p.Store(&i)
	return e
}

// loadReadOnly 原子地加载 readOnly 字段。
func (m *Map) loadReadOnly() readOnly {
	if p := m.read.Load(); p != nil {
		return *p
	}
	return readOnly{}
}

// Load 返回 map 中指定键的值，如果值不存在则返回 nil。
// ok 结果表示是否在 map 中找到了该值。
func (m *Map) Load(key any) (value any, ok bool) {
	// 快速路径：尝试从 read map 中无锁读取。
	read := m.loadReadOnly()
	e, ok := read.m[key]
	// 如果在 read map 中没找到，并且 read map 的数据不完整（amended 为 true），则进入慢速路径。
	if !ok && read.amended {
		m.mu.Lock()
		// 双重检查：再次从 read map 读取，因为在我们等待锁的期间，dirty map 可能已经被提升为 read map。
		read = m.loadReadOnly()
		e, ok = read.m[key]
		// 如果还是没找到，并且 read map 依然不完整，则从 dirty map 中查找。
		if !ok && read.amended {
			e, ok = m.dirty[key]
			// 无论是否找到，都记录一次 miss。这次 miss 表明该键将一直走慢速路径，
			// 直到 dirty map 被提升为 read map。
			m.missLocked()
		}
		m.mu.Unlock()
	}
	if !ok {
		return nil, false
	}
	// 从 entry 中加载实际的值。
	return e.load()
}

// load 从 entry 中原子地加载值。
func (e *entry) load() (value any, ok bool) {
	p := e.p.Load()
	// 如果指针是 nil 或 expunged，表示条目已被删除。
	if p == nil || p == expunged {
		return nil, false
	}
	// 返回指针指向的实际值。
	return *p, true
}

// Store 设置一个键值对。它实际上是 Swap 的一个简单封装。
func (m *Map) Store(key, value any) {
	_, _ = m.Swap(key, value)
}

// tryCompareAndSwap 尝试原子地比较并交换 entry 的值。
// 如果 entry 的当前值等于 old，并且 entry 未被清除，则将其替换为 new。
// 如果 entry 已被清除，则返回 false 并且不修改 entry。
func (e *entry) tryCompareAndSwap(old, new any) bool {
	p := e.p.Load()
	// 如果 p 是 nil、expunged，或者指向的值不等于 old，则操作失败。
	if p == nil || p == expunged || *p != old {
		return false
	}

	// 为了优化逃逸分析，在第一次加载后才复制接口。
	// 如果一开始比较就失败了，我们就不应该为存储分配一个接口值的堆内存。
	nc := new
	for {
		// 尝试用原子 CAS 操作更新指针。
		if e.p.CompareAndSwap(p, &nc) {
			return true
		}
		// 如果 CAS 失败，重新加载 p，并再次检查条件。
		p = e.p.Load()
		if p == nil || p == expunged || *p != old {
			return false
		}
	}
}

// unexpungeLocked 确保条目不被标记为 expunged。
// 如果条目之前是 expunged 状态，它必须在 m.mu 解锁前被添加到 dirty map 中。
// 返回值表示该条目之前是否是 expunged 状态。
func (e *entry) unexpungeLocked() (wasExpunged bool) {
	// 尝试用原子 CAS 操作将 expunged 状态替换为 nil。
	return e.p.CompareAndSwap(expunged, nil)
}

// swapLocked 无条件地将一个值交换到 entry 中。
// 调用此函数时，必须已知该 entry 不是 expunged 状态。
func (e *entry) swapLocked(i *any) *any {
	return e.p.Swap(i)
}

// LoadOrStore 如果键存在，则返回现有值。
// 否则，它存储并返回给定的值。
// loaded 结果为 true 表示值是加载的，false 表示是存储的。
func (m *Map) LoadOrStore(key, value any) (actual any, loaded bool) {
	// 快速路径：如果是一个干净的命中（在 read map 中），避免加锁。
	read := m.loadReadOnly()
	if e, ok := read.m[key]; ok {
		// 尝试在 entry 上执行加载或存储。
		actual, loaded, ok := e.tryLoadOrStore(value)
		if ok {
			return actual, loaded
		}
	}

	// 慢速路径：加锁处理。
	m.mu.Lock()
	read = m.loadReadOnly()
	if e, ok := read.m[key]; ok {
		// 如果条目在 read map 中，但可能是 expunged 状态。
		if e.unexpungeLocked() {
			// 如果它之前是 expunged，现在恢复了，需要把它加入 dirty map。
			m.dirty[key] = e
		}
		// 再次尝试加载或存储。
		actual, loaded, _ = e.tryLoadOrStore(value)
	} else if e, ok := m.dirty[key]; ok {
		// 如果条目在 dirty map 中。
		actual, loaded, _ = e.tryLoadOrStore(value)
		// 记录一次 miss，因为我们走了慢速路径。
		m.missLocked()
	} else {
		// 如果条目完全不存在。
		if !read.amended {
			// 这是第一次向 dirty map 添加新键。
			// 确保 dirty map 已分配，并将 read map 标记为不完整。
			m.dirtyLocked()
			m.read.Store(&readOnly{m: read.m, amended: true})
		}
		// 在 dirty map 中创建新条目。
		m.dirty[key] = newEntry(value)
		actual, loaded = value, false
	}
	m.mu.Unlock()

	return actual, loaded
}

// tryLoadOrStore 原子地加载或存储一个值，前提是 entry 未被清除。
// 如果 entry 已被清除，则不改变 entry 并返回 ok==false。
func (e *entry) tryLoadOrStore(i any) (actual any, loaded, ok bool) {
	p := e.p.Load()
	if p == expunged {
		return nil, false, false
	}
	if p != nil {
		return *p, true, true
	}

	// 为了优化逃逸分析，在第一次加载后才复制接口。
	ic := i
	for {
		// 如果 p 是 nil，尝试用 CAS 存储新值。
		if e.p.CompareAndSwap(nil, &ic) {
			return i, false, true
		}
		p = e.p.Load()
		if p == expunged {
			return nil, false, false
		}
		if p != nil {
			return *p, true, true
		}
	}
}

// LoadAndDelete 删除一个键的值，并返回之前的值（如果存在）。
// loaded 结果报告该键是否存在。
func (m *Map) LoadAndDelete(key any) (value any, loaded bool) {
	read := m.loadReadOnly()
	e, ok := read.m[key]
	// 如果在 read map 中没找到，并且 read map 不完整，则走慢速路径。
	if !ok && read.amended {
		m.mu.Lock()
		read = m.loadReadOnly()
		e, ok = read.m[key]
		if !ok && read.amended {
			// 从 dirty map 中查找并删除。
			e, ok = m.dirty[key]
			delete(m.dirty, key)
			// 记录一次 miss。
			m.missLocked()
		}
		m.mu.Unlock()
	}
	if ok {
		// 如果找到了 entry，调用其 delete 方法。
		return e.delete()
	}
	return nil, false
}

// Delete 删除一个键的值。
func (m *Map) Delete(key any) {
	m.LoadAndDelete(key)
}

// delete 原子地删除 entry 的值。
func (e *entry) delete() (value any, ok bool) {
	for {
		p := e.p.Load()
		if p == nil || p == expunged {
			return nil, false
		}
		// 尝试用 CAS 将指针设置为 nil。
		if e.p.CompareAndSwap(p, nil) {
			return *p, true
		}
	}
}

// trySwap 尝试交换一个值，前提是 entry 未被清除。
// 如果 entry 已被清除，返回 false 并且不修改 entry。
func (e *entry) trySwap(i *any) (*any, bool) {
	for {
		p := e.p.Load()
		if p == expunged {
			return nil, false
		}
		// 尝试用 CAS 交换指针。
		if e.p.CompareAndSwap(p, i) {
			return p, true
		}
	}
}

// Swap 交换一个键的值，并返回之前的值（如果存在）。
// loaded 结果报告该键是否存在。
func (m *Map) Swap(key, value any) (previous any, loaded bool) {
	read := m.loadReadOnly()
	// 快速路径：尝试在 read map 中直接交换。
	if e, ok := read.m[key]; ok {
		if v, ok := e.trySwap(&value); ok {
			if v == nil {
				return nil, false
			}
			return *v, true
		}
	}

	// 慢速路径：加锁处理。
	m.mu.Lock()
	read = m.loadReadOnly()
	if e, ok := read.m[key]; ok {
		if e.unexpungeLocked() {
			// 如果条目之前是 expunged，现在恢复了，需要把它加入 dirty map。
			m.dirty[key] = e
		}
		// 在锁的保护下交换值。
		if v := e.swapLocked(&value); v != nil {
			loaded = true
			previous = *v
		}
	} else if e, ok := m.dirty[key]; ok {
		// 如果条目在 dirty map 中，直接交换。
		if v := e.swapLocked(&value); v != nil {
			loaded = true
			previous = *v
		}
	} else {
		// 如果条目完全不存在，则创建新条目。
		if !read.amended {
			m.dirtyLocked()
			m.read.Store(&readOnly{m: read.m, amended: true})
		}
		m.dirty[key] = newEntry(value)
	}
	m.mu.Unlock()
	return previous, loaded
}

// CompareAndSwap 如果 map 中存储的值等于 old，则交换新旧值。
// old 值必须是可比较的类型。
func (m *Map) CompareAndSwap(key, old, new any) bool {
	read := m.loadReadOnly()
	// 快速路径：尝试在 read map 中直接 CAS。
	if e, ok := read.m[key]; ok {
		return e.tryCompareAndSwap(old, new)
	} else if !read.amended {
		// 如果 read map 是完整的但没有这个键，那它肯定不存在。
		return false
	}

	// 慢速路径：加锁处理。
	m.mu.Lock()
	defer m.mu.Unlock()
	read = m.loadReadOnly()
	swapped := false
	if e, ok := read.m[key]; ok {
		swapped = e.tryCompareAndSwap(old, new)
	} else if e, ok := m.dirty[key]; ok {
		swapped = e.tryCompareAndSwap(old, new)
		// 记录一次 miss，以促使 dirty map 最终被提升。
		m.missLocked()
	}
	return swapped
}

// CompareAndDelete 如果键的值等于 old，则删除该条目。
// old 值必须是可比较的类型。
// 如果 map 中没有该键的当前值，则返回 false。
func (m *Map) CompareAndDelete(key, old any) (deleted bool) {
	read := m.loadReadOnly()
	e, ok := read.m[key]
	// 慢速路径逻辑
	if !ok && read.amended {
		m.mu.Lock()
		read = m.loadReadOnly()
		e, ok = read.m[key]
		if !ok && read.amended {
			e, ok = m.dirty[key]
			m.missLocked()
		}
		m.mu.Unlock()
	}
	// 循环 CAS 删除
	for ok {
		p := e.p.Load()
		if p == nil || p == expunged || *p != old {
			return false
		}
		if e.p.CompareAndSwap(p, nil) {
			return true
		}
	}
	return false
}

// Range 对 map 中存在的每个键值对顺序调用 f 函数。
// 如果 f 返回 false，则停止迭代。
//
// Range 不一定对应于 Map 内容的任何一致性快照：没有键会被访问超过一次，
// 但如果任何键的值被并发地存储或删除（包括由 f 本身引起），Range 可能会
// 反映 Range 调用期间该键的任何映射。Range 不会阻塞接收器上的其他方法；
// 甚至 f 本身也可以调用 m 上的任何方法。
//
// 即使 f 在常数次调用后返回 false，Range 的时间复杂度也可能是 O(N)。
func (m *Map) Range(f func(key, value any) bool) {
	read := m.loadReadOnly()
	// 如果 read map 不完整，说明 dirty map 中有新数据。
	if read.amended {
		// 幸运的是，Range 本身就是 O(N) 的，所以一次 Range 调用可以摊销
		// 整个 map 的复制成本：我们可以立即提升 dirty map！
		m.mu.Lock()
		read = m.loadReadOnly()
		if read.amended {
			// 将 dirty map 提升为 read map。
			read = readOnly{m: m.dirty}
			copyRead := read
			m.read.Store(&copyRead)
			m.dirty = nil
			m.misses = 0
		}
		m.mu.Unlock()
	}

	// 遍历提升后的或原本就完整的 read map。
	for k, e := range read.m {
		v, ok := e.load()
		if !ok {
			continue
		}
		if !f(k, v) {
			break
		}
	}
}

// missLocked 增加 miss 计数，并在达到阈值时将 dirty map 提升为 read map。
// 调用此函数时必须持有 mu 锁。
func (m *Map) missLocked() {
	m.misses++
	// 如果 miss 次数还不足以覆盖复制成本（即 dirty map 的大小），则直接返回。
	if m.misses < len(m.dirty) {
		return
	}
	// 提升 dirty map 为新的 read map。
	m.read.Store(&readOnly{m: m.dirty})
	// 清空 dirty map 和 miss 计数器。
	m.dirty = nil
	m.misses = 0
}

// dirtyLocked 确保 dirty map 被初始化。
// 它会通过浅拷贝 read map 来创建 dirty map，并排除掉被清除的条目。
// 调用此函数时必须持有 mu 锁。
func (m *Map) dirtyLocked() {
	if m.dirty != nil {
		return
	}

	read := m.loadReadOnly()
	m.dirty = make(map[any]*entry, len(read.m))
	// 从 read map 复制所有未被清除的条目到 dirty map。
	for k, e := range read.m {
		if !e.tryExpungeLocked() {
			m.dirty[k] = e
		}
	}
}

// tryExpungeLocked 尝试将一个已被删除（p==nil）的条目标记为 expunged。
// 调用此函数时必须持有 mu 锁。
func (e *entry) tryExpungeLocked() (isExpunged bool) {
	p := e.p.Load()
	for p == nil {
		// 尝试用 CAS 将 nil 替换为 expunged。
		if e.p.CompareAndSwap(nil, expunged) {
			return true
		}
		p = e.p.Load()
	}
	// 如果 p 本来就是 expunged，也返回 true。
	return p == expunged
}
