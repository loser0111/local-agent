# 面试备战进度总览

> 更新时间：2026-10-23
> 候选人：王永庆（2024 届哈尔滨工程大学，网络安全专业）
> 经历：抖音生活服务（售后 Go）→ 携程（营销选品 Java + AI Agent）
> 求职方向：Java/Go 后端 + AI Agent 开发

---

## 一、面试文档总览

### 面试题库文档（4 份，共 270 题）

| 文档 | 题数 | 覆盖范围 |
|------|------|---------|
| `java-interview-100-questions.md` | 117 题 | Java 基础/OOP/集合/并发/JVM/异常IO/Spring/数据库缓存/微服务/高频深入题/计算机网络 |
| `middleware-interview-questions.md` | 78 题 | Redis(含哨兵检测Q13b/客户端感知Q13c/rehash Q22b) / MQ(RabbitMQ+Kafka+RocketMQ) / ES / MySQL |
| `agent-interview-questions.md` | 42 题 | Agent 基础/推理(ReAct/Plan-Execute)/工具调用(MCP)/记忆/多智能体/工程落地 |
| `rag-interview-questions.md` | 36 题 | RAG 基础/分块/Embedding/混合检索/Rerank(Cross-Encoder原理Q20b)/幻觉/评估 |

### 面试复盘文档（3 份）

| 文档 | 面试 | 日期 | 整体评分 |
|------|------|------|---------|
| `interview-real-review.md` | 金融后端（催收/触达） | 2026-10-21 | 5.5/10 |
| `interview-jt-review.md` | 极兔 HR/薪酬系统 | 2026-10-23 | 5.5/10 |
| `interview-mock-record.md` | 模拟练习记录（18 题 + 2 道算法） | 持续 | — |

### 项目介绍文档（2 份）

| 文档 | 项目 |
|------|------|
| `interview-self-introduction.md` | 自我介绍 + 三个项目 STAR 结构 + 离职话术 |
| `ibu-campaign-copilot-intro.md` | Campaign Copilot AI Agent 平台简介 |
| `ibu-campaign-frontend-selection-agent.md` | 前后端交互机制 + 选品子 Agent 实现 |

---

## 二、面试复盘核心结论

### 两次真实面试对比

| 维度 | 金融面试（10/21） | 极兔面试（10/23） |
|------|-----------------|-----------------|
| 项目深挖 | 配置化 + 一致性（扛住追问） | 规则引擎 + 性能优化（扛住追问） |
| 技术基础盲点 | MySQL 幻读答错 | 秒杀方案被指出瓶颈 |
| 表达能力 | 4/10（冗长） | 5.5/10（稍好） |
| 稳定性 | 3/10（归因外部） | 3.5/10（有 N+1 缓解） |
| 综合 | 5.5/10 | 5.5/10 |

### 已纠正的知识盲点

| 盲点 | 纠正前 | 纠正后 | 状态 |
|------|--------|--------|------|
| MySQL 幻读 | "RR 不能解决幻读" | ✅ RR 已解决，快照读 MVCC + 当前读间隙锁 | ✅ 已纠正 |
| 秒杀扣库存 | "Redis 查+锁+DB 同步扣" | ✅ Redis Lua 原子扣减 + MQ 异步更新 DB | ✅ 已纠正 |
| 两阶段提交 | "先写 redo 再写 DB" | ✅ redo prepare → binlog → redo commit | ✅ 已纠正 |
| Spring Boot 自动装配 | "配置加载到运行时变量" | ✅ AutoConfigurationImportSelector + spring.factories + 条件注解 | ✅ 已纠正 |
| Redis 哨兵客户端感知 | "写失败查从节点" | ✅ 哨兵 Pub/Sub +switch-master 主动通知 | ✅ 已纠正 |

### 仍需注意的点

1. **离职话术**：两次都归因外部，需加个人成长视角 + "这次想稳定"
2. **表达能力**：口头禅多（嗯/呃/不好意思），需练 STAR 结构 + "先结论后展开"
3. **ReentrantLock 是 JDK 层（AQS）不是 JVM 层**，升级为互斥锁不是读写锁
4. **ThreadLocal key 是 ThreadLocal 对象（弱引用）不是线程 ID**
5. **分布式锁防误删对比的是 value(UUID) 不是 key**

---

## 三、模拟练习记录

### 已完成的模拟练习轮次

| 轮次 | 题数 | 覆盖 | 均分 |
|------|------|------|------|
| 中间件八股 10 题 | 10 | Redis/MySQL/Kafka/ES/RabbitMQ | 6.5/10 |
| Agent & RAG 3 题 | 3 | ReAct/混合检索/工具失败+防死循环 | 7/10 |
| Java 八股 5 题（第一轮） | 5 | HashMap/锁升级/JVM GC/Spring 自动装配/ConcurrentHashMap | 5/10 |
| Java 八股 5 题（第二轮） | 5 | String/锁升级/索引设计/慢SQL/Spring Bean | 5.6/10 |
| Agent & RAG 5 题 | 5 | ReAct/MCP/记忆/多Agent/工程落地 | 7.4/10 |
| Java 十题考核 | 10 | ==/equals、ArrayList、锁升级、volatile、JVM、GC、Bean、事务、MVCC、缓存三问题 | 7.9/10 |
| Go 专项 5 题 | 5 | channel/defer/覆盖索引/混合持久化/Agentic RAG/限流 | 7.9/10 |
| Agent 专项 5 题 | 5 | ReAct/MCP/记忆/Multi-Agent/防死循环 | 7.4/10 |
| Go 后端专项 6 题 | 6 | GMP/next-key lock/AOF/ThreadLocal/分布式ID/slice | 6.9/10 |
| 后端综合 6 题 | 6 | Go interface/MySQL锁/主从复制/TCP握手/synchronized/分布式锁 | 7.2/10 |
| 专项加强 5 题 | 5 | MVCC/AQS/慢SQL/DB-DC一致性/线程池 | 4.1/10 |
| 后端+Agent 5 题 | 5 | Semaphore/Next-Key Lock/过期删除/自动装配/记忆系统 | 7.3/10 |

### 算法题练习

| 题目 | 评分 | 方案 |
|------|------|------|
| 链表倒数第 k 个节点 | 9/10 | 单指针+count 控制（快慢指针变体） |
| LRU 缓存 | 9.5/10 | HashMap + 双向链表 + 哨兵节点 |
| 数组第 K 大 | 8.5/10 | 小顶堆维护 K 个最大元素 |

---

## 四、面试题库新增内容记录（本会话）

### java-interview-100-questions.md 新增

| 题号 | 题目 | 新增内容 |
|------|------|---------|
| Q70b | CMS vs G1 选型 | 堆大小选型决策树 |
| Q49b | AQS 核心机制详解 | state/CLH队列/独占共享/公平非公平/加解锁流程/Condition |
| Q107 | 限流算法与实现 | 四种算法+单机vs分布式+令牌桶vs固定窗口 |
| Q108-Q117 | 计算机网络（10 题） | TCP握手/TCP挥手/UDP/可靠传输/HTTPS/HTTP版本/URL渲染/粘包/跨域/状态码 |

### middleware-interview-questions.md 新增

| 题号 | 题目 | 新增内容 |
|------|------|---------|
| Q13b | 哨兵检测主节点机制 | PING/SDOWN/ODOWN/quorum/Pub/Sub |
| Q13c | 客户端感知新主 | +switch-master 频道/兜底查询/Redisson |
| Q22b | Redis rehash 机制 | 渐进式 rehash/双表读写/rehashidx |

### rag-interview-questions.md 新增

| 题号 | 题目 | 新增内容 |
|------|------|---------|
| Q20b | Rerank 为什么 query+doc 拼一起编码 | Bi-Encoder vs Cross-Encoder/Cross-Attention/生产流程 |

### agent-interview-questions.md（本会话首次创建，42 题）

完整覆盖：基础概念/推理机制/工具调用MCP/记忆管理/多智能体/工程落地

---

## 五、技术薄弱点追踪

### 已掌握（稳定输出）

| 题目 | 练习次数 | 最新评分 |
|------|---------|---------|
| MySQL 幻读（RR 已解决） | 4 次 | 9.5/10 |
| Redis 缓存穿透/击穿/雪崩 | 3 次 | 9/10 |
| 秒杀扣库存 | 2 次 | 9/10 |
| Go slice 扩容 | 1 次 | 9.5/10 |
| LRU 缓存（算法） | 1 次 | 9.5/10 |
| HashMap 红黑树 | 3 次 | 9.5/10 |
| ReAct vs CoT | 3 次 | 8.5/10 |
| Agent 记忆系统 | 2 次 | 8.5/10 |
| Redis AOF 混合持久化 | 2 次 | 9/10 |
| 两阶段提交 | 2 次 | 8/10 |

### 仍需加强

| 题目 | 问题 | 优先级 |
|------|------|--------|
| synchronized vs ReentrantLock | ReentrantLock 是 JDK 不是 JVM，互斥锁不是读写锁 | P0 |
| ThreadLocal 内存泄漏 | key 是 ThreadLocal 对象不是线程 ID | P0 |
| Spring Boot 自动装配 | 缺 spring.factories 文件名 | P1 |
| MySQL next-key lock 未命中 | 纯间隙锁不含记录锁 | P1 |
| 分布式锁防误删 | 对比 value(UUID) 不是 key | P1 |
| AQS | 不熟悉，需背 state+CLH+CAS | P1 |
| 离职话术 | 归因外部，需加成长视角 | P0 |
| 表达能力 | 口头禅多，需练 STAR 结构 | P0 |

---

## 六、下一步建议

### 面试前必做（P0）

1. **离职话术**：背一版 30 秒版本，强调个人成长 + "这次想稳定"
2. **自我介绍**：练 2 分钟精炼版，录音回听减少口头禅
3. **synchronized vs ReentrantLock**：JVM 层 vs JDK 层(AQS)，互斥锁不是读写锁
4. **ThreadLocal**：key=ThreadLocal 对象(弱引用)，value=业务对象(强引用)，remove()
5. **MySQL 幻读**：开口就说"RR 已解决"

### 面试中注意

1. **先结论后展开**：每道题先一句话给答案，再展开分析
2. **术语精确**：直接说组件名（ThreadPoolExecutor/QMQ/JDK21），不等追问
3. **量化产出**：1000 条规则、90 个身份、耗时降 40-60%
4. **主动打消顾虑**：自我介绍末尾提"稳定""深耕"
5. **AI 工具不主动演示**：一句话带过，等追问再展开

### 持续复习

- Java 题库 117 题 + 中间件 78 题 + Agent 42 题 + RAG 36 题 = 共 273 题
- 重点复习已标记的薄弱点（见第五节）
- 算法题保持手感：LRU、快排变体、链表操作
