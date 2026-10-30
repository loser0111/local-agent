# 中间件高频面试题（Redis / MQ / ES / MySQL）

> 参考多家权威来源并结合实战经验整理，按中间件分类。每题附要点答案，便于快速复习。

---

## 目录

1. [Redis（Q1–Q22b）](#一redisq1q22)
2. [消息队列 MQ（Q23–Q40）](#二消息队列-mqq23q40)
3. [Elasticsearch（Q41–Q55）](#三elasticsearchq41q55)
4. [MySQL（Q56–Q75）](#四mysqlq56q75)

---

## 一、Redis（Q1–Q22b）

### Q1. Redis 是什么？核心特点？
Redis（Remote Dictionary Server）是基于内存的 key-value NoSQL 数据库，C 语言开发。核心特点：
- **全内存存储**，读写延迟低（百纳秒级），单实例 QPS 可达 10 万+。
- **支持多种数据结构**：String/Hash/List/Set/ZSet 等。
- **支持持久化**：RDB + AOF。
- **单线程命令处理**（6.0 后网络 IO 多线程，命令执行仍单线程），避免锁竞争与上下文切换。
- **支持高可用与集群**：主从复制、哨兵 Sentinel、Cluster 分片。

### Q2. Redis 为什么快？
1. **全内存操作**：数据在内存中，无磁盘寻道开销。
2. **单线程模型**（命令执行层）：避免多线程锁竞争与上下文切换，IO 多路复用（epoll）单线程处理大量连接。
3. **高效数据结构**：SDS、跳表、压缩列表、哈希表，针对不同场景优化。
4. **Redis 6.0 多线程网络 IO**：接收/发送用多线程，命令执行仍单线程，突破网络瓶颈。

### Q3. Redis 的数据类型及适用场景？
| 类型 | 底层结构 | 典型场景 |
|------|---------|---------|
| String | SDS | 缓存对象、计数器、分布式锁 |
| Hash | ziplist / hashtable | 对象属性存储、购物车 |
| List | quicklist（ziplist+链表） | 消息队列、最新列表 |
| Set | intset / hashtable | 去重、标签、共同好友 |
| ZSet | ziplist / skiplist+hashtable | 排行榜、延迟队列（score 为时间） |
| 高阶 | — | Bitmap（签到）、HyperLogLog（UV 统计）、Stream（消息流）、Geo（附近的人） |

### Q4. Redis 持久化机制 RDB 和 AOF 的区别？
| 对比 | RDB | AOF |
|------|-----|-----|
| 原理 | 全量快照，fork 子进程生成 .rdb 文件 | 追加每条写命令到 .aof 文件 |
| 触发 | save/bgsave/自动触发 | always/everysec/no 三种策略 |
| 数据安全 | 丢最后一次快照后的数据 | everysec 最多丢 1 秒 |
| 恢复速度 | 快（直接加载二进制） | 慢（重放命令） |
| 文件大小 | 小（压缩二进制） | 大（命令日志，需 rewrite 压缩） |
- **生产推荐**：RDB + AOF 混合（Redis 4.0+），兼顾恢复速度与数据安全。

### Q5. AOF 重写是什么？
AOF 文件会越来越大，重写（rewrite）就是把当前内存数据转成最小命令集写入新文件，替换旧文件。
- 触发：配置 `auto-aof-rewrite-percentage 100`（文件翻倍）和 `auto-aof-rewrite-min-size 64mb`。
- 重写期间用 AOF 重写缓冲区暂存新写命令，重写完成后追加，保证一致性。
- Redis 4.0+ 混合持久化：AOF 文件前半段是 RDB 二进制，后半段是增量命令，恢复时先加载 RDB 再重放增量。

### Q6. Redis 的内存淘汰策略？
8 种策略（maxmemory-policy）：
1. **noeviction**：不淘汰，写入报错（默认）。
2. **allkeys-lru**：所有 key 中淘汰最近最少使用（最常用）。
3. **allkeys-lfu**：所有 key 中淘汰最不常用（Redis 4.0+）。
4. **allkeys-random**：随机淘汰。
5. **volatile-lru**：设了过期时间的 key 中 LRU。
6. **volatile-lfu**：设了过期时间的 key 中 LFU。
7. **volatile-random**：设了过期时间的 key 中随机。
8. **volatile-ttl**：设了过期时间的 key 中优先淘汰 TTL 最短的。
- 经验：缓存场景用 `allkeys-lru`；存了永久数据的混合场景用 `volatile-lru`。

### Q7. 缓存穿透、击穿、雪崩的区别与解决方案？
| 问题 | 描述 | 解决方案 |
|------|------|---------|
| **穿透** | 查不存在的 key，请求打到 DB | 缓存空值（短 TTL）、布隆过滤器拦截非法 key |
| **击穿** | 热点 key 过期瞬间高并发穿透 | 互斥锁（只让一个请求回源）、热点 key 永不过期（逻辑过期 + 后台刷新） |
| **雪崩** | 大量 key 同时过期 | 过期时间加随机扰动、多级缓存、限流降级、集群高可用 |

### Q8. Redis 的过期删除策略？
- **定期删除**：每隔一段时间随机抽取一些设了过期的 key 检查，删除已过期的（惰性删除兜底）。
- **惰性删除**：访问 key 时才检查是否过期，过期则删除。
- 两者配合：定期删除主动清理，惰性删除兜底，仍未删的由淘汰策略处理。

### Q9. Redis 内存淘汰 LRU 和 LFU 的区别？
- **LRU（Least Recently Used）**：淘汰最久没被访问的，基于时间维度。Redis 用近似 LRU（随机采样 5 个，淘汰最旧的），非严格 LRU，节省内存。
- **LFU（Least Frequently Used）**：淘汰访问频率最低的，基于频率维度。适合"某些 key 偶尔被访问一次但占用内存"的场景，Redis 4.0+ 引入，用 Morris 计数器近似统计频率。

### Q10. Redis 单线程为什么还需要加锁？
命令执行虽单线程，但有些非原子操作仍需保证原子性：
- `incr` 本身原子，但"读取-判断-写入"组合（如 `get → 判断 → set`）不是原子的，并发下会出错。
- 解决：`MULTI/EXEC` 事务、Lua 脚本（原子执行）、`SETNX` 系列。
- Lua 脚本在 Redis 中以原子方式执行，期间不会被打断，是分布式锁的核心。

### Q11. Redis 事务（MULTI/EXEC）是什么？支持 ACID 吗？
- `MULTI` 开启事务，后续命令入队列，`EXEC` 一次性执行。
- `DISCARD` 取消，`WATCH` 乐观锁（key 变化则事务不执行）。
- **不支持回滚**：某条命令执行出错（非语法错），后续仍继续执行，不回滚。
- 与 MySQL 事务不同，Redis 事务只保证命令"顺序执行"和"不被打断"，不是真正的 ACID 事务。

### Q12. Redis 主从复制的原理？
1. **全量同步**：slave 首次连接 master，master 执行 bgsave 生成 RDB 发给 slave，slave 加载恢复。期间新写命令存到复制缓冲区，同步后追加。
2. **增量同步**：全量后，master 把新命令实时同步给 slave（基于 offset + replid）。
3. **断线重连**：slave 断线后，若 offset 在 master 复制积压缓冲区内则增量同步，否则全量。
- Redis 2.8+ 引入 psync，支持部分重同步，减少全量开销。

### Q13. Redis 哨兵（Sentinel）的作用？
- **监控**：监控 master/slave 是否存活。
- **通知**：通知运维或其他客户端实例状态。
- **自动故障转移**：master 宕机，哨兵选举新 master，通知 slave 和客户端。
- 选举：多个哨兵共同投票（quorum），防止脑裂。
- 适用：一主多从架构的高可用，不解决容量扩展问题（需 Cluster）。

### Q13b. 哨兵如何检测主节点是否存活？（检测机制详解）

**核心机制：PING 心跳（Redis 协议层，非 TCP ACK）**

哨兵每隔 `sentinel ping-period`（默认 1 秒）向所有主从节点发送 Redis `PING` 命令：

| master 回复 | 含义 |
|-------------|------|
| `+PONG` | 存活（正常） |
| `+LOADING` | 存活（正在加载数据，临时不可用但节点在） |
| `+MASTERDOWN` | 存活（slave 还在同步中，拒绝服务但节点在） |
| 超时无回复 | 认为下线 |

**为什么用 Redis PING 而非 TCP ACK？** TCP ACK 只确认传输层收到，Redis 进程可能假死（进程在但不能处理请求）。Redis PING 是应用层确认，只有 Redis 进程正常运行才能回复 +PONG。

**两阶段下线判断：**

**第一阶段：主观下线（SDOWN，Subjective Down）**
```
哨兵 A 每 1 秒向 master 发 PING
  → master 超过 down-after-milliseconds（默认 30 秒）无回复
  → 哨兵 A 标记 master 为【主观下线 SDOWN】
  → 只是哨兵 A 自己的判断，不代表 master 真的挂了（可能网络抖动）
```

**第二阶段：客观下线（ODOWN，Objective Down）**
```
哨兵 A 判定 SDOWN 后，通过 Pub/Sub 频道 __sentinel__:hello 广播
  → 问其他哨兵"你们觉得 master 挂了吗？"
  → 其他哨兵也判定 SDOWN → 回复确认
  → 统计：SDOWN 的哨兵数 ≥ quorum（法定人数）
  → 标记为【客观下线 ODOWN】→ 确认 master 挂了，触发故障转移
```

**SDOWN vs ODOWN 区别：**

| | SDOWN（主观） | ODOWN（客观） |
|--|--------------|--------------|
| 谁判断 | 单个哨兵 | 多数哨兵（≥ quorum） |
| 是否确认死亡 | ❌ 可能误判（网络抖动） | ✅ 多数派确认，不会误判 |
| 是否触发故障转移 | ❌ 不触发 | ✅ 触发选举新主 |

**关键参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `sentinel down-after-milliseconds` | 30000ms（30 秒） | 超时无回复 → 主观下线 |
| `sentinel ping-period` | 1000ms（1 秒） | PING 发送间隔 |
| `quorum` | 哨兵数/2+1 | 客观下线所需最少同意数（3 哨兵设 2，5 哨兵设 3） |

**哨兵之间怎么通信？**
- 通过 Pub/Sub 频道 `__sentinel__:hello` 发布/订阅消息，发现其他哨兵、交换状态。
- 哨兵之间也互相发 PING 检测存活。

**完整流程：**
```
哨兵每秒发 PING → 30 秒无回复 → SDOWN（主观，单个哨兵）
  → Pub/Sub 广播问其他哨兵
  → ≥ quorum 同意 → ODOWN（客观，多数派确认）
  → 触发故障转移
  → 选举 Leader 哨兵（负责执行故障转移）
  → Leader 从 slave 中选新 master
  → 通知所有 slave 指向新 master
  → 通过 Pub/Sub 通知客户端新 master 地址
```

**速记：** 每秒 PING → 30 秒无回复 → SDOWN（主观）→ Pub/Sub 问其他哨兵 → ≥quorum 同意 → ODOWN（客观）→ 故障转移。是 Redis PING 不是 TCP ACK。

### Q13c. 主节点切换后客户端怎么感知新主？

**核心机制：哨兵通过 Pub/Sub 主动通知客户端。**

客户端启动时不直接连 master，而是连接哨兵并订阅频道：

```
客户端启动：
  ① 连接哨兵（不是直接连 master）
  ② 订阅哨兵 Pub/Sub 频道：
     - +switch-master（主节点切换）
     - +sdown（主观下线）
     - +odown（客观下线）
     - +slave-reconf-sent（从节点重新配置）
  ③ 哨兵告诉客户端当前 master 是谁 → 客户端连 master

主节点切换时：
  ④ 哨兵发布 +switch-master 消息（旧 IP:port → 新 IP:port）
  ⑤ 客户端收到 → 断开旧 master → 连接新 master
  ⑥ 业务代码无感知
```

**Redisson 实际行为（Java 客户端）：**
```java
config.useSentinelServers()
    .setMasterName("mymaster")
    .addSentinelAddress("redis://sentinel1:26379")
    .addSentinelAddress("redis://sentinel2:26379");
// Redisson 内部自动：连接哨兵 → 订阅 +switch-master → 主切换时自动切连
```

**如果 Pub/Sub 消息丢失怎么办？（兜底机制）：**

| 场景 | 兜底 |
|------|------|
| Pub/Sub 消息丢失 | 客户端定期（如每 10s）主动查询：`SENTINEL get-master-addr-by-name mymaster` |
| 旧 master 写入失败 | 客户端捕获异常 → 重新查询哨兵获取新 master 地址 → 重试 |

**注意：客户端连的是哨兵不是从节点。** 从节点不负责告知客户端新主是谁，告知新主是哨兵的职责。

**两层机制总结：**
1. **主动通知**（主）：哨兵 Pub/Sub 频道 `+switch-master` 主动推送新主地址。
2. **兜底查询**（备）：客户端定期 `SENTINEL get-master-addr-by-name` 主动查询。

**速记：** 哨兵 Pub/Sub `+switch-master` 主动通知客户端新主 → 客户端订阅哨兵频道（不是连从节点）→ 消息丢失兜底：`SENTINEL get-master-addr-by-name` 主动查询。

### Q14. Redis Cluster 的原理？
- **数据分片**：16384 个哈希槽（slot），按 CRC16(key) % 16384 分配到各节点。
- **集群节点**：每个节点负责一部分槽，通过 gossip 协议通信。
- **客户端路由**：客户端直连，若 key 不在该节点返回 MOVED 重定向；迁移中返回 ASK。
- **高可用**：每个节点可配从节点，主节点宕机，从节点接管（集群级故障检测和故障转移）。
- 集群最少 3 主 3 从，推荐 6 节点起步。

### Q15. Redis 分布式锁怎么实现？
- 基础：`SET key value NX PX timeout`（原子加锁 + 过期）。
- value 用唯一标识（UUID/线程ID），解锁用 Lua 脚本判断值再删，避免误删。
- 看门狗续期：Redisson 框架自动定时续 TTL，避免业务未完成锁过期。
- 可重入：用 Hash 结构记录线程 ID + 重入次数。
- 问题：主从异步复制可能丢锁 → 红锁/Redisson RedLock；强一致需求用 ZK/etcd。

### Q16. Redis 的 bigkey 和 hotkey 怎么排查和处理？
- **bigkey**：value 过大（如 String > 10KB、List/Hash 元素 > 1 万）。
  - 排查：`redis-cli --bigkeys`、`MEMORY USAGE key`。
  - 处理：拆分（Hash 分片）、压缩、设 TTL、避免大集合操作（HGETALL 改 HSCAN）。
- **hotkey**：访问频率极高的 key，导致单节点热点。
  - 排查：`redis-cli --hotkeys`（LFU 模式）、代理层统计。
  - 处理：本地缓存（多级缓存）、key 分片（拆成多份随机访问）、读写分离。

### Q17. Redis 的 Pipeline 是什么？
- 批量发送命令一次性到 Redis，减少网络往返（RTT）开销。
- 非原子执行，仅减少网络延迟，不等同于事务。
- 适用：批量写入、大批量查询。
- 注意：Pipeline 数据量大时可能阻塞，需分批。

### Q18. Redis 6.0 多线程是什么？
- Redis 6.0 之前：单线程处理网络 IO 和命令执行，epoll 多路复用。
- 6.0 之后：网络 IO（接收/回写响应）用多线程，**命令执行仍单线程**。
- 原因：单线程网络 IO 成为瓶颈，多线程网络可突破 10 万 QPS 上限。
- 配置：`io-threads 4`（建议 CPU 核数的一半）。

### Q19. Redis 和 Memcached 的区别？
| 对比 | Redis | Memcached |
|------|-------|-----------|
| 数据类型 | 丰富（String/Hash/List/Set/ZSet） | 只有 String |
| 持久化 | RDB+AOF | 无 |
| 高可用 | 主从/哨兵/Cluster | 需客户端一致性哈希 |
| 线程模型 | 单线程命令（6.0 网络 IO 多线程） | 多线程 |
| 适用 | 缓存 + 数据结构 + 分布式协调 | 纯缓存，追求极简 |

### Q20. Redis 的 Lua 脚本有什么用？
- 原子执行多条命令，避免并发竞态。
- 减少网络往返（多条命令一次发送）。
- 典型场景：分布式锁解锁（判断 value → 删除）、限流（滑动窗口计数）、库存扣减。
- 注意：脚本执行期间阻塞其他命令，不能太耗时；脚本过长影响性能。

### Q21. Redis 集群为什么是 16384 个哈希槽？
- 心跳包小：节点间 gossip 通信携带槽位 bitmap，16384 位 = 2KB，可控。
- 集群规模：官方建议最大 1000 节点，16384 槽足够分配。
- 历史原因：CRC16 有 16 bit，但 Redis 取了 16384 = 2^14，平衡 bitmap 大小与集群规模。

### Q22. Redis 常见性能问题和调优？
1. **bigkey**：拆分、SCAN 替代全量操作。
2. **慢命令**：KEYS *、FLUSHALL 等阻塞，禁用或用 SCAN。
3. **内存碎片**：`INFO memory` 看 fragmentation ratio，`activedefrag` 自动整理。
4. **持久化阻塞**：fork 耗时（大实例），AOF rewrite 频率过高。
5. **网络瓶颈**：启用 Pipeline、6.0 多线程 IO。
6. **合理设 TTL**：避免内存只增不减。
7. **监控**：`SLOWLOG GET` 慢日志、`INFO` 各指标、Prometheus + redis_exporter。

### Q22b. Redis 是如何实现 rehash 的？

**字典结构：** Redis 字典底层有两个哈希表 `ht[0]` 和 `ht[1]`，正常只用 `ht[0]`，`rehashidx` 记录迁移进度（-1 表示未进行）。

**触发条件：**
| 条件 | 触发 |
|------|------|
| 无后台 BGSAVE/BGREWRITEAOF | 负载因子 ≥ 1 |
| 正在进行 BGSAVE/BGREWRITEAOF | 负载因子 ≥ 5（避免 cow 期间大量复制内存页） |
| 缩容 | 负载因子 < 0.1（used/size 低于 10%） |
- 扩容目标：`ht[1]` 容量 = 第一个 ≥ `ht[0].used * 2` 的 2^n。

**渐进式 rehash 流程（核心：不一次性搬完，分批每次搬一点）：**
1. 触发 rehash：分配 `ht[1]`，`rehashidx = 0`。
2. 每次增删改查操作后，顺便把 `ht[0]` 中 `rehashidx` 对应的桶迁移到 `ht[1]`，`rehashidx++`。
3. 空桶跳过（最多连续跳 100 个空桶，防极端卡顿）。
4. 定时任务（serverCron）也会辅助迁移一些桶，加快进度。
5. 迁移完成后：释放 `ht[0]`，`ht[1]` 变成新的 `ht[0]`，分配新空 `ht[1]`，`rehashidx = -1`。

**rehash 期间的读写处理：**
| 操作 | 处理方式 |
|------|---------|
| 查找（读） | 先查 `ht[0]`，找不到再查 `ht[1]` |
| 插入（写） | 新元素一律写入 `ht[1]`，`ht[0]` 只减不增 |
| 删除/修改 | 两张表都找，找到的表上操作 |
| 元素总数 | `ht[0].used + ht[1].used` |

**为什么渐进式而非一次性？**
- Redis 单线程，百万级 key 一次性搬迁会阻塞几秒，导致延迟尖峰、客户端超时、雪崩风险。
- 渐进式把开销均摊到每次操作，每次只搬一个桶，用户无感知。
- 代价：rehash 期间同时持有两张表，内存占用稍高，但不会暴涨。

**速记：** 两张表 ht[0]/ht[1]，rehashidx 逐桶搬，读写双表查、新写进 ht[1]，搬完释放 ht[0] 替为 ht[1]，渐进式避免单线程阻塞。

---

## 二、消息队列 MQ（Q23–Q40）

### Q23. 为什么要用消息队列？核心作用？
三大核心作用：
1. **解耦**：生产者消费者通过 MQ 解耦，互不影响。
2. **异步**：非核心链路异步处理，加快主流程响应。
3. **削峰**：高并发请求先入队列，消费者按能力消费，保护下游。
- 代价：引入复杂度、可用性风险、一致性问题（最终一致）。

### Q24. 常见 MQ 对比？
| 对比 | RabbitMQ | RocketMQ | Kafka | ActiveMQ |
|------|----------|----------|-------|----------|
| 语言 | Erlang | Java | Scala/Java | Java |
| 吞吐量 | 万级 | 十万级 | 百万级 | 万级 |
| 延迟 | 微秒级 | 毫秒级 | 毫秒级 | 毫秒级 |
| 可靠性 | 高 | 高 | 高 | 一般 |
| 顺序消息 | 队列级 | 分区级 | 分区级 | 不保证 |
| 事务消息 | 不支持 | 支持 | 支持（0.11+） | 不支持 |
| 适用 | 低延迟、高可靠 | 金融、电商 | 大数据、日志 | 已淘汰 |
| 协议 | AMQP | 自研 | 自研 | JMS |

### Q25. RabbitMQ 的核心概念？
- **Producer/Consumer**：生产者/消费者。
- **Exchange（交换机）**：接收消息，按路由规则分发到队列。
- **Queue（队列）**：存储消息。
- **Binding（绑定）**：Exchange 与 Queue 的绑定关系。
- **Routing Key**：消息路由键，Exchange 按此分发。
- 四种 Exchange 类型：direct（精准匹配）、fanout（广播）、topic（模式匹配）、headers（头匹配）。

### Q26. RabbitMQ 四种 Exchange 类型？
| 类型 | 路由方式 | 场景 |
|------|---------|------|
| **direct** | Routing Key 精准匹配 | 点对点，按 key 路由 |
| **fanout** | 不看 key，广播到所有绑定队列 | 广播通知、日志 |
| **topic** | Routing Key 模式匹配（`*` 单词，`#` 多词） | 按主题订阅 |
| **headers** | 按消息头属性匹配 | 少用 |

### Q27. RabbitMQ 如何保证消息不丢失？
三个环节：
1. **生产者到 MQ**：开启 confirm 模式，消息到达 MQ 后回调确认；事务模式（性能差，少用）。
2. **MQ 自身存储**：队列和消息设为 **durable**（持久化），消息设 `deliveryMode=2`（持久化到磁盘）。
3. **MQ 到消费者**：手动 ACK（`basicAck`），消费者处理成功后才确认；失败则 nack 重投。
- 兜底：消费方幂等，防止重复消费。

### Q28. RabbitMQ 死信队列（DLX）是什么？
消息变成死信（Dead Letter）的条件：
1. 消息被消费者 `basicNack/basicReject` 且 `requeue=false`。
2. 消息 TTL 过期。
3. 队列达到最大长度。
- 死信被转发到绑定的 DLX（Dead Letter Exchange），再路由到死信队列。
- 场景：延迟队列（设 TTL + DLX）、异常消息隔离、重试机制。

### Q29. RabbitMQ 延迟队列怎么实现？
两种方式：
1. **TTL + DLX**：消息设 TTL，过期变死信转 DLX → 延迟队列消费。（注意：队头消息 TTL 未到会阻塞后续消息 TTL 检查）
2. **rabbitmq-delayed-message-exchange 插件**：原生支持延迟投递，消息存 Mnesia 表，到期投递到队列。
- 经验：生产推荐插件方式，TTL+DLX 有"队头阻塞"坑。

### Q30. Kafka 的核心概念？
- **Producer/Consumer**：生产者/消费者。
- **Broker**：Kafka 节点。
- **Topic**：消息主题，逻辑分类。
- **Partition**：分区，物理存储单元，一个 Topic 可多分区，分布在不同 Broker。
- **Replica**：副本，Leader 读写、Follower 同步。
- **Consumer Group**：消费者组，组内每分区只被一个消费者消费，实现负载均衡。
- **Offset**：消费者在分区中的消费位置。
- **ZooKeeper/KRaft**：集群协调（Kafka 3.x 后可用 KRaft 替代 ZK）。

### Q31. Kafka 为什么高吞吐？
1. **顺序写磁盘**：消息追加到分区日志文件，磁盘顺序写性能接近内存。
2. **零拷贝（Zero Copy）**：用 sendfile，数据从内核态直接到网卡，避免用户态拷贝。
3. **PageCache**：利用 OS 页缓存，读走缓存，写走缓存异步刷盘。
4. **批量与压缩**：生产者批量发送 + 压缩（snappy/lz4），减少网络和存储开销。
5. **分区并行**：多分区多消费者并行消费。

### Q32. Kafka 如何保证消息顺序性？
- **问题**：多分区无序，消费端多线程也打乱顺序。
- **方案**：同一业务 key（如订单 ID）的消息发到同一 Partition（按 key 哈希），消费端单线程消费该分区。
- 瓶颈：单分区吞吐有限，需要并行时可在消费端按 key 分组到不同线程。
- RocketMQ 有**分区有序**（MessageQueueSelector）和**全局有序**（单分区，牺牲并发）两种模式。

### Q33. Kafka 如何保证消息不丢？
1. **生产者**：`acks=all`（所有副本确认才算成功）+ `retries`（重试）。
2. **Broker**：副本数 `replication.factor ≥ 3`，`min.insync.replicas ≥ 2`（至少 2 个同步副本才算可用）。
3. **消费者**：关闭自动提交 offset，处理完手动提交；失败不提交，重消费。
- 注意：acks=all 会增加延迟，按业务权衡。

### Q34. Kafka 的消费模式：拉模式 vs 推模式？
- Kafka 用**拉模式（Pull）**：消费者主动从 Broker 拉取消息，按自身节奏消费。
  - 优点：消费者按能力消费，不压垮。
  - 缺点：无消息时需轮询（长轮询缓解）。
- 推模式（Push）：Broker 主动推送。RabbitMQ 主要是推模式。
- Kafka 2.x 后有 Kafka Connect 的"推-拉"组合。

### Q35. Kafka 的 ISR 是什么？
- **ISR（In-Sync Replicas）**：与 Leader 保持同步的副本集合。
- Leader 始终在 ISR 中，Follower 落后（超 `replica.lag.time.max.ms`）则被移出 ISR。
- Leader 宕机时，优先从 ISR 中选举新 Leader，保证数据不丢。
- `min.insync.replicas`：写入至少要有 N 个 ISR 副本确认，否则拒绝写入，保证可靠性。

### Q36. Kafka 消费者 Rebalance 是什么？有什么问题？
- **Rebalance**：消费者组内成员变化（加入/退出/宕机）或订阅的分区变化时，重新分配分区。
- 过程：消费者组协调器触发，所有消费者暂停消费，等待重新分配。
- **问题**：Rebalance 期间"stop the world"，消费暂停，频繁触发影响性能。
- 优化：合理设置 `session.timeout.ms`、`heartbeat.interval.ms`、`max.poll.interval.ms`，避免消费者"假死"被踢出。

### Q37. RabbitMQ 和 Kafka 如何选择？
| 维度 | RabbitMQ | Kafka |
|------|----------|-------|
| 延迟 | 微秒级 | 毫秒级 |
| 吞吐 | 万级 | 百万级 |
| 顺序 | 队列级 | 分区级 |
| 持久化 | 默认刷盘 | PageCache + 顺序写 |
| 路由 | 丰富（4 种 Exchange） | 简单（Topic） |
| 适用 | 低延迟、高可靠、复杂路由 | 大数据、高吞吐、日志流 |
- 经验：业务消息用 RabbitMQ（低延迟），大数据/日志用 Kafka，电商核心链路也可用 RocketMQ（事务消息）。

### Q38. 消息幂等性怎么保证？
消费方幂等是核心，常见手段：
1. **唯一 ID + 去重表**：消息 ID 存表唯一索引，消费前查是否已处理。
2. **Redis 去重**：`setnx msg_id` 设标记，过期自动清理。
3. **状态机**：业务状态单向流转，`UPDATE ... WHERE status=?` 天然幂等。
4. **乐观锁**：版本号/version 校验。
5. **Token 机制**：一次性 token，提交后失效。
- 经验：MQ 不保证"恰好一次"（exactly-once），至少一次 + 幂等是工程标准方案。

### Q39. 消息堆积怎么处理？
1. **临时扩容**：增加消费者实例（注意分区数限制，Kafka 消费者数 ≤ 分区数）。
2. **异步批量处理**：消费者批量拉取 + 批量写 DB。
3. **转发分流**：消息转发到其他队列，分散消费。
4. **降级**：非核心消息丢弃或延迟处理。
5. **根因排查**：消费者是否慢、DB 是否瓶颈、是否死循环。
- 预防：监控队列堆积告警、合理设分区数、消费端限流保护。

### Q40. RocketMQ 事务消息原理？
两阶段 + 回查：
1. **半消息（Half Message）**：生产者先发半消息到 Broker，消费者不可见。
2. **执行本地事务**：半消息发送成功后，执行本地事务。
3. **提交/回滚**：本地事务成功则 commit（消息可见），失败则 rollback（消息删除）。
4. **事务回查**：若 commit/rollback 丢失，Broker 定时回查生产者事务状态，生产者返回 commit/rollback。
- 原理：半消息 + 本地事务 + 回查补偿，实现最终一致的分布式事务。
- 适用：电商下单 + 发消息扣库存等场景。

---

## 三、Elasticsearch（Q41–Q55）

### Q41. Elasticsearch 是什么？
Elasticsearch 是基于 Apache Lucene 的分布式全文搜索引擎，Java 开发，提供 RESTful API。
- 核心能力：**全文检索、结构化搜索、数据分析聚合**。
- 特点：分布式、近实时（NRT）、高可用、横向扩展、JSON 文档存储。
- 适用：日志搜索（ELK）、电商商品搜索、全文检索、指标分析。

### Q42. ES 的核心概念（与 DB 对比）？
| ES | DB | 说明 |
|----|----|------|
| Index | Database | 索引，文档集合 |
| Type（7.x 后移除） | Table | 逻辑分类，已废弃 |
| Document | Row | 一条 JSON 文档 |
| Field | Column | 字段 |
| Mapping | Schema | 字段类型定义 |
| Shard | 分区 | 数据分片 |
| Replica | 副本 | 分片副本 |
- 7.0 后一个 Index 只有一个 Type，8.0 后彻底移除 Type 概念。

### Q43. ES 的倒排索引是什么？
正排索引：文档 ID → 内容；倒排索引：**词 → 文档 ID 列表**。
```
文档1：hello world
文档2：hello es

倒排索引：
  hello → [文档1, 文档2]
  world → [文档1]
  es    → [文档2]
```
- 搜索 "hello" 直接查倒排表得到文档列表，无需遍历所有文档。
- 结构：Term Dictionary（词典）+ Term Index（词典索引，FST 加速定位）+ Posting List（文档列表，用 Roaring Bitmap 压缩）。

### Q44. ES 写入流程？
1. 请求到协调节点，路由到目标分片（`shard = hash(routing) % primary`）。
2. 主分片写入：
   - 写入 **translog**（ WAL，崩溃恢复用）。
   - 写入 **Indexing Buffer**（内存缓冲区）。
3. **refresh**（默认 1 秒）：Buffer → segment（内存中的倒排索引，可被搜索）。
4. **flush**（translog 满 512MB 或 30 分钟）：segment 落盘 + 清空 translog。
5. 副本同步：主分片成功后同步到副本。
- 关键：refresh 后数据可被搜索但未落盘，flush 才持久化；translog 保证未 flush 的数据可恢复。

### Q45. ES 的 near real-time（NRT）是什么？
- ES 不是真正的实时（Real-time），而是**近实时**：
  - refresh 默认 1 秒：写入后约 1 秒才可被搜索。
  - flush（落盘）更慢，按策略触发。
- 强实时需求：手动 `refresh` 或 `refresh=true` 参数，但性能下降。
- 关闭 refresh（`refresh_interval=-1`）可提升批量写入性能，写完再开。

### Q46. ES 如何保证高可用？
1. **分片副本（Replica）**：每个主分片有副本，主分片宕机副本提升为主。
2. **集群状态管理**：Master 节点维护集群状态（分片位置、节点列表）。
3. **数据恢复（Recovery）**：节点宕机后，Master 重新分配分片，副本提升或重新同步。
4. **Discovery**：基于 Zen Discovery（7.x 前）或 Coordination（7.x+），集群节点发现与 Master 选举。
- 推荐：3 个 Master-eligible 节点 + `minimum_master_nodes`（7.x 前防脑裂），数据节点和主节点分离。

### Q47. ES 的分片（Shard）与副本（Replica）？
- **主分片（Primary Shard）**：数据分片，索引创建时确定数量，后续可通过 split/reindex 调整。
- **副本分片（Replica Shard）**：主分片的拷贝，提供高可用 + 读负载均衡。
- 路由：`shard = hash(routing) % number_of_primary_shards`。
- 分片数建议：按数据量规划，单个分片建议 30-50GB，过多分片增加开销（集群状态、合并）。
- 副本数可动态调整（`number_of_replicas`）。

### Q48. ES 的 Master 选举与脑裂问题？
- **7.x 前**：Zen Discovery，Master 选举靠 `discovery.zen.minimum_master_nodes`（建议 `(N/2)+1`），防脑裂。
- **7.x+**：内置 Coordination，自动管理，`minimum_master_nodes` 废弃，默认安全配置防止脑裂。
- **脑裂**：网络分区导致两个 Master 同时存在，数据不一致。
- 预防：多数派选举、仲裁节点（候选 Master 节点数 ≥ 3 且为奇数）。

### Q49. ES 的检索流程（Query Then Fetch）？
两阶段：
1. **Query 阶段**：
   - 协调节点分发查询到所有分片（含副本）。
   - 各分片本地搜索，返回匹配文档的 **docID + score**（默认前 10）。
   - 协调节点合并、排序，取 Top N 的 docID。
2. **Fetch 阶段**：
   - 协调节点根据 docID 去**对应分片**取完整文档内容。
   - 合并返回给客户端。
- 优化：用 `preference` 控制路由、`routing` 只查特定分片、`source filtering` 只取需要的字段。

### Q50. ES 的打分机制（TF-IDF / BM25）？
- **TF-IDF**（5.0 前）：词频（TF）× 逆文档频率（IDF），经典但短文本缺陷。
- **BM25**（5.0+ 默认）：TF 饱和（词频到一定程度不再线性增长）+ 文档长度归一化，效果优于 TF-IDF。
- 公式：`score = BM25(query, doc)`，考虑词频、IDF、文档长度、字段长度。
- 可通过 `explain=true` 查看打分细节。

### Q51. ES 的聚合查询是什么？
分析统计能力，类似 SQL 的 GROUP BY：
- **Bucket Aggregation（桶聚合）**：按条件分组，如 `terms`（按字段值分桶）、`range`（范围）、`date_histogram`（按时间分桶）。
- **Metric Aggregation（指标聚合）**：计算指标，如 `avg`、`sum`、`max`、`cardinality`（基数去重，基于 HyperLogLog）。
- **Pipeline Aggregation（管道聚合）**：基于其他聚合结果再聚合。
- 注意：聚合基于 segment，text 字段需开启 `fielddata`（不推荐）或用 keyword 子字段。

### Q52. ES 中 keyword 和 text 类型的区别？
| 对比 | text | keyword |
|------|------|---------|
| 用途 | 全文检索 | 精确匹配、排序、聚合 |
| 分词 | 会分词（analyzer） | 不分词，整体作为一个 term |
| 索引 | 倒排索引（分词后） | 倒排索引（完整值） |
| 适用 | 文章内容、描述 | ID、标签、枚举值、城市名 |
- 常见做法：一个字段同时有 text 和 keyword 子字段：
```json
"city": {
  "type": "text",
  "fields": { "keyword": { "type": "keyword" } }
}
```
text 用于全文搜索，keyword 子字段用于排序和聚合。

### Q53. ES 的 deep paging 问题？
- 深度分页：`from + size` 很大（如 `from=10000, size=10`），每个分片取 `from+size` 条给协调节点合并，性能极差。
- 原因：Query 阶段每分片返回 `from+size`，协调节点排序 `(from+size) × 分片数` 条数据。
- **解决方案**：
  1. **scroll（游标）**：维护游标上下文，依次取下一页，适合全量导出，不适合实时分页（占用内存、状态过期）。
  2. **search_after**：基于上一页最后一条的排序值取下一页，无状态、适合实时深分页。
  3. 业务限制：限制最大分页深度（如只允许前 100 页）。
- 经验：`from + size` 默认上限 10000，深分页用 `search_after`。

### Q54. ES 如何调优？
**写入优化：**
1. 批量写入（Bulk API），减少请求次数。
2. 关闭/降低 refresh_interval（批量写时设 -1 或 30s）。
3. 增大 Indexing Buffer（`indices.memory.index_buffer_size`）。
4. 用 auto-generated ID（ES 不用查版本，写入更快）。
5. 合理设分片数，避免过多。

**查询优化：**
1. 用 filter 替代 query（filter 不打分、走缓存）。
2. 避免 wildcard 前缀查询（性能差）。
3. 用 keyword 做聚合、排序。
4. routing 路由只查特定分片。
5. 冷热分离（热数据 SSD、冷数据 HDD），ILM（Index Lifecycle Management）管理。

### Q55. ES 与 MySQL 数据同步怎么做？
1. **同步双写**：业务代码写 MySQL 后同步写 ES。简单但侵入强、不强一致。
2. **MQ 异步**：写 MySQL 发 MQ，消费者写 ES。解耦、削峰，需幂等。
3. **Canal 监听 binlog**：Canal 解析 MySQL binlog 异步同步到 ES。无侵入、准实时，主流方案。
4. **Logstash JDBC Input**：定时查 MySQL 增量同步 ES。简单但延迟高。
5. **DataX / Flink CDC**：批量或流式同步。
- 经验：生产主流是 Canal + MQ + ES 或 Flink CDC。

---

## 四、MySQL（Q56–Q75）

### Q56. MySQL 的存储引擎 InnoDB 和 MyISAM 的区别？
| 对比 | InnoDB | MyISAM |
|------|--------|--------|
| 事务 | 支持 | 不支持 |
| 锁粒度 | 行锁/表锁 | 表锁 |
| 外键 | 支持 | 不支持 |
| 聚簇索引 | 是（数据和主键索引一体） | 否（数据和索引分离） |
| 崩溃恢复 | 支持（redo log） | 不支持 |
| 适用 | OLTP 事务型 | 读密集只读表 |
- MySQL 5.5+ 默认 InnoDB，推荐使用。

### Q57. MySQL 索引底层为什么用 B+ 树？
- **对比二叉树/红黑树**：数据多时树高，磁盘 IO 次数多。
- **对比 B 树**：B+ 树非叶子节点只存索引键，叶子节点存数据，单节点能存更多 key，树更矮，IO 少。
- **对比 Hash**：Hash 等值查询快，但不支持范围查询和排序。
- B+ 树叶子节点形成**有序双向链表**，范围查询高效（`WHERE id BETWEEN`、`ORDER BY`）。
- InnoDB 聚簇索引叶子存整行，二级索引叶子存主键值，需回表。

### Q58. 聚簇索引和非聚簇索引的区别？
- **聚簇索引（Clustered）**：叶子节点存**整行数据**，数据和主键索引一体。一张表只有一个，通常是主键。
- **非聚簇索引（Secondary/二级索引）**：叶子节点存**主键值**，查到后需"回表"到聚簇索引取完整数据。
- 若查询字段都在二级索引上，无需回表，称**覆盖索引**。

### Q59. 联合索引和最左前缀原则？
- 联合索引 `(a, b, c)`，按 a→b→c 建立有序排列。
- 最左前缀：查询必须从最左列开始，跳过中间列后续列无法使用索引。
  - `WHERE a=? AND b=?` ✅ 命中 a,b
  - `WHERE a=? AND c=?` ✅ 命中 a（c 用不上索引定位但可 ICP 下推）
  - `WHERE b=?` ❌ 不命中
  - `WHERE a>? AND b=?` ✅ a 范围扫描后，b 可用 ICP 下推过滤（但无法参与索引定位）

### Q60. 索引失效的常见场景？
1. 不满足最左前缀（联合索引跳过左边列）。
2. 索引列做函数/运算：`WHERE YEAR(create_time)=2024`、`WHERE age+1=18`。
3. 隐式类型转换（字符串列传数字、编码不一致）。
4. `LIKE '%xxx'`（左侧通配符，右侧 `xxx%` 可以用索引）。
5. `OR` 连接非索引列。
6. `!=`、`NOT IN`、`IS NULL`（选择性差时优化器放弃索引）。
7. 优化器认为全表扫描更快（数据量小或选择率低）。
- 用 `EXPLAIN` 看 type、key、rows 判断是否命中。

### Q61. EXPLAIN 的重要字段？
| 字段 | 含义 | 理想值 |
|------|------|--------|
| type | 访问类型 | system > const > eq_ref > ref > range > index > ALL |
| key | 实际使用的索引 | 命中预期索引 |
| key_len | 索引使用长度 | 越长说明用到联合索引越多 |
| rows | 预估扫描行数 | 越小越好 |
| Extra | 额外信息 | Using index（覆盖）> Using index condition（ICP）> Using filesort（排序）|
| ref | 连接条件用到的列/常量 | — |
- `ALL` 全表扫描需优化；`Using filesort`/`Using temporary` 需优化排序和分组。

### Q62. MySQL 的事务隔离级别？
| 级别 | 脏读 | 不可重复读 | 幻读 |
|------|------|-----------|------|
| READ UNCOMMITTED | ✅ | ✅ | ✅ |
| READ COMMITTED | ❌ | ✅ | ✅ |
| REPEATABLE READ（默认） | ❌ | ❌ | ❌（InnoDB 解决） |
| SERIALIZABLE | ❌ | ❌ | ❌ |
- InnoDB 默认 RR，通过 MVCC + 间隙锁解决幻读。
- Oracle/PG 默认 RC。

### Q63. MVCC 是什么？怎么实现的？
- **MVCC（多版本并发控制）**：每行数据维护多个版本，读不加锁、读写不阻塞，提高并发。
- **实现（InnoDB）**：
  - 每行隐藏列：`trx_id`（最近修改的事务 ID）、`roll_pointer`（指向 undo log 中的旧版本）。
  - **Read View（读视图）**：事务开始时生成，记录当前活跃事务列表。
  - 读时：根据 trx_id 和 Read View 判断该版本是否可见，不可见则沿 roll_pointer 找历史版本。
- RC：每次 SELECT 都生成新 Read View（所以不可重复读）。
- RR：事务首次 SELECT 生成 Read View，后续复用（所以可重复读）。

### Q64. MySQL 的锁有哪些？
- **按粒度**：
  - **表锁**：锁整表，MyISAM 主要锁；InnoDB 的 DDL、意向锁也是表级。
  - **行锁**：锁单行/索引记录，InnoDB 默认。
- **按类型**：
  - **共享锁（S/读锁）**：`SELECT ... LOCK IN SHARE MODE`。
  - **排他锁（X/写锁）**：`SELECT ... FOR UPDATE`、DML。
- **InnoDB 行锁细分**：
  - **记录锁（Record Lock）**：锁单条索引记录。
  - **间隙锁（Gap Lock）**：锁索引间隙（防插入，解决幻读）。
  - **临键锁（Next-Key Lock）**：记录锁 + 间隙锁，RR 默认。
- 注意：InnoDB 行锁是**锁索引**，不是锁数据行本身；无索引则退化为表锁。

### Q65. redo log、undo log、binlog 的区别？
| 日志 | 作用 | 层级 | 写入时机 |
|------|------|------|---------|
| **redo log** | 崩溃恢复（持久性），物理日志 | InnoDB 引擎层 | 事务进行中持续写 |
| **undo log** | 回滚（原子性）+ MVCC，逻辑日志 | InnoDB 引擎层 | 修改前写旧值 |
| **binlog** | 主从复制 + 数据恢复，逻辑日志 | MySQL Server 层 | 事务提交时写 |
- **两阶段提交**（redo log + binlog）：保证 redo log 和 binlog 数据一致。
  1. 写 redo log（prepare 状态）。
  2. 写 binlog。
  3. 提交 redo log（commit 状态）。
- 崩溃恢复：redo log prepare 但未 commit → 检查 binlog 是否完整 → 完整则提交，否则回滚。

### Q66. MySQL 死锁是怎么产生的？如何排查？
- **死锁条件**：两个事务互相持有对方需要的锁，形成循环等待。
- 举例：事务 A 锁住行 1 等行 2；事务 B 锁住行 2 等行 1 → 死锁。
- **InnoDB 处理**：自动检测死锁（wait-for graph），回滚代价较小的事务，报 `Deadlock found`。
- **排查**：
  - `SHOW ENGINE INNODB STATUS` → 看 `LATEST DETECTED DEADLOCK`。
  - 开启 `innodb_print_all_deadlocks=ON`（所有死锁写错误日志）。
- **预防**：固定加锁顺序、事务尽量短、降低隔离级别、用索引避免行锁升级。

### Q67. 慢 SQL 如何优化？
1. 开启慢查询日志（`slow_query_log=ON`）定位慢 SQL。
2. `EXPLAIN` 查看执行计划：type、key、rows、Extra。
3. 建合适索引，遵循最左前缀与覆盖索引原则。
4. 避免 `SELECT *`，只查需要的列。
5. 大分页用游标/子查询：`WHERE id > last_id LIMIT 20`，避免 `OFFSET 100000`。
6. 拆分复杂 SQL，改写 JOIN 为小表驱动大表。
7. 数据量大考虑分库分表。

### Q68. MySQL 大分页为什么慢？怎么优化？
- 原因：`LIMIT 100000, 10` 会扫描 100010 行，丢弃前 10 万行，越深越慢。
- **优化方案**：
  1. **子查询/延迟关联**：`SELECT * FROM t WHERE id IN (SELECT id FROM t WHERE ... LIMIT 100000,10)`，子查询走覆盖索引。
  2. **游标分页**：`WHERE id > last_id ORDER BY id LIMIT 10`，记住上一页末尾 ID。
  3. 业务限制：限制最大分页深度，引导用户用条件筛选。
  4. 前端：瀑布流/加载更多替代深分页。

### Q69. MySQL 主从复制原理？
1. **Master** 写操作记录到 binlog。
2. **Slave** 的 IO 线程连接 Master，请求 binlog。
3. Master 的 **Dump 线程**发送 binlog 给 Slave。
4. Slave IO 线程写入 **relay log（中继日志）**。
5. Slave **SQL 线程**读 relay log，重放 SQL 到从库。
- **同步方式**：
  - 异步复制（默认）：Master 不等 Slave 确认，性能高但可能丢数据。
  - 半同步复制：Master 等至少一个 Slave 确认 binlog 接收才返回。
  - 组复制（MGR）：基于 Paxos，强一致，但性能低。

### Q70. 主从延迟的原因与解决？
- **原因**：
  1. 单线程重放 SQL（5.7 后有并行复制但仍有限）。
  2. Master 写入大事务、批量操作。
  3. Slave 性能差、负载高。
  4. 网络延迟。
- **解决**：
  1. 开启并行复制（`slave_parallel_workers`）。
  2. 避免大事务、批量分批。
  3. 读强制走主库（关键业务）。
  4. 用半同步复制减少延迟窗口。
  5. 分库分表降低单库压力。

### Q71. MySQL 什么时候需要分库分表？
- **分表**：单表数据量大（如 > 1000 万行），查询性能下降明显。
- **分库**：单库并发/连接数/IO 瓶颈。
- **判断依据**：
  1. 单表行数 > 1000 万或单表数据 > 50GB。
  2. 并发量 > 单库 QPS 上限（几千）。
  3. 慢 SQL 优化已到极限。
- **分表策略**：
  - 水平分表（按行）：按 hash/range/时间分。
  - 垂直分表（按列）：拆字段。
- **分库策略**：按业务垂直分库、按 hash/range 水平分库。
- 中间件：ShardingSphere、MyCat、Vitess。

### Q72. 分库分表后面临的问题？
1. **跨库 JOIN**：难以直接 JOIN，需应用层组装或冗余字段。
2. **分布式事务**：跨库事务需 Seata 等方案。
3. **全局唯一 ID**：自增 ID 失效，用雪花算法、号段模式（Leaf）。
4. **跨库分页/排序**：每个分页需聚合多库结果，性能差。
5. **数据迁移**：分表后扩容需重新 hash，数据迁移。
6. **运维复杂**：多库多表备份、监控、DDL 困难。

### Q73. MySQL 的 count(*)、count(1)、count(列名) 区别？
- `count(*)`：统计所有行（含 NULL），InnoDB 优化器选最小索引扫描，**推荐使用**。
- `count(1)`：类似 count(*)，扫描所有行，性能差异极小。
- `count(主键)`：扫描主键索引，等价于 count(*)，稍慢（要解析主键值）。
- `count(普通列)`：只统计该列非 NULL 的行数，语义不同。
- 性能：`count(*) ≈ count(1) > count(主键) > count(列)`（看是否解析值）。
- InnoDB 的 count 需要遍历（不像 MyISAM 有计数器），大表 count 慢，可估算或用 Redis 计数。

### Q74. MySQL 的回表和覆盖索引？
- **回表**：二级索引找到主键值后，再到聚簇索引查整行数据。
- **覆盖索引**：查询的字段都在索引上，二级索引直接返回，无需回表。
- EXPLAIN 中 Extra 显示 `Using index` 即覆盖索引。
- 优化：把高频查询字段加入联合索引，实现覆盖索引，避免回表。

### Q75. MySQL 的索引下推（ICP）是什么？
- **ICP（Index Condition Pushdown）**：MySQL 5.6 引入，把部分 WHERE 条件从 Server 层下推到存储引擎层。
- 场景：联合索引 `(a, b)`，`WHERE a LIKE 'x%' AND b > 10`，a 做范围扫描后 b 无法参与索引定位，但 b 的值在索引记录上，引擎层可直接判断，避免无谓回表。
- EXPLAIN Extra 显示 `Using index condition` 即 ICP 生效。
- 效果：减少回表次数和 Server 层处理行数。

---

## 速记口诀

| 主题 | 速记 |
|------|------|
| Redis | 五类型 + 两持久化（RDB 快/AOF 安全）+ 八淘汰（allkeys-lru 最常用）+ 三问题（穿透空值、击穿互斥锁、雪崩随机 TTL）+ 单线程快（内存+IO 多路复用+6.0 网络 IO 多线程）+ 渐进式 rehash（两表逐桶搬，读写双表查）+ 哨兵检测（每秒 PING→30 秒无回复 SDOWN→多数派 ODOWN→故障转移，Redis PING 非 TCP ACK）+ 分布式锁三防（看门狗续期+Lua 防误删+红锁/ZK 防主从丢锁）+ 看门狗（不传时间才启动，TTL=30s 每 10s 续一次） |
| MQ | 解耦/异步/削峰；RabbitMQ 低延迟高可靠（四 Exchange+死信+延迟队列）；Kafka 高吞吐（顺序写+零拷贝+PageCache+分区并行）；不丢失三环节（生产 ack/MQ 持久化/消费手动 ACK）；幂等靠唯一 ID+去重表 |
| ES | 倒排索引（词→文档）+ 写入 translog→buffer→refresh(1s)→flush；分片副本高可用；查询 Query then Fetch 两阶段；BM25 打分；深分页用 search_after；keyword 聚合、text 全文 |
| MySQL | InnoDB（事务+行锁+聚簇+MVCC）；B+ 树（非叶只存 key，叶子有序链表）；最左前缀；索引失效六类（函数/隐式转换/前缀 LIKE 等）；三日志（redo 持久/undo 回滚/binlog 复制）+ 两阶段提交；MVCC=Read View+undo 链 |
