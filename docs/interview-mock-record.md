# 中间件面试 Mock 记录

> 日期：2026-10-21
> 题库来源：`java-interview-100-questions.md` + `middleware-interview-questions.md`
> 共 10 题（第 9 题跳过），整体均分约 6.5/10

---

## 逐题记录

### Q1. Redis 的 RDB 和 AOF 两种持久化方式有什么区别？生产环境怎么选？

**考生回答：**
> RDB 是 RedisDataBase，把内存二进制内容直接存到二进制文件。AOF 是 Append Of File，追加用户操作命令到文件。
> RDB 恢复快、体积小，缺点是保存慢、只能全量。AOF 写快（追加写），但体积大、恢复慢（回放命令）。
> 生产选 RDB + AOF 结合，旧数据用 RDB，就近命令用 AOF。

**判定：✅ 基本正确（8.5/10）**

**得分点：**
- RDB 二进制快照、AOF 命令日志，区别清晰
- 混合持久化选型方向正确

**遗漏/修正：**
- RDB 全称 = R**edis **D**ata**B**ase**；AOF 全称 = A**ppend **O**nly **F**ile**（缩写来源稍偏差）
- AOF 三种刷盘策略：always / everysec（默认，最多丢 1 秒）/ no
- AOF 重写（rewrite）：文件过大时压缩成最小命令集
- Redis 4.0+ 混合持久化：AOF 文件 = RDB 头部 + 增量命令尾部，恢复先加载 RDB 再重放增量

**参考文档：** middleware-interview-questions.md Q4/Q5

---

### Q2. MySQL InnoDB 的 MVCC 是什么？怎么实现的？RC 和 RR 为什么不同？

**考生回答：**
> MVCC = 多版本并发控制，借助 ReadView + undo log 实现。ReadView 包含事务 ID（递增）、最大已完成事务 ID、最大未提交事务 ID。每行通过版本链链接前一个版本。
> RC：每次读都创建新 ReadView。RR：事务启动时创建一个 ReadView，多次读复用同一个，保证可重复读。

**判定：✅ 正确（8.5/10）**

**得分点：**
- MVCC 三要素：ReadView + undo log 版本链 + 可见性判断
- RC 每次读新 ReadView → 不可重复读；RR 复用 → 可重复读
- 通过 trx_id 判断版本可见性

**遗漏/修正：**
- ReadView 核心字段更精确：`m_ids`（活跃事务 ID 集合）、`min_trx_id`、`max_trx_id`（下一个将分配的事务 ID，非"最大已完成"）、`creator_trx_id`
- 可见性四步判断规则：trx_id < min_trx_id 可见；≥ max_trx_id 不可见；在 m_ids 中不可见；不在则可见
- RR 解决幻读：快照读靠 MVCC，当前读（FOR UPDATE）靠间隙锁（Gap Lock）/临键锁（Next-Key Lock）

**参考文档：** middleware-interview-questions.md Q63，java-interview-100-questions.md Q62

---

### Q3. Kafka 为什么吞吐量高？从存储、网络、消费三个维度说明。

**考生回答：**
> Kafka 多 Broker 分布式，整体吞吐是各节点之和；消费者组多消费者并行，消费能力大。

**判定：⚠️ 部分正确（3.5/10）**

**得分点：**
- 多 Broker 分布式扩展
- 消费者组多消费者并行

**遗漏（核心机制全部漏掉）：**

| 维度 | 关键技术 | 说明 |
|------|---------|------|
| 存储 | **顺序写磁盘** | 消息追加到分区日志末尾，顺序写性能接近内存 |
| 存储 | **PageCache** | 利用 OS 页缓存，读走缓存，写走缓存异步刷盘 |
| 网络 | **零拷贝（sendfile）** | 数据从内核态直接到网卡，不经用户态拷贝 |
| 网络 | **批量发送 + 压缩** | 生产者批量发送 + snappy/lz4 压缩 |
| 消费 | **分区并行 + 拉模式** | 多分区多消费者并行，消费者主动拉取 |

**参考文档：** middleware-interview-questions.md Q31

---

### Q4. ES 的倒排索引是什么结构？由哪几部分组成？

**考生回答：**
> 倒排索引是对原始文档分词，建立分词（关键词）→ 原始文档的索引。由分词器、关键词、原始文档组成。

**判定：⚠️ 方向正确但深度不够（4/10）**

**得分点：**
- 核心思想：词 → 文档映射

**遗漏（底层三段式结构）：**

| 组成 | 作用 | 关键技术 |
|------|------|---------|
| **Term Dictionary（词典）** | 存所有分词后的 term | 按字典序排列 |
| **Term Index（词典索引）** | 加速定位 term | **FST**（有限状态机），前缀复用压缩，常驻内存 |
| **Posting List（倒排表）** | 词对应的文档 ID 列表 | **Roaring Bitmap** 压缩，支持高效交集并集 |

**参考文档：** middleware-interview-questions.md Q43

---

### Q5. MySQL 索引在什么情况下会失效？列举至少 5 种场景。

**考生回答：**
> 1. 区分度不够高（如性别列），优化器全表扫描
> 2. 索引列类型转换
> 3. 索引列函数运算
> 4. 违反最左匹配原则
> 5. 模糊匹配以通配符开头 LIKE '%xxx'
> 6. 索引列使用 OR

**判定：✅ 正确且全面（9/10）**

**得分点：**
- 6 种场景覆盖面广
- "区分度低"是亮点，很多候选人答不到

**可补充：**
- 隐式类型转换具体场景：字符串列传数字 `WHERE phone = 13800138000`
- 范围查询导致后续列失效：`WHERE a > 1 AND b = 2`，b 无法参与索引定位（可走 ICP 下推）
- `!=`、`NOT IN`、`IS NULL`（选择性差时）
- 排查工具：`EXPLAIN` 看 type/key/Extra

**参考文档：** middleware-interview-questions.md Q60/Q61

---

### Q6. 缓存穿透、缓存击穿、缓存雪崩三者有什么区别？分别怎么解决？

**考生回答：**
> 穿透：热点数据不存在，请求访问 DB。击穿：Redis 和 DB 都不存在。
> 雪崩：大量热点缓存同时过期。解决：随机过期时间或永不过期。

**判定：⚠️ 穿透和击穿说反了（4.5/10）**

**纠正：**

| 概念 | 正确定义 | 解决方案 |
|------|---------|---------|
| **穿透** | 查**根本不存在**的 key，Redis 和 DB 都没有 | 缓存空值（短 TTL）+ 布隆过滤器 |
| **击穿** | **热点 key 过期**瞬间，高并发打到 DB | 互斥锁（只让一个回源）+ 热点 key 永不过期（逻辑过期 + 后台刷新） |
| **雪崩** | **大量 key 同时过期** | 过期时间加随机扰动 + 多级缓存 + 限流降级 + 集群高可用 |

**记忆口诀：** 穿透 = 不存在（穿过去）；击穿 = 过期（击穿一个洞）；雪崩 = 大批过期（崩一片）

**参考文档：** middleware-interview-questions.md Q7

---

### Q7. MySQL 的 redo log、undo log、binlog 三种日志分别有什么作用？什么是两阶段提交？

**考生回答：**
> binlog：数据变更日志，主从同步、DRC。undo log：数据回滚 + MVCC。redo log：commit 后数据未刷盘时重做已提交事务。
> 两阶段提交：先写 redo log 再写 DB。

**判定：⚠️ 日志作用对，两阶段提交答错（5.5/10）**

**得分点：**
- 三种日志作用基本正确

**纠正——两阶段提交：**
> 两阶段提交是 redo log 和 binlog 之间的一致性保证，不是 redo log 和 DB 之间。

**正确流程：**
```
① 写 redo log（prepare 状态）
② 写 binlog
③ 提交 redo log（commit 状态）
```

**崩溃恢复：**
- redo log prepare 但未 commit → 查 binlog 是否完整 → 完整则提交，不完整则回滚

**参考文档：** middleware-interview-questions.md Q65

---

### Q8. RabbitMQ 如何保证消息不丢失？从消息流转三个环节说明。

**考生回答：**
> 1. 生产者发送时通过确认机制保证消息被 Broker 收集
> 2. Broker 收集后对消息持久化
> 3. 消费者用 ACK 机制保证正常接收

**判定：✅ 正确（7/10）**

**得分点：**
- 三环节框架正确

**可补充的具体配置：**

| 环节 | 补充细节 |
|------|---------|
| 生产者 → MQ | 具体是 **confirm 模式**，到达后回调确认；事务模式性能差少用 |
| MQ 自身 | 队列声明 **durable=true** + 消息设 **deliveryMode=2**，缺一不可 |
| MQ → 消费者 | **手动 ACK**（basicAck），成功才确认，失败 basicNack + requeue 重投 |

**兜底：** 消费方幂等（防重复消费）+ 死信队列（DLX，重试失败后隔离）

**参考文档：** middleware-interview-questions.md Q27/Q28

---

### Q9. ES 的写入流程？refresh 和 flush 区别？数据多久能被搜索？

**考生回答：** 跳过（不太会）

**要点速览：**
> - 写入链路：`translog（WAL）` → `Indexing Buffer（内存）` → `refresh` → `segment` → `flush`
> - **refresh**（默认 1 秒）：Buffer → segment（内存倒排索引），**可被搜索**但未落盘
> - **flush**（translog 满 512MB 或 30 分钟）：segment 落盘 + 清空 translog
> - 数据写入后约 **1 秒**可被搜索（NRT 近实时），持久化要等 flush
> - 速记：refresh = 可搜索，flush = 落盘持久化

**参考文档：** middleware-interview-questions.md Q44/Q45

---

### Q10. Redis 的 rehash 怎么实现？为什么渐进式而非一次性？

**考生回答：**
> Redis hash 结构有两个 hash 表，正常只用 hash[0]。装填因子超阈值触发 rehash，每次操作数据时搬一个到 hash[1]，渐进式完成。不一次性是因为 Redis 单线程，会阻塞后续请求。

**判定：✅ 正确（7.5/10）**

**得分点：**
- 双哈希表、触发条件、每次搬一个、单线程阻塞风险

**可补充的细节：**

| 维度 | 补充 |
|------|------|
| 搬运粒度 | 每次搬一个**桶**（bucket），桶内链表所有元素一次搬完 |
| rehashidx | 记录当前搬到 ht[0] 的哪个桶，每次 +1 |
| 空桶处理 | 跳过（最多连续跳 100 个），rehashidx 仍前进 |
| 读写处理 | 查找**两表都查**，新写入一律进 ht[1]，ht[0] 只减不增 |
| 定时辅助 | serverCron 定时任务也辅助搬一些桶 |
| 完成处理 | 释放 ht[0]，ht[1] 变成新 ht[0] |
| 触发条件 | 无 BGSAVE 负载因子 ≥1；有 BGSAVE ≥5（避免 cow）；缩容 <0.1 |

**参考文档：** middleware-interview-questions.md Q22b

---

## 总评

| 题号 | 主题 | 评分 | 一句话评价 |
|------|------|------|-----------|
| 1 | Redis RDB/AOF | 8.5 | 区别清晰，选型正确，缺 AOF 刷盘/重写 |
| 2 | MySQL MVCC | 8.5 | 原理到位，ReadView 字段可更精确 |
| 3 | Kafka 高吞吐 | 3.5 | ⚠️ 只答分布式，漏顺序写/零拷贝/PageCache |
| 4 | ES 倒排索引 | 4 | ⚠️ 思想对，缺 Term Dictionary/FST/Posting List |
| 5 | MySQL 索引失效 | 9 | ⭐ 场景全面，区分度低是亮点 |
| 6 | 缓存穿透/击穿/雪崩 | 4.5 | ⚠️ 穿透和击穿说反了 |
| 7 | 三日志+两阶段提交 | 5.5 | ⚠️ 日志作用好，两阶段提交答错 |
| 8 | RabbitMQ 消息不丢 | 7 | 三环节对，缺具体配置和幂等兜底 |
| 9 | ES 写入流程 | 跳过 | 建议复习 Q44/Q45 |
| 10 | Redis rehash | 7.5 | 核心正确，缺 rehashidx/读写双表/桶粒度 |

**整体均分：约 6.5/10**

---

## 重点复习清单

### 🔴 高优先级（答错或概念混淆）

1. **Kafka 高吞吐底层机制**（文档 Q31）
   - 存储：顺序写磁盘 + PageCache
   - 网络：零拷贝 sendfile + 批量压缩
   - 消费：分区并行 + 拉模式

2. **缓存穿透 vs 击穿 vs 雪崩**（文档 Q7）
   - 穿透 = 查的东西**压根不存在** → 空值 + 布隆
   - 击穿 = **一个热点** key 过期 → 互斥锁
   - 雪崩 = **一大批** key 同时过期 → 随机 TTL

3. **MySQL 两阶段提交**（文档 Q65）
   - 流程：redo log（prepare）→ binlog → redo log（commit）
   - 崩溃恢复：查 binlog 完整性决定提交或回滚

4. **ES 倒排索引结构**（文档 Q43）
   - Term Dictionary（词典）+ Term Index（FST）+ Posting List（Roaring Bitmap）

### 🟡 中优先级（答对但缺细节）

5. **ES 写入流程**（文档 Q44/Q45）
   - refresh = 可搜索（1 秒），flush = 落盘持久化

6. **AOF 刷盘策略 + 重写**（文档 Q4/Q5）
   - always / everysec（默认）/ no
   - 重写压缩 + 4.0 混合持久化

7. **MVCC ReadView 精确字段**（文档 Q63）
   - m_ids / min_trx_id / max_trx_id / creator_trx_id + 四步可见性判断

8. **RabbitMQ 具体配置**（文档 Q27）
   - confirm 模式 / durable / deliveryMode=2 / 手动 ACK / 幂等兜底

9. **Redis rehash 细节**（文档 Q22b）
   - rehashidx / 桶粒度 / 读写双表处理 / 空桶跳过

### 🟢 已掌握（保持）

- Redis RDB/AOF 区别与选型
- MySQL 索引失效场景（区分度低是亮点）
- MVCC 原理与 RC/RR 区别
- Redis rehash 核心原理

---

## 附：算法题

### 题目：链表中倒数第 k 个节点

> 输入一个链表，输出该链表中倒数第 k 个节点。从 1 开始计数，尾节点是倒数第 1 个。
> 要求：只允许遍历一次链表；k 大于链表长度时返回 null。
>
> 示例：链表 1→2→3→4→5，k=2，返回节点 4。
>
> 提示：经典解法用**双指针（快慢指针）**，快指针先走 k 步，然后两个指针一起走，快指针到尾时慢指针正好在倒数第 k 个。

### 考生解答

```java
ListNode findKthToTail(ListNode head, int k) {
    // 你的代码
    ListNode ans = head;
    int count = 0;
    for (ListNode p = head; p != null; p = p.next) {
        count += 1;
        if (count > k) {
            ans = ans.next;
        }
    }
    if (count < k) {
        return null;
    }
    return ans;
}
```

### 判定：✅ 正确（9/10）

**思路分析：**

巧妙地用单个快指针 `p` 遍历，count 超过 k 后慢指针 `ans` 同步跟随，本质等价于双指针，代码更简洁。

**走一遍验证（链表 1→2→3→4→5，k=2）：**

| 步骤 | p 指向 | count | ans 指向 |
|------|--------|-------|----------|
| 初始 | 1 | 0 | 1 |
| 第1步 | 2 | 1 | 1（count ≤ k，不动）|
| 第2步 | 3 | 2 | 1（count ≤ k，不动）|
| 第3步 | 4 | 3 | 2（count > k，前进一步）|
| 第4步 | 5 | 4 | 3（前进一步）|
| 第5步 | null | 5 | 4（前进一步）|

→ 返回 `ans = 节点4` ✅

**k=5（等于链表长度）验证：**
→ count=5，ans 始终指向节点 1，count < k 为 false，返回节点 1 ✅（倒数第 5 个就是头节点）

**k=6（大于链表长度）验证：**
→ count=5 < k=6 为 true，返回 null ✅

**复杂度：**
- 时间：O(n)，一次遍历 ✅
- 空间：O(1)，两个指针变量 ✅

### 与标准双指针写法对比

```java
// 标准双指针写法（快慢指针）
ListNode findKthToTail(ListNode head, int k) {
    ListNode fast = head, slow = head;
    // 快指针先走 k 步
    for (int i = 0; i < k; i++) {
        if (fast == null) return null;  // k > 链表长度
        fast = fast.next;
    }
    // 快慢指针同步走
    while (fast != null) {
        fast = fast.next;
        slow = slow.next;
    }
    return slow;
}
```

| 对比 | 考生写法 | 标准双指针 |
|------|---------|-----------|
| 思路 | 快指针走 k 步后慢指针才开始跟 | 显式分两阶段：先走 k 步再同步 |
| 遍历次数 | 1 次 | 1 次（两阶段在一次遍历内） |
| 边界处理 | 先遍历完再判断 count < k | 快指针走 k 步时提前判断 null |
| 可读性 | 稍需推导 | 更直观 |

### 小建议

考生写法是**先走完整个链表再判断 count < k**。标准双指针在**快指针走 k 步时就提前判断** null，如果 k 远大于链表长度，可以提前返回，不必遍历整个链表。不过这是极端场景优化，解法完全正确，面试中这个写法没问题。

**评分：9/10** — 逻辑正确，一次遍历，空间 O(1)，巧妙用 count 比较 k 控制慢指针延迟启动。唯一可优化的是 k 远大于链表长度时的提前返回。

---

## 附二：Agent & RAG 面试 Mock（3 题）

### 题 1. 什么是 ReAct？它和 CoT 有什么区别？

**考生回答：**
> ReAct 是 Agent 和大模型交互的模式，包含 Thought、Action、Observation 三个过程，支持大模型多次调用工具并感知工具执行结果，逐步思考下一步动作，多次调用工具后完成任务。
> CoT 表示思维链，是一种链式思考模式。

**判定：⚠️ ReAct 答对，CoT 对比太浅（6/10）**

**得分点：**
- ReAct = Thought → Action → Observation 循环
- 支持多次调用工具、感知执行结果、逐步决定下一步

**遗漏——CoT 对比（核心得分点）：**

| 对比 | CoT | ReAct |
|------|-----|-------|
| 核心 | 纯推理链（想→想→想→答） | 推理 + 行动交替（想→做→看→想→做→看→答） |
| 工具调用 | ❌ 不调工具，纯靠模型内部知识 | ✅ 可调工具获取实时外部信息 |
| 外部信息 | 依赖模型训练数据 | 可实时检索/API 查询 |
| 适用 | 数学推理、逻辑题 | 需要实时信息/工具的复杂任务 |
| 一句话 | **只想不做** | **边想边做** |

**记忆口诀：** CoT = 只想不做（纯脑补）；ReAct = 边想边做（动手验证）。

**参考文档：** agent-interview-questions.md Q9/Q10

---

### 题 2. 什么是混合检索（Hybrid Search）？为什么纯向量检索不够？Rerank 起什么作用？

**考生回答：**
> 混合检索：除向量检索外，还通过文本对比等方式增加检索相似度。
> 纯向量检索不够：embedding 效果不够好或分词不好时，向量相似度不等于语义相似度，所以结合关键词/文本相似度做混合检索。
> Rerank：从向量库召回多个 top 数据后，不能全部注入上下文，需进一步筛选。Rerank 模型与 RAG 的检索模型不同，用另一个模型从候选集中选最合适的，避免单模型偏差。

**判定：✅ 基本正确但缺关键术语（6.5/10）**

**得分点：**
- 混合检索 = 向量 + 文本/关键词两路
- 纯向量不够的原因：embedding 质量不稳定
- Rerank 在召回后二次筛选，不把所有结果塞上下文
- 用不同模型减少单模型偏差

**遗漏——三个关键术语（面试得分点）：**

1. **BM25**：你说的"文本对比"，面试官想听的是 **BM25（关键词检索）**。典型例子：专有名词/ID/型号（如"iPhone 15 Pro Max"中的"15"），向量检索召回不到，BM25 精准匹配。

2. **RRF（Reciprocal Rank Fusion）**：两路检索结果怎么合并？用 **RRF**——按每条结果在两路检索中的排名倒数求和，得分高的排前面。

3. **Cross-Encoder**：你说"另一个模型选择"，更准确的说法：
   - 向量检索 = **Bi-Encoder**（query 和 doc 独立编码），快但粗
   - Rerank = **Cross-Encoder**（query + doc 拼一起编码），精度高但慢
   - 流程：Bi-Encoder 粗筛 50 条 → Cross-Encoder 精排取 5 条

**完整标准回答：**
> 混合检索 = 向量检索（语义）+ BM25（关键词）两路召回，用 RRF 融合排序。纯向量不够是因为专有名词/ID/型号靠语义匹配不到，BM25 精准但不懂同义词，两者互补。Rerank 用 Cross-Encoder 对 query+doc 逐对精排，从粗筛 50 条中选最相关 5 条注入上下文，兼顾速度和精度。

**参考文档：** rag-interview-questions.md Q19/Q20

---

### 题 3. Agent 工具调用失败或 LLM 选错工具时有哪些处理策略？如何防止死循环？

**考生回答：**
> 选错工具：排查暴露了哪些工具、从工具描述和适用场景入手、增加禁用场景描述。
> 工具执行失败：系统问题（网络等）静默重试 LLM 不感知；业务/参数错误把错误信息给模型让其纠正。
> 死循环成因：① 终止条件不明确 ② 任务状态没推进 ③ 子 Agent 间循环依赖（A→B→A）。

**判定：✅ 回答得很好，工程思维清晰（8.5/10）**

**得分点：**
- 区分"选错工具"和"工具执行失败"两类问题
- 工具执行失败区分系统问题（静默重试）vs 业务错误（反馈给 LLM 纠正）
- try-catch 捕获异常
- 死循环三个成因分析到位（终止条件/状态推进/循环依赖）

**可补充的加分点：**

**工具选错补充：**

| 补充项 | 说明 |
|--------|------|
| few-shot 示例 | 工具描述加"什么时候用/不用"的示例，准确率显著提升 |
| 工具数量限制 | 工具太多（>10）LLM 易选错，用工具路由/嵌套 Agent 分组 |
| Structured Output | 强制 LLM 输出合法 JSON，避免格式错误 |

**防止死循环补充：**

| 补充项 | 说明 |
|--------|------|
| **max_iterations 硬限制** | 生产最核心兜底，设置最大步数（简单 5-10/中等 10-20/复杂 20-50），超限强制终止 |
| **超时控制** | 单步超时 + 全局超时，防止卡在某个工具调用上 |
| **循环检测** | 记录历史 Action，重复调用同一工具同一参数 N 次 → 判定死循环终止 |
| **降级策略** | 多次失败后降级（返回默认值/转人工），不让 Agent 无限重试 |

**参考文档：** agent-interview-questions.md Q20/Q22/Q36/Q37

---

## Agent & RAG Mock 总评

| 题号 | 主题 | 评分 | 评价 |
|------|------|------|------|
| 1 | Agent ReAct vs CoT | 6 | ReAct 机制对，CoT 对比太浅，漏了工具调用差异 |
| 2 | RAG 混合检索+Rerank | 6.5 | 方向都对，缺 BM25/RRF/Cross-Encoder 关键术语 |
| 3 | Agent 工具失败+防死循环 | 8.5 | ⭐ 工程思维好，分类清晰，缺 max_steps 硬兜底 |

**整体均分：7/10**

**强项：** 工程实践思维好，能区分问题类型分别处理，死循环分析有深度。

**需重点复习：**
1. **ReAct vs CoT 的工具调用差异**（文档 Q9/Q10）——"CoT 只想不做，ReAct 边想边做"
2. **混合检索三术语**：BM25（关键词）、RRF（融合）、Cross-Encoder（Rerank 精排）——术语是面试得分点
3. **防死循环硬兜底**：max_iterations 限制 + 超时控制 + 循环检测

---

## 附三：Java 面试 Mock 第二轮（5 题）

### 题 1. String、StringBuilder、StringBuffer 区别？String 为什么不可变？

**考生回答：**
> String 不可变，底层 char[] 实现，不可变原因是 char[] 不支持改变大小。StringBuilder 和 StringBuffer 实现动态拼接，前者线程不安全，后者安全。

**判定：⚠️ 区别对，不可变原因答错（5/10）**

**得分点：**
- String 不可变，StringBuilder/StringBuffer 可变
- StringBuilder 线程不安全，StringBuffer 线程安全（synchronized）

**纠正——不可变原因：**
char[] 本身可以改内容，String 不可变是设计选择：
1. **final 修饰类**：不能被继承，子类不能破坏不可变性
2. **private final value**：value 数组私有 final，外部无法访问
3. **无 setter**：不提供修改方法，replace/substring 都创建新对象

**不可变四大好处：** 线程安全、hashCode 可缓存（HashMap key）、安全性（防篡改）、字符串常量池共享

**补充：** Java 9+ 底层从 char[] 改为 byte[]（JEP 254 Compact Strings，省内存）

**参考文档：** java-interview-100-questions.md Q6/Q7

---

### 题 2. synchronized 和 volatile 区别？synchronized 锁升级过程？

**考生回答：**
> synchronized 是锁机制，volatile 不是锁。前者互斥防止同时修改，后者无法保证两写事务同时修改数据。synchronized 保证原子性，volatile 不保证。锁升级：自旋等待→超时→重量锁。

**判定：⚠️ 区别方向对，锁升级漏两步（4/10）**

**得分点：**
- synchronized 是互斥锁保证原子性，volatile 非锁不保证原子性

**纠正——锁升级四步（你只答了自旋→重量，漏了两步）：**

```
无锁 → 偏向锁 → 轻量级锁 → 重量级锁
```

| 阶段 | 触发 | 原理 |
|------|------|------|
| 偏向锁 | 第一个线程访问 | 对象头记录线程 ID，同线程再进无需 CAS |
| 轻量级锁 | 有竞争但不激烈 | CAS 自旋等待（你说的"自旋"在这一步） |
| 重量级锁 | 竞争激烈/自旋超时 | OS 互斥量，线程阻塞挂起 |

- 锁只能升级不能降级
- Java 15 默认禁用偏向锁（JEP 374）
- 你说的"自旋"是轻量级锁阶段行为，不是独立阶段

**区别补充：** volatile 还保证可见性（写刷主存/读从主存）和有序性（禁止重排），不阻塞；synchronized 保证三大特性但阻塞

**参考文档：** java-interview-100-questions.md Q45/Q46/Q47

---

### 题 3. JVM 如何判断对象可回收？GC Roots 包含哪些？

**考生回答：**
> 从根对象出发，不可达则可回收。根对象包括全局变量、运行中定义的变量。可达性分析是判断从根对象能否直接或间接关联到堆中对象。

**判定：⚠️ 思想对，GC Roots 太笼统（5.5/10）**

**得分点：**
- 可达性分析核心思想正确
- 提到了静态变量和局部变量

**补充——GC Roots 四类（面试要答全）：**

| GC Roots 类型 | 说明 |
|--------------|------|
| **虚拟机栈局部变量** | 方法中声明的局部变量、参数 |
| **方法区静态变量** | static 修饰的引用 |
| **方法区常量** | static final 常量引用 |
| **本地方法栈 JNI 引用** | Native 方法引用的对象 |

记忆口诀：**栈、静态、常量、Native**

**补充：**
- Java 不用引用计数法（循环引用问题）
- 四种引用：强（不回收）、软（内存不足回收）、弱（下次 GC 回收）、虚（仅跟踪）

**参考文档：** java-interview-100-questions.md Q65/Q66

---

### 题 4. Spring Boot 自动装配原理？@SpringBootApplication 包含哪些注解？

**考生回答：**
> 配置文件中配了数据源等配置，Spring 加载到运行时变量中。

**判定：⚠️ 答偏了，这不是自动装配（2.5/10）**

**纠正：** 你描述的是配置属性绑定（@ConfigurationProperties），不是自动装配。

**自动装配原理（标准回答）：**

```
1. @SpringBootApplication 内含 @EnableAutoConfiguration
2. @EnableAutoConfiguration 通过 @Import 导入 AutoConfigurationImportSelector
3. AutoConfigurationImportSelector 读取 META-INF/spring.factories
   （Spring Boot 2.7+ 改为 AutoConfiguration.imports）
4. 加载所有 AutoConfiguration 类（如 RedisAutoConfiguration）
5. 每个类用条件注解判断是否生效：
   - @ConditionalOnClass：classpath 有这个类才装配
   - @ConditionalOnMissingBean：容器没有才创建
   - @ConditionalOnProperty：配置了属性才装配
6. 满足条件的自动创建 Bean → "约定大于配置"
```

**@SpringBootApplication 三个核心注解：**

| 注解 | 作用 |
|------|------|
| @SpringBootConfiguration | = @Configuration，标记配置类 |
| @EnableAutoConfiguration | 开启自动装配（核心） |
| @ComponentScan | 包扫描 |

**参考文档：** java-interview-100-questions.md Q86

---

### 题 5. ConcurrentHashMap Java 8 实现原理？为什么放弃分段锁？

**考生回答：**
> Java 7 用分段锁，把 hashlist 分成若干索引段，写时对所在索引段加锁。Java 8 不再锁索引段，而是锁单个桶，锁粒度降低。放弃分段锁因为分段锁影响不同桶的写操作。

**判定：✅ 正确，核心都答到了（8/10）**

**得分点：**
- Java 7 分段锁（Segment）→ Java 8 桶级锁
- 锁粒度降低，并发度提升
- 放弃分段锁原因正确

**补充的技术细节：**

| 细节 | 说明 |
|------|------|
| Java 8 空桶 | 用 **CAS** 无锁插入 |
| Java 8 非空桶 | 用 **synchronized** 锁桶头节点 |
| 读操作 | val/next 用 **volatile** 保证可见性，不加锁 |
| 放弃分段锁第二个原因 | Segment 多一层间接寻址，内存和效率有开销 |
| Java 8 结构 | Node[] + 链表 + 红黑树（和 HashMap 一致） |

**参考文档：** java-interview-100-questions.md Q32

---

## Java Mock 第二轮总评

| 题号 | 主题 | 评分 | 评价 |
|------|------|------|------|
| 1 | String 不可变 | 5 | 区别对，不可变原因答错（char[]限制→实际是final class+private final+无setter） |
| 2 | synchronized+锁升级 | 4 | 区别方向对，锁升级漏偏向锁和无锁两个阶段 |
| 3 | JVM GC判活+GC Roots | 5.5 | 可达性分析思想对，GC Roots 太笼统，漏常量和JNI |
| 4 | Spring Boot 自动装配 | 2.5 | ⚠️ 答的是配置属性绑定，不是自动装配原理 |
| 5 | ConcurrentHashMap | 8 | ⭐ 核心区别和原因都对，补 CAS/synchronized 细节更好 |

**整体均分：5/10**

**强项：** ConcurrentHashMap（8 分）——Java 7/8 差异理解清晰

**需重点复习：**
1. **Spring Boot 自动装配**（文档 Q86）——核心：spring.factories + AutoConfigurationImportSelector + 条件注解，答偏了需重点补
2. **synchronized 锁升级四步**（文档 Q45）——无锁→偏向锁→轻量级锁→重量级锁
3. **String 不可变原因**（文档 Q7）——final class + private final value + 无 setter，不是 char[] 限制
4. **GC Roots 四类**（文档 Q65）——栈、静态、常量、Native
