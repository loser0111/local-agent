# Java 高频面试 100 题（附答案）

> 参考多家权威来源并结合实战经验整理，按主题分类。每题附要点答案，便于快速复习。

---

## 目录

1. [Java 基础（Q1–Q12）](#一java-基础q1q12)
2. [面向对象（Q13–Q25）](#二面向对象q13q25)
3. [集合框架（Q26–Q40）](#三集合框架q26q40)
4. [多线程与并发（Q41–Q58）](#四多线程与并发q41q58)
5. [JVM（Q59–Q70）](#五jvmq59q70)
6. [异常处理与 IO（Q71–Q78）](#六异常处理与-ioq71q78)
7. [Spring / Spring Boot（Q79–Q88）](#七spring--spring-bootq79q88)
8. [数据库 / MyBatis / Redis（Q89–Q96）](#八数据库--mybatis--redisq89q96)
9. [微服务与分布式（Q97–Q100）](#九微服务与分布式q97q100)
10. [高频深入题（Q101–Q106）](#十高频深入题q101q106)

---

## 一、Java 基础（Q1–Q12）

### Q1. Java 的特点有哪些？
- **面向对象**：封装、继承、多态。
- **平台无关**：编译成字节码，由各平台 JVM 解释执行（Write Once, Run Anywhere）。
- **自动内存管理**：JVM 内置 GC，无需手动释放。
- **多线程**：语言级线程支持。
- **安全**：字节码校验、无指针运算、安全管理器。
- **生态成熟**：丰富的类库与开源框架。

### Q2. JDK、JRE、JVM 的区别？
- **JDK**（Java Development Kit）：开发工具包，包含 JRE + 编译器 javac + 调试等工具。
- **JRE**（Java Runtime Environment）：运行环境，包含 JVM + 核心类库，只能运行不能开发。
- **JVM**（Java Virtual Machine）：虚拟机，负责把字节码翻译成对应平台的机器码。
- 关系：JDK ⊃ JRE ⊃ JVM。

### Q3. Java 是编译型还是解释型语言？
两者兼有。源码先由 javac **编译**成 `.class` 字节码，再由 JVM **解释**执行；热点代码会被 JIT 编译器编译成机器码缓存，提升性能。

### Q4. Java 的数据类型有哪些？
- **基本类型（8 种）**：byte(1)、short(2)、int(4)、long(8)、float(4)、double(8)、char(2)、boolean(1)。
- **引用类型**：类、接口、数组、枚举。
- 基本类型直接存栈上（值传递），引用类型存堆上（传引用的副本）。

### Q5. == 和 equals() 的区别？
- `==`：比较栈中的值。基本类型比值，引用类型比内存地址。
- `equals()`：Object 默认也是比地址，但 String、Integer 等已重写为比较内容。
- 习惯：比较内容一律用 `equals()`，慎用 `==`。

### Q6. String、StringBuilder、StringBuffer 区别？
| 特性 | String | StringBuilder | StringBuffer |
|------|--------|---------------|--------------|
| 可变性 | 不可变 | 可变 | 可变 |
| 线程安全 | 是（不可变天然安全） | 否 | 是（synchronized） |
| 性能 | 拼接慢 | 最快 | 次之 |
- 拼接场景：单线程用 StringBuilder，多线程用 StringBuffer；常量用 String。

### Q7. String 为什么设计成不可变？
1. **线程安全**：不可变对象天然线程安全。
2. **hashCode 可缓存**：作为 HashMap key 不会变。
3. **安全性**：防止类加载器、数据库连接串被篡改。
4. **字符串常量池**：可共享，节省内存。

### Q8. 自动装箱与拆箱？
- 装箱：基本类型 → 包装类，如 `Integer i = 10` 实际调用 `Integer.valueOf(10)`。
- 拆箱：包装类 → 基本类型，如 `int n = i` 实际调用 `i.intValue()`。
- 陷阱：`Integer a = 127; Integer b = 127; a == b` 为 true（缓存 -128~127），128 则为 false。

### Q9. Java 中的值传递还是引用传递？
Java **只有值传递**。基本类型传值的副本，引用类型传引用的副本（副本指向同一对象，方法内改对象属性会影响外部，但重新赋值不影响）。

### Q10. final 关键字的作用？
- 修饰**类**：不可被继承（如 String）。
- 修饰**方法**：不可被重写，可被重载。
- 修饰**变量**：只能赋值一次，常量。
- 修饰**对象**：引用不可变，但对象内部属性可变。

### Q11. static 关键字的作用？
- 修饰**变量**：类变量，所有实例共享一份。
- 修饰**方法**：可通过类名直接调用，不能访问非 static 成员。
- 修饰**代码块**：类加载时执行一次。
- 修饰**内部类**：不依赖外部实例，只能访问外部类的 static 成员。

### Q12. 接口和抽象类的区别？
| 对比项 | 抽象类 | 接口 |
|--------|--------|------|
| 关系 | 单继承 | 多实现 |
| 构造方法 | 有 | 无 |
| 成员变量 | 任意 | 默认 public static final |
| 方法 | 可有普通方法 | 默认 public abstract（Java 8+ 允许 default/static） |
| 设计语义 | "是什么" | "能做什么" |

---

## 二、面向对象（Q13–Q25）

### Q13. 面向对象的三大特性？
- **封装**：隐藏内部实现，对外提供方法访问（private + getter/setter）。
- **继承**：子类复用父类非 private 成员，extends 单继承。
- **多态**：编译时类型与运行时类型不一致，父类引用指向子类对象，运行期动态绑定方法。

### Q14. 重载与重写的区别？
- **重载（Overload）**：同类中同名方法、参数列表不同（个数/类型/顺序），与返回值无关，属于**编译时多态**。
- **重写（Override）**：子类对父类同名同参方法重新实现，访问权限不能更严，抛出异常不能更大，属于**运行时多态**。

### Q15. 多态的实现方式？
1. 父类引用指向子类对象：`Animal a = new Dog()`。
2. 接口引用指向实现类：`List list = new ArrayList()`。
3. 方法重写 + 动态绑定。

### Q16. 构造方法的特点？
- 名与类名相同、无返回值类型（连 void 都没有）。
- 不写则默认有无参构造；一旦手写有参构造，无参构造不再自动生成。
- 不能被继承、不能被 final/static/synchronized 修饰。
- this() 调用本类其他构造，super() 调用父类构造，必须放首行。

### Q17. this 和 super 的区别？
- `this`：当前对象引用，可调本类成员与构造方法。
- `super`：父类对象引用，可调父类被覆盖的方法与构造方法。
- 构造方法中 this() 与 super() 互斥，只能出现一个且在首行。

### Q18. Java 的访问控制修饰符有哪些？
| 修饰符 | 本类 | 同包 | 子类 | 其他包 |
|--------|------|------|------|--------|
| public | ✅ | ✅ | ✅ | ✅ |
| protected | ✅ | ✅ | ✅ | ❌ |
| default | ✅ | ✅ | ❌ | ❌ |
| private | ✅ | ❌ | ❌ | ❌ |

### Q19. 内部类的几种形式？
- **静态内部类**：用 static 修饰，不依赖外部实例，最常用。
- **成员内部类**：持有外部类实例引用。
- **局部内部类**：定义在方法内，作用域仅限方法。
- **匿名内部类**：没有名字，常用于事件监听、回调、线程 Runnable。

### Q20. 抽象类和接口如何选择？
- 如果有共享的**字段和构造方法**，或子类间有强层次关系，选抽象类。
- 如果只是定义**行为规范**、需要多继承能力，选接口。
- Java 8 后接口有 default 方法，接口能力增强，现代设计更倾向接口优先。

### Q21. 深拷贝与浅拷贝？
- **浅拷贝**：只复制对象本身，引用类型字段仍指向同一对象（实现 Cloneable + super.clone()）。
- **深拷贝**：引用类型字段也复制一份，方式有：递归 clone、序列化/反序列化、JSON 往返。

### Q22. Java 的单例模式怎么实现？
```java
// 推荐写法：静态内部类，懒加载 + 线程安全
public class Singleton {
    private Singleton() {}
    private static class Holder {
        static final Singleton INSTANCE = new Singleton();
    }
    public static Singleton getInstance() {
        return Holder.INSTANCE;
    }
}
```
也可用双重检查锁（DCL）或枚举实现。

### Q23. Java 中的包装类缓存机制？
- Byte/Short/Integer/Long 缓存 -128~127；Character 缓存 0~127；Boolean 缓存 TRUE/FALSE。
- 在缓存范围内 `Integer.valueOf(127) == Integer.valueOf(127)` 为 true。
- 超出范围则为 false，比较内容务必用 equals()。

### Q24. Java 是不是纯面向对象语言？
不是。基本类型（int、boolean 等）不是对象，虽然有自动装箱弥补，但严格说不是纯 OOP（像 SmallTalk 那样）。Java 被称为"基于对象 + 面向对象"。

### Q25. Java 中的回调机制怎么实现？
典型用接口 + 匿名内部类：
```java
interface Callback { void onComplete(String result); }
void doWork(Callback cb) { /* ... */ cb.onComplete("done"); }
```
Java 8 后多用 Lambda 替代匿名内部类，本质仍是函数式接口回调。

---

## 三、集合框架（Q26–Q40）

### Q26. Java 集合体系结构？
两大顶层接口：
- **Collection**：List（有序可重复）、Set（无序不可重复）、Queue（队列）。
- **Map**：键值对，key 不可重复。
- 常见实现：ArrayList、LinkedList、HashSet、LinkedHashSet、TreeSet、HashMap、LinkedHashMap、TreeMap、ConcurrentHashMap。

### Q27. ArrayList 和 LinkedList 的区别？
| 对比 | ArrayList | LinkedList |
|------|-----------|------------|
| 底层 | 动态数组 | 双向链表 |
| 随机访问 | O(1) | O(n) |
| 插入删除（中间） | O(n) 需移动 | O(1) 改指针（但定位仍 O(n)） |
| 线程安全 | 否 | 否 |
- 读多写少用 ArrayList，频繁首尾增删用 LinkedList。

### Q28. HashMap 的底层原理？
- Java 8：数组 + 链表 + 红黑树。
- put 时按 key 的 hash 定位桶，冲突则用链地址法；链表长度 ≥8 且数组容量 ≥64 时转红黑树（≤6 退化为链表）。
- 默认初始容量 16，负载因子 0.75，扩容翻倍。
- 非线程安全，并发用 ConcurrentHashMap。

### Q29. HashMap 的 hash 计算为什么扰动？
`(h = key.hashCode()) ^ (h >>> 16)`，高 16 位与低 16 位异或，让高位也参与桶定位（数组容量小时只用低位），减少冲突。

### Q30. HashMap 链表为什么用尾插法（Java 8）？
Java 7 是头插法，多线程扩容可能形成环导致死循环。Java 8 改尾插法 + 红黑树，既保持顺序又避免环形链表。

### Q31. HashMap 与 HashTable 的区别？
| 对比 | HashMap | HashTable |
|------|---------|-----------|
| 线程安全 | 否 | 是（synchronized） |
| null key/value | 允许（key 最多一个 null） | 不允许 |
| 初始容量 | 16 | 11 |
| 扩容 | 翻倍 | 翻倍 +1 |
| 推荐 | 日常首选 | 已过时，并发用 ConcurrentHashMap |

### Q32. ConcurrentHashMap 的实现原理？
- Java 7：Segment 分段锁，默认 16 段，每段独立加锁。
- Java 8：弃用分段锁，改用 CAS + synchronized 锁单个桶头节点；并发度更高。
- 写：空桶用 CAS 插入，非空 synchronized 锁头节点；读：volatile 语义保证可见，通常不加锁。

### Q33. HashSet 的底层是什么？
基于 HashMap：元素作为 key，value 固定为一个 `PRESENT` 对象。利用 key 不可重复的特性实现去重。

### Q34. TreeMap 的底层？
红黑树，key 有序（自然排序或 Comparator），增删查 O(log n)。适合需要按 key 排序遍历的场景。

### Q35. fail-fast 与 fail-safe 的区别？
- **fail-fast**：遍历时若结构被直接修改（modCount 变化）抛 ConcurrentModificationException，如 ArrayList 的迭代器。
- **fail-safe**：遍历的是副本，如 CopyOnWriteArrayList、ConcurrentHashMap，不抛异常但可能读不到最新值。

### Q36. ArrayList 扩容机制？
- 默认容量 10（首次 add 后才分配）。
- add 前检查 `size+1` 是否超过容量，超过则 `grow()`：新容量 = 旧容量 + 旧容量 >> 1（即 1.5 倍），不够再按需求扩。
- 用 `Arrays.copyOf` 拷贝到新数组。

### Q37. Iterator 和 ListIterator 的区别？
- Iterator 可正向遍历 List/Set，支持 remove。
- ListIterator 只能用于 List，支持双向遍历（hasPrevious）、add、set、获取索引。

### Q38. Comparable 和 Comparator 的区别？
- **Comparable**：类内部实现 `compareTo`，定义自然顺序，一个类只能有一种排序。
- **Comparator**：外部定义比较器 `compare`，可定义多种排序，灵活。`Collections.sort(list, cmp)`。

### Q39. 如何让集合线程安全？
1. `Collections.synchronizedXxx()` 包装（全局锁，性能差）。
2. `CopyOnWriteArrayList` / `ConcurrentHashMap`（分段/无锁，并发友好）。
3. `Vector`、`HashTable`（已不推荐）。

### Q40. Collections 常用方法？
`sort()` 排序、`reverse()` 反转、`shuffle()` 打乱、`binarySearch()` 二分查找、`unmodifiableList()` 不可修改、`synchronizedList()` 同步包装、`singletonList()` 单元素不可变集合。

---

## 四、多线程与并发（Q41–Q58）

### Q41. 创建线程的几种方式？
1. 继承 Thread，重写 run()。
2. 实现 Runnable，传入 Thread。
3. 实现 Callable + FutureTask，可有返回值。
4. 线程池 ExecutorService.submit()（生产推荐）。
- Java 19+ 还可用虚拟线程 `Thread.startVirtualThread()`。

### Q42. 线程的生命周期（Java 线程状态）？
NEW（创建未启动）→ RUNNABLE（就绪/运行，JVM 不区分）→ BLOCKED（等 monitor 锁）→ WAITING（wait/join 无超时）→ TIMED_WAITING（sleep/wait 带时间）→ TERMINATED。共 6 种状态（Thread.State）。

### Q43. start() 和 run() 的区别？
- `start()`：启动新线程并自动调用 run()，一个线程只能 start 一次。
- `run()`：普通方法调用，在当前线程同步执行，不产生新线程。

### Q44. sleep() 和 wait() 的区别？
| 对比 | sleep | wait |
|------|-------|------|
| 所属 | Thread 静态方法 | Object 方法 |
| 释放锁 | 不释放 | 释放 |
| 使用位置 | 任意 | synchronized 块内 |
| 唤醒 | 超时自动 | notify/notifyAll 或超时 |

### Q45. synchronized 的原理？
- 修饰方法：锁当前对象实例（静态方法锁 Class 对象）。
- 修饰代码块：锁括号里指定对象。
- 底层基于 Monitor 对象，monitorenter/monitorexit 指令。
- JVM 中对象头 Mark Word 记录锁状态：无锁→偏向锁→轻量级锁→重量级锁（可升级不可降级）。

### Q46. volatile 的作用？
1. **可见性**：写后立即刷主存，读强制从主存读，其他线程可见。
2. **禁止指令重排**：插入内存屏障。
3. **不保证原子性**：`i++` 仍不安全，需用 AtomicInteger 或 synchronized。
- 适用场景：状态标志位、双重检查锁单例。

### Q47. synchronized 和 volatile 的区别？
- volatile 轻量、不阻塞、只保证可见性和有序性；synchronized 重量、阻塞、保证三大特性（可见、有序、原子）。
- volatile 用于状态变量，synchronized 用于临界区整体。

### Q48. CAS 是什么？有什么问题？
- **CAS（Compare And Swap）**：乐观锁，比较当前值与预期值，相等才更新，无锁。
- 底层 Unsafe + CPU cmpxchg 指令。AQS、Atomic 系列都基于它。
- 问题：ABA（用 AtomicStampedReference 加版本号解决）、自旋开销大、只能保证一个变量原子。

### Q49. AQS 是什么？
- **AbstractQueuedSynchronizer**，并发框架核心。用一个 volatile int state 表示同步状态 + FIFO 双向队列管理等待线程。
- ReentrantLock、Semaphore、CountDownLatch、ReentrantReadWriteLock 都基于它。
- 独占模式与共享模式两种。

### Q49b. AQS 的核心机制详解

**两个核心组件：**

**① state（同步状态）**
- `volatile int state`，用 CAS 修改，保证可见性。
- 含义随实现不同：

| 实现类 | state 含义 |
|--------|-----------|
| ReentrantLock | 重入次数（0=未加锁，2=重入2次） |
| Semaphore | 许可证数量 |
| CountDownLatch | 剩余计数（0时放行） |
| ReentrantReadWriteLock | 高16位读 / 低16位写 |

**② CLH 队列（等待队列）**
- FIFO 双向队列（CLH 变体），存放等待获取锁的线程。
- 每个 Node 封装：等待线程 + waitStatus（SIGNAL/CANCELLED/CONDITION/PROPAGATE）+ 前驱/后继指针。
- head 是哨兵节点（dummy），实际等待线程从 head.next 开始。

**两种模式：**

| 模式 | 同时持有者 | 获取/释放方法 | 代表实现 |
|------|-----------|-------------|---------|
| **独占模式** | 1 个线程 | tryAcquire / tryRelease | ReentrantLock |
| **共享模式** | 多个线程 | tryAcquireShared / tryReleaseShared | Semaphore、CountDownLatch |

- 独占模式：CAS 抢 state → 失败则封装 Node 入 CLH 队列 park → 前驱释放时 unpark 唤醒后继。
- 共享模式：多个线程可同时获取（如 Semaphore state=3 → 3 个线程各拿 1 个许可），释放后唤醒后继并**传播**（PROPAGATE）。

**公平锁 vs 非公平锁：**

| 对比 | 非公平锁（默认） | 公平锁 |
|------|-----------------|--------|
| 新线程行为 | 直接 CAS 抢，可能插队 | 先查队列 `hasQueuedPredecessors()`，有人排队就排后面 |
| 吞吐量 | 高（减少线程切换） | 低（严格排队） |
| 公平性 | 可能饿死某些线程 | 不会饥饿 |
- `new ReentrantLock(true)` 为公平锁。

**加锁流程（非公平 ReentrantLock 为例）：**
```
线程调用 lock()
  → CAS 尝试 state 0→1
    → 成功 → 设持有者 → 加锁成功
    → 失败 → 判断是否重入（当前线程 == 持有者）
      → 是 → state+1 重入
      → 否 → tryAcquire 失败 → 封装 Node 入 CLH 队列
        → 自旋检查前驱是不是 head
          → 是 → 再试 tryAcquire
            → 成功 → 设自己为 head
            → 失败 → 前驱设为 SIGNAL(-1) → LockSupport.park 挂起
          → 不是 → park 挂起
```

**解锁流程：**
```
线程调用 unlock()
  → tryRelease：state-1
    → state > 0 → 还在重入，不算释放
    → state = 0 → 真正释放
      → 检查 head 的 waitStatus
      → 是 SIGNAL → CAS 改为 0 → LockSupport.unpark(后继线程)
      → 后继线程被唤醒 → 从 park 处继续 → 自旋抢锁
```

**Condition 条件变量：**
- AQS 支持多个 Condition（类似 wait/notify 但更灵活）。
- AQS 内部维护两个队列：**同步队列（CLH）**等锁的线程 + **条件队列**await 等待的线程。
- `await()`：释放锁 + 进入条件队列等待。
- `signal()`：从条件队列移到同步队列，等重新获取锁后继续。

**基于 AQS 的同步器全貌：**

| 同步器 | 模式 | state 含义 | 特点 |
|--------|------|-----------|------|
| ReentrantLock | 独占 | 重入次数 | 可公平/非公平，可重入 |
| ReentrantReadWriteLock | 独占+共享 | 高16位读/低16位写 | 读共享、写独占 |
| Semaphore | 共享 | 许可证数 | 控制并发访问数 |
| CountDownLatch | 共享 | 剩余计数 | 一次性，countDown到0放行 |
| CyclicBarrier | 独占（间接） | — | 基于 ReentrantLock+Condition |
| StampedLock | 独占+共享 | 戳状态 | 乐观读，性能更高 |

**速记：** AQS = volatile state（状态）+ CLH 队列（排队）+ CAS（抢锁）；独占 ReentrantLock / 共享 Semaphore+CountDownLatch；非公平直接抢、公平先查队列；加锁 CAS→失败入队park→释放unpark唤醒后继。

### Q50. ReentrantLock 与 synchronized 的区别？
| 对比 | synchronized | ReentrantLock |
|------|---------------|----------------|
| 实现 | JVM 层关键字 | JDK 层类 |
| 灵活性 | 自动释放 | 需手动 unlock（finally） |
| 可中断 | 否 | 可 tryLock(timeout) |
| 公平锁 | 只有非公平 | 可选公平/非公平 |
| 条件变量 | 一个 wait/notify | 多个 Condition |
- 简单同步用 synchronized，高级控制用 ReentrantLock。

### Q51. 什么是死锁？怎么排查？
四个必要条件：互斥、占有等待、不可剥夺、循环等待。
排查：`jstack` 打印线程栈找死锁块；`jconsole`/`arthas` 可视化；预防：固定加锁顺序、超时获取锁、避免嵌套。

### Q52. 线程池的核心参数？
ThreadPoolExecutor 七参数：
1. corePoolSize 核心线程数
2. maximumPoolSize 最大线程数
3. keepAliveTime 空闲存活时间
4. unit 时间单位
5. workQueue 任务队列
6. threadFactory 线程工厂
7. handler 拒绝策略

### Q53. 线程池的工作流程？
1. 任务来先判断核心线程是否满，未满创建核心线程执行。
2. 满了入队列排队。
3. 队列满了判断是否达到最大线程数，未满创建非核心线程。
4. 达到最大走拒绝策略：AbortPolicy（抛异常，默认）、CallerRunsPolicy（调用者执行）、DiscardPolicy（丢弃）、DiscardOldestPolicy（丢最老任务）。

### Q54. 线程池为什么推荐用 ThreadPoolExecutor 而不是 Executors？
- `Executors.newFixedThreadPool`/`newSingleThreadExecutor` 用无界 LinkedBlockingQueue，可能 OOM。
- `newCachedThreadPool` 最大线程数 Integer.MAX_VALUE，可能创建大量线程 OOM。
- 手动用 ThreadPoolExecutor 能显式控制队列容量与线程数，规避 OOM。

### Q55. ThreadLocal 的原理？
- 每个线程持有一个 ThreadLocalMap，key 为 ThreadLocal 弱引用，value 为业务对象。
- 实现线程间变量隔离，典型用于数据库连接、用户上下文。
- 内存泄漏：value 是强引用，线程不结束则不回收。用完务必 `remove()`。
- InheritableThreadLocal 可传递给子线程。

### Q56. CountDownLatch、CyclicBarrier、Semaphore 的区别？
- **CountDownLatch**：一次性，主线程等待 N 个子任务完成（countDown 到 0）。
- **CyclicBarrier**：可复用，多个线程互相等待到达屏障点后一起继续。
- **Semaphore**：信号量，控制并发访问数量， acquire/release。

### Q57. Fork/Join 框架是什么？
用于分治任务的并发框架：把大任务递归拆成小任务，用工作窃取（work-stealing）算法让空闲线程从其他线程队列尾端偷任务，提高利用率。Java 的 parallelStream 底层就是它。

### Q58. Java 内存模型（JMM）是什么？
JMM 规定线程与主存的关系：每个线程有工作内存（CPU 缓存/寄存器），共享变量在主存。定义 happens-before 规则保证有序性与可见性：解锁 happens-before 后续加锁、volatile 写 happens-before 读、线程 start happens-before 它的动作等。volatile、synchronized、final 的内存语义都由 JMM 定义。

---

## 五、JVM（Q59–Q70）

### Q59. JVM 的运行时数据区有哪些？
- **线程私有**：程序计数器、虚拟机栈、本地方法栈。
- **线程共享**：堆、方法区（Java 8+ 元空间 Metaspace，移出堆外）。
- 程序计数器记录当前执行字节码行号；虚拟机栈存栈帧（局部变量表、操作数栈、动态链接、返回地址）。

### Q60. 堆和栈的区别？
- **栈**：线程私有，存基本类型局部变量、对象引用、方法调用栈帧，LIFO 自动回收，速度快。
- **堆**：线程共享，存对象实例与数组，由 GC 管理，分新生代/老年代。
- 栈溢出 StackOverflowError（递归过深），堆溢出 OutOfMemoryError。

### Q61. Java 对象的创建过程？
1. 类加载检查（是否已加载并解析）。
2. 分配内存（指针碰撞 / 空闲列表）。
3. 内存空间初始化为零值。
4. 设置对象头（Mark Word、类型指针、数组长度）。
5. 执行 `<init>` 构造方法赋值。

### Q62. 对象的内存布局？
- **对象头**：Mark Word（哈希、GC 分代年龄、锁状态）+ 类型指针（指向 Class 元数据）。
- **实例数据**：各字段值（按类型对齐）。
- **对齐填充**：保证对象大小是 8 字节整数倍。

### Q63. 类加载的过程？
加载 → 验证 → 准备 → 解析 → 初始化 → 使用 → 卸载。其中验证/准备/解析合称连接。
- 加载：通过类全限定名获取字节流。
- 准备：为静态变量分配内存并赋零值。
- 解析：符号引用转直接引用。
- 初始化：执行 `<clinit>` 静态代码块与静态变量赋值。

### Q64. 双亲委派模型？
- 类加载请求先委托父加载器，父加载不到再自己加载。
- 层次：BootstrapClassLoader（rt.jar）→ ExtClassLoader/PlatformClassLoader → AppClassLoader → 自定义。
- 好处：防止核心类被篡改（如自定义 java.lang.String 会被 Bootstrap 的 String 覆盖），保证唯一性。
- 打破场景：Tomcat（每个 webapp 独立 ClassLoader，先自己加载）、SPI、JDBC（用 ContextClassLoader）。

### Q65. 垃圾回收如何判断对象死亡？
- **引用计数法**（基本不用，循环引用问题）。
- **可达性分析**：从 GC Roots 向下搜索，不可达的对象为可回收。GC Roots 包括：虚拟机栈局部变量、本地方法栈引用、方法区静态/常量引用、JNI 引用。

### Q66. 引用的分类？
- **强引用**（Strong）：`Object o = new Object()`，只要强可达就不回收。
- **软引用**（Soft）：内存不足时回收，SoftReference，用于缓存。
- **弱引用**（Weak）：下次 GC 必回收，WeakReference，如 ThreadLocalMap 的 key。
- **虚引用**（Phantom）：不影响回收，仅用于跟踪回收通知，PhantomReference。

### Q67. 常见的垃圾回收算法？
- **标记-清除**：标记存活、清除死亡，产生碎片。
- **标记-复制**：存活对象复制到另一块，无碎片但浪费空间，用于新生代。
- **标记-整理**：存活对象向一端移动，无碎片但慢，用于老年代。
- **分代收集**：新生代用复制，老年代用标记-整理/清除。

### Q68. 常见 GC 收集器？
- **Serial / Serial Old**：单线程，适合客户端。
- **ParNew / Parallel Scavenge + Parallel Old**：多线程吞吐量优先。
- **CMS**：老年代并发，低停顿，已淘汰。
- **G1**：分 Region，可预测停顿，Java 9 默认。
- **ZGC / Shenandoah**：着色指针/读屏障，亚毫秒级停顿，Java 15+ 生产可用。

### Q69. JVM 调优常见参数？
- `-Xms` / `-Xmx`：初始/最大堆大小，建议相等避免扩容抖动。
- `-Xmn`：新生代大小。
- `-XX:MetaspaceSize` / `-XX:MaxMetaspaceSize`：元空间。
- `-XX:+UseG1GC`：启用 G1。
- `-XX:MaxGCPauseMillis`：目标最大停顿。
- `-XX:+PrintGCDetails`：打印 GC 日志。

### Q70. 内存泄漏的常见场景？
1. 静态集合持有对象引用不释放。
2. ThreadLocal 用完未 remove。
3. 监听器/回调未注销。
4. 连接/流未关闭。
5. 缓存无淘汰策略（可换 WeakHashMap 或 Guava Cache）。

### Q70b. 什么情况下适合 CMS，什么情况下适合 G1？

**一句话选型：** 堆小于 6GB 用 CMS，堆大于 6GB 用 G1。

**CMS vs G1 核心对比：**

| 维度 | CMS | G1 |
|------|-----|-----|
| 堆布局 | 物理分代（连续区域） | Region 化（格子动态角色） |
| 回收算法 | 标记-清除（**有碎片**） | 标记-复制（**无碎片**） |
| 老年代回收 | 全老年代扫描 | 只回收垃圾最多的几个 Region |
| 停顿可控 | 不可预测 | ✅ MaxGCPauseMillis 可预测 |
| 碎片 | 有（需定期 Full GC compact） | 无（复制式） |
| 适合堆大小 | <6GB（中小堆） | 6GB-64GB+（大堆） |
| Full GC | 并发减少 | 退化单线程（更差，要避免） |
| 状态 | Java 9 废弃，14 移除 | Java 9+ 默认 |

**适合 CMS 的场景：**
- 堆 < 6GB，中小堆，CMS 并发标记效率够用。
- 追求低停顿，对延迟敏感。
- 老年代碎片可容忍（堆不大碎片积累慢）。
- 大对象少，碎片积累慢。
- **注意**：CMS 用标记-清除有碎片，碎片多了触发 Full GC（STW 停顿很长）；Concurrent Mode Failure 退化为 Full GC；Java 9 废弃 14 移除，新项目不要用。

**适合 G1 的场景：**
- 堆 6GB-64GB+，大堆 Region 化设计优势明显。
- 需要可控停顿，MaxGCPauseMillis 设目标。
- 碎片化是痛点，G1 复制式无碎片。
- 大对象多，Humongous Region 专门处理。
- 多核 CPU，G1 并行标记和复制回收能利用多核。
- **注意**：堆太小（<6GB）用 G1 不如 Parallel；MaxGCPauseMillis 不要设太小（<50ms 导致频繁 GC）；避免 Full GC（退化单线程停顿极长）。

**选型决策树：**
```
堆大小？
├── < 4GB  → Parallel Scavenge + Parallel Old（吞吐量优先）
├── 4-6GB  → CMS（低停顿）或 G1（Java 9+ 直接用 G1）
├── 6-64GB → G1 ✅ 首选
└── 64GB+  → G1 或 ZGC（亚毫秒级停顿，Java 15+ 生产可用）
```

**速记：** 堆小（<6G）+ 低停顿 → CMS（有碎片，已废弃）；堆大（>6G）+ 可控停顿 + 无碎片 → G1（Java 9+ 默认）；堆超大 → ZGC。

---

## 六、异常处理与 IO（Q71–Q78）

### Q71. Java 异常体系结构？
- `Throwable` 是顶层父类，分两支：
  - **Error**：JVM 错误，程序无法处理（OutOfMemoryError、StackOverflowError）。
  - **Exception**：
    - **受检异常（Checked）**：编译期强制处理，如 IOException、SQLException。
    - **非受检异常（Unchecked/Runtime）**：RuntimeException 及子类，如 NullPointerException、ClassCastException。

### Q72. throw 和 throws 的区别？
- `throw`：在方法体内手动抛出一个异常对象。
- `throws`：方法签名声明可能抛出的异常类型，交给调用者处理。
- throw 后执行流中断，throws 是声明义务。

### Q73. try-catch-finally 的执行顺序？
1. try 正常则 finally 执行；try 抛异常则匹配 catch，再执行 finally。
2. finally 一定执行（除非 System.exit 或 JVM 崩溃）。
3. finally 中的 return 会覆盖 try/catch 的 return，故避免在 finally 中 return。
4. try-with-resources（Java 7+）自动关闭实现 AutoCloseable 的资源，优于手动 finally close。

### Q74. 自定义异常怎么写？
```java
public class BizException extends RuntimeException {
    private int code;
    public BizException(int code, String msg) {
        super(msg);
        this.code = code;
    }
}
```
- 业务异常一般继承 RuntimeException（非受检，不强制 try-catch），配合全局异常处理器。

### Q75. Java 的 IO 流体系？
- 按流向：输入流 / 输出流。
- 按类型：字节流（InputStream/OutputStream） / 字符流（Reader/Writer）。
- 按功能：节点流（直接读源） / 处理流（包装，如 Buffered、Data）。
- 字节流读二进制（图片、压缩包），字符流读文本（需指定编码）。

### Q76. BIO、NIO、AIO 的区别？
| 模式 | 特点 | 典型 |
|------|------|------|
| BIO | 同步阻塞，一个连接一个线程 | 传统 Socket |
| NIO | 同步非阻塞，多路复用 Selector + Channel + Buffer | Netty、Java NIO |
| AIO | 异步非阻塞，操作系统回调 | AIO（Linux 支持有限，实际多用 NIO） |

### Q77. NIO 的核心组件？
- **Buffer**：数据容器，capacity/position/limit，flip() 翻转读写模式。
- **Channel**：双向通道，可读可写（FileChannel、SocketChannel、ServerSocketChannel）。
- **Selector**：选择器，单线程监听多个 Channel 的事件（OP_READ/WRITE/CONNECT/ACCEPT），实现多路复用。

### Q78. 序列化与反序列化？
- 序列化：对象 → 字节流，实现 Serializable 接口，用 ObjectOutputStream.writeObject。
- 反序列化：字节流 → 对象，ObjectInputStream.readObject。
- 注意：serialVersionUID 要显式指定，否则类结构变化会反序列化失败；敏感字段用 transient 跳过；现代推荐 JSON / Protobuf，可读性好且跨语言。

---

## 七、Spring / Spring Boot（Q79–Q88）

### Q79. Spring 的核心模块有哪些？
- **Core Container**：BeanFactory（工厂模式）+ ApplicationContext（扩展上下文）。
- **AOP**：面向切面编程，事务、日志、权限。
- **DAO/ORM**：JDBC 封装与 ORM 集成。
- **Web**：Spring MVC。
- **声明式事务**：@Transactional。
- 核心思想：IoC 控制反转 + AOP 切面 + DI 依赖注入。

### Q80. IoC 和 DI 的区别？
- **IoC（Inversion of Control）**：对象创建权由容器掌握，不由对象自己 new。
- **DI（Dependency Injection）**：容器在运行时把依赖注入对象，是 IoC 的实现方式。
- 注入方式：构造方法注入（推荐）、setter 注入、字段注入（@Autowired/@Resource，不推荐）。

### Q81. Bean 的生命周期？
1. **实例化**：通过构造方法/工厂方法创建对象。
2. **属性赋值**：DI 注入依赖。
3. **Aware 接口回调**：BeanNameAware、ApplicationContextAware 等。
4. **BeanPostProcessor.postProcessBeforeInitialization**。
5. **初始化**：@PostConstruct → InitializingBean.afterPropertiesSet → init-method。
6. **BeanPostProcessor.postProcessAfterInitialization**（AOP 代理在此生成）。
7. **使用**。
8. **销毁**：@PreDestroy → DisposableBean.destroy → destroy-method。

### Q82. Bean 的作用域（Scope）？
- **singleton**（默认）：单例，容器中唯一。
- **prototype**：每次获取新建。
- request：每次 HTTP 请求一个（Web）。
- session：每个会话一个。
- 自定义 scope 也支持。

### Q83. @Autowired 和 @Resource 的区别？
| 对比 | @Autowired | @Resource |
|------|------------|-----------|
| 来源 | Spring | JSR-250 标准 |
| 默认 | 按类型注入 | 按名称注入 |
| 找不到多个时 | 配 @Qualifier 指定名 | name 属性指定 |
- 推荐构造方法注入，避免字段注入带来的循环依赖与不可变性缺失。

### Q84. Spring AOP 的实现原理？
- 有接口时用 **JDK 动态代理**（基于接口反射）。
- 无接口时用 **CGLIB 代理**（生成子类，final 类/方法不能代理）。
- Spring 5+ 默认 CGLIB（spring.aop.proxy-target-class=true）。
- 常见通知类型：前置、后置、环绕、异常、返回。

### Q85. Spring 事务的传播行为？
七种，常用的：
- **REQUIRED**（默认）：有则加入，无则新建。
- **REQUIRES_NEW**：总是新建，挂起当前事务。
- **NESTED**：嵌套事务，基于 Savepoint。
- SUPPORTS / NOT_SUPPORTED / NEVER / MANDATORY。
- 注意：同类内方法直接调用不会触发 AOP 事务，需从代理对象调用（如注入自身或 AopContext.currentProxy()）。

### Q86. Spring Boot 的自动装配原理？
- 启动类 `@SpringBootApplication` 内含 `@EnableAutoConfiguration`。
- 通过 `AutoConfigurationImportSelector` 读取 `META-INF/spring.factories`（2.7+ 改用 `AutoConfiguration.imports`）。
- 每个自动配置类用 `@ConditionalOnClass` / `@ConditionalOnMissingBean` 等条件装配，实现"约定大于配置"。
- 可在 `application.yml` 通过前缀覆盖默认值，或 `@SpringBootApplication(exclude=...)` 排除。

### Q87. Spring Boot 的启动流程？
1. SpringApplication.run 启动，创建 SpringApplication 实例（推断应用类型、加载 Listener、读 application.properties/yml）。
2. 准备环境（Environment）、打印 Banner。
3. 创建 ApplicationContext（Servlet/WebFlux/Reactive）。
4. refreshContext：自动装配 Bean、启动 Tomcat（嵌入式容器）、注册 BeanPostProcessor。
5. 执行 ApplicationRunner / CommandLineRunner。
6. 发布 ApplicationReadyEvent。

### Q88. Spring Boot 常用注解？
- `@RestController` = @Controller + @ResponseBody。
- `@RequestMapping` / `@GetMapping` / `@PostMapping`：路由。
- `@RequestParam` / `@PathVariable` / `@RequestBody`：参数绑定。
- `@Service` / `@Repository` / `@Component` / `@Configuration`：组件注册。
- `@Transactional`：声明式事务。
- `@Value` / `@ConfigurationProperties`：配置注入。
- `@SpringBootApplication`：启动注解。

---

## 八、数据库 / MyBatis / Redis（Q89–Q96）

### Q89. MySQL 的存储引擎 InnoDB 与 MyISAM 的区别？
| 对比 | InnoDB | MyISAM |
|------|--------|--------|
| 事务 | 支持 | 不支持 |
| 锁粒度 | 行锁/表锁 | 表锁 |
| 外键 | 支持 | 不支持 |
| 聚簇索引 | 是 | 否（非聚簇） |
| 崩溃恢复 | 支持（redo log） | 不支持 |
- MySQL 5.5+ 默认 InnoDB，推荐使用。

### Q90. MySQL 的事务隔离级别与并发问题？
四种隔离级别：
1. **读未提交（READ UNCOMMITTED）**：脏读。
2. **读已提交（READ COMMITTED）**：Oracle 默认，解决脏读，不可重复读。
3. **可重复读（REPEATABLE READ）**：MySQL 默认，解决不可重复读，InnoDB 通过 MVCC + 间隙锁解决幻读。
4. **串行化（SERIALIZABLE）**：完全串行，性能最低。
- 并发问题：脏读、不可重复读、幻读。

### Q91. MySQL 索引的底层结构？
- InnoDB 索引基于 **B+ 树**：
  - 非叶子节点只存索引键，叶子节点存数据（聚簇）或主键（二级索引）。
  - 叶子节点形成有序双向链表，范围查询高效。
- 聚簇索引（主键）叶子存整行；二级索引叶子存主键值，需"回表"。
- 覆盖索引：查询字段都在索引上，无需回表。

### Q92. 索引失效的常见场景？
1. 不满足最左前缀（联合索引跳过左边列）。
2. 在索引列上做函数/运算：`WHERE YEAR(create_time)`。
3. 隐式类型转换（字符串列传数字）。
4. `LIKE '%xxx'`（前缀模糊）。
5. `OR` 连接非索引列。
6. `!=`、`NOT IN`、`IS NULL`（选择性差时优化器放弃索引）。
- 用 `EXPLAIN` 看 type 是否为 ref/range，key 是否命中。

### Q93. 慢 SQL 如何优化？
1. 开启慢查询日志定位慢 SQL。
2. `EXPLAIN` 查看执行计划：type、key、rows、Extra（Using filesort/temporary 需优化）。
3. 建合适索引，遵循最左前缀与覆盖索引原则。
4. 避免 SELECT *，只查需要的列。
5. 大分页用游标/子查询：`WHERE id > last_id LIMIT 20`。
6. 拆分复杂 SQL，改写 JOIN 为小表驱动大表。
7. 数据量大考虑分库分表。

### Q94. MyBatis 的工作原理？
1. 读取配置（mybatis-config.xml）与 Mapper 映射文件，构建 Configuration。
2. 创建 SqlSessionFactory → SqlSession。
3. SqlSession 通过动态代理（MapperProxy）执行接口方法。
4. 根据方法找到对应 MappedStatement，交 Executor 执行（Simple/Reuse/Batch）。
5. StatementHandler 处理 SQL，ParameterHandler 设参，ResultSetHandler 把结果集映射成对象。
- `#{}` 预编译防注入；`${}` 字符串拼接有注入风险。

### Q95. Redis 常见数据类型与应用场景？
| 类型 | 结构 | 典型场景 |
|------|------|----------|
| String | KV 字符串 | 缓存对象、计数器、分布式锁 |
| Hash | 字段-值 | 对象属性存储、购物车 |
| List | 双向链表 | 消息队列、最新列表 |
| Set | 无序集合 | 去重、标签、共同好友 |
| ZSet | 有序集合 | 排行榜、延迟队列（score 为时间） |
- 高阶：Bitmap（签到）、HyperLogLog（UV 统计）、Stream（消息流）、Geo（附近的人）。

### Q96. 缓存穿透、击穿、雪崩的解决方案？
- **穿透**：查不存在的 key，打到 DB。解决：缓存空值（短 TTL）、布隆过滤器拦截非法 key。
- **击穿**：热点 key 过期瞬间高并发穿透。解决：互斥锁（只让一个请求回源）、热点 key 永不过期（逻辑过期 + 后台刷新）。
- **雪崩**：大量 key 同时过期。解决：过期时间加随机扰动、多级缓存、限流降级、Redis 集群高可用。

---

## 九、微服务与分布式（Q97–Q100）

### Q97. Spring Cloud 的核心组件？
- **注册中心**：Eureka（2.x 停更，可用 Nacos / Consul）。
- **负载均衡**：Ribbon（已弃用，改 LoadBalancer）。
- **服务调用**：Feign / OpenFeign（声明式 HTTP 客户端）。
- **熔断限流**：Hystrix（弃用）→ Resilience4j / Sentinel。
- **网关**：Zuul → Spring Cloud Gateway。
- **配置中心**：Config → Nacos / Apollo。
- **链路追踪**：Sleuth + Zipkin / SkyWalking。
- **分布式事务**：Seata（AT/TCC/SAGA/XA）。

### Q98. 服务雪崩与熔断、降级、限流？
- **雪崩**：下游服务不可用导致上游线程池耗尽，连锁崩溃。
- **熔断**：错误率/RT 超阈值时直接断开调用一段时间（熔断器 Open → Half Open → Closed），类似电路保险丝。
- **降级**：熔断后返回兜底数据（默认值、缓存、友好提示），保证核心链路可用。
- **限流**：控制 QPS，常用算法：计数器、漏桶（匀速）、令牌桶（允许突发）。
- 工具：Sentinel（阿里）功能最全，集成流控/熔断/热点/系统保护。

### Q99. 分布式锁的实现方案对比？
| 方案 | 实现 | 优缺点 |
|------|------|--------|
| 数据库 | `UPDATE ... WHERE locked=0` 或唯一索引 | 简单，性能差，单点 |
| Redis | `SET key value NX PX timeout` + Lua 释放 + Redisson 看门狗续期 | 高性能，但主从切换可能丢锁；RedLock 多节点更稳但复杂 |
| ZooKeeper | 临时顺序节点 + Watch，羊群效应可优化 | 强一致（CP），可靠但写性能不如 Redis |
| etcd | Lease + 事务 CAS | K8s 生态，Raft 一致性 |
- 经验：CP 场景选 ZK/etcd，AP 高并发选 Redis（接受极小概率不一致），均有可重入/续期/阻塞获取需求时用 Redisson。

### Q100. 微服务如何保证数据一致性（分布式事务）？
1. **2PC / XA**：强一致，阻塞、性能差，Seata XA 模式。
2. **TCC**（Try-Confirm-Cancel）：业务侵入，最终一致，需保证幂等与空回滚/悬挂控制。
3. **SAGA**：长事务拆成一系列子事务 + 补偿动作，适合流程长的业务。
4. **本地消息表 / MQ 可靠消息**：本地事务写业务表 + 消息表，MQ 投递给下游消费幂等执行，最终一致。
5. **最大努力通知**：支付回调重试 + 对账兜底。
- 经验：优先**避免跨库事务**（合理拆分聚合与边界），必须分布式时优先选最终一致（MQ + 本地消息表），强一致才上 Seata。

---

## 十、高频深入题（Q101–Q106）

> 以下题目是对前文 Q50/Q91/Q99/Q100 的深度扩展，面试中常作为追问或独立大题出现。

### Q101. synchronized 与 ReentrantLock 的深入区别？

**核心对比表：**

| 对比维度 | synchronized | ReentrantLock |
|---------|--------------|---------------|
| 实现层面 | JVM 关键字（monitorenter/monitorexit 指令） | JDK 类（AQS 实现） |
| 锁释放 | 自动（出代码块/异常自动释放） | 手动，必须在 `finally` 里 `unlock()` |
| 响应中断 | 不响应，线程一直等 | 可响应 `lockInterruptibly()`，避免死锁 |
| 超时获取 | 不支持 | `tryLock(timeout)` 支持 |
| 公平/非公平 | 只有非公平 | 构造参数可选：`new ReentrantLock(true)` 公平 |
| 条件变量 | 一个（wait/notify） | 多个 Condition，可精确唤醒 |
| 性能 | Java 6+ 锁升级优化后差距不大 | 高竞争下略稳定，无锁升级开销 |

**可中断性（实战关键）：**
```java
// synchronized：死锁时只能杀进程
synchronized (lock) { /* ... */ }

// ReentrantLock：等待锁期间可被 interrupt 打断
try {
    lock.lockInterruptibly();
    // 临界区
} catch (InterruptedException e) {
    // 被中断，放弃抢锁
} finally {
    lock.unlock();
}
```

**多 Condition 精确唤醒：**
```java
ReentrantLock lock = new ReentrantLock();
Condition condA = lock.newCondition();
Condition condB = lock.newCondition();
condA.await();        // 线程 A 等待
condA.signal();       // 精确唤醒 A，而非 notifyAll 全部唤醒
```

**底层原理：**
- synchronized 锁升级：无锁 → 偏向锁（单线程）→ 轻量级锁（CAS 自旋）→ 重量级锁（OS 互斥量），只能升级不能降级。Java 15 默认禁用偏向锁（JEP 374）。
- ReentrantLock 基于 AQS：volatile `state` 记录重入次数 + FIFO 双向队列管理等待线程，抢锁用 CAS，公平锁先 `hasQueuedPredecessors()` 判断队列前是否有等待者。

**选型建议：** 能用 synchronized 就别用 ReentrantLock（简单不易错），只有需要中断/超时/公平/多条件时才上 ReentrantLock。读多写少用 `ReentrantReadWriteLock`，读极频繁用 `StampedLock`（乐观读）。

### Q102. 索引下推（ICP）是什么？

**一句话理解：** 把"部分 WHERE 条件的过滤"从 Server 层下推到存储引擎层执行，减少回表次数。

**示例表：**
```sql
CREATE TABLE t_user (
  id BIGINT PRIMARY KEY,
  name VARCHAR(50), age INT, city VARCHAR(50), job VARCHAR(50),
  INDEX idx_name_age (name, age)
);
SELECT * FROM t_user WHERE name LIKE '张%' AND age > 30;
```

**没有 ICP（5.6 之前）：**
1. 引擎层按 name LIKE '张%' 范围扫描，命中 3 条索引记录（张三25/30/35）。
2. 3 条全部回表取整行。
3. Server 层用 age > 30 过滤，丢弃 2 条。
4. **回表 3 次，浪费 2 次。**

**有 ICP（5.6+）：**
1. 引擎层按 name LIKE '张%' 范围扫描，命中 3 条索引记录。
2. **ICP 下推：在索引记录上直接用 age > 30 过滤**（age 值就在索引记录上）。
3. 只有 1 条满足 → 回表 1 次。
4. Server 层直接返回。

**关键点：** 联合索引 `(name, age)` 中，name 做范围查询后 age 无法参与索引定位（最左前缀中断），但 age 的值在索引记录上，引擎层可直接读取判断，无需回表。

**EXPLAIN 验证：** Extra 显示 `Using index condition` 即 ICP 生效；`Using where` 是无下推；`Using index` 是覆盖索引。

**不适用：** 聚簇索引主键查询、覆盖索引、子查询、存储函数条件、全文索引。

### Q103. 红锁（Redlock）是什么？在主从架构下是否生效？

**红锁原理：** Redis 作者 antirez 提出的多节点分布式锁算法。向 N 个**完全独立的 Redis master**（无主从、无 Cluster）同时加锁，多数派（N/2+1）成功才算加锁成功，容忍 N/2 个节点宕机。

**为什么需要红锁：** 单节点 Redis 主从是异步复制，master 加锁后未同步到 slave 就宕机，slave 升级为新 master 后锁丢失 → 两个客户端同时持锁。

**红锁算法（5 节点为例）：**
1. 记录开始时间 t1。
2. 依次向 5 个独立 master 发 `SET key value NX PX timeout`，单节点失败立即继续下一个。
3. 成功数 ≥ 3 且总耗时 < 锁 TTL → 加锁成功；否则失败，向所有节点发解锁。

**★ 红锁在单主多备（主从）场景下不生效：**

红锁的前提是每个节点都是**独立 master，无 slave**。如果每个"节点"本身是主从架构，failover 时 slave 异步复制未完成就升级为 master，锁 key 丢失概率和单节点锁一样。红锁的多数派只能防止"整个节点宕机"，不能防止"节点内部主从切换丢锁"——后者恰恰是红锁要解决的核心问题。

| 场景 | 防节点宕机 | 防主从 failover 丢锁 |
|------|-----------|---------------------|
| 独立 master（无 slave） | ✅ | ✅（无 slave 则无切换） |
| 主从架构 | ✅ | ❌（每对主从内部都有丢锁窗口） |

**Kleppmann 的批评：** ① 依赖时钟同步，时钟漂移/GC STW 仍可能出错；② 仍不是真正的 CP；③ 存在 fencing token 问题（两个客户端先后以为持锁）。

**实战建议：** AP 场景用单节点 Redis + Redisson + 幂等兜底；强一致场景直接上 ZooKeeper/etcd（CP 共识）；红锁工程中用得少（运维 5 个独立 Redis 成本高，且不如 ZK 严谨）。

### Q104. 分布式事务六大方案详解

**问题根因：** 多个独立数据库/服务各有本地事务，没有全局协调者保证"全成功或全回滚"，网络超时/宕机/消息丢失都可能导致数据不一致。

| 方案 | 一致性 | 性能 | 复杂度 | 业务侵入 | 典型场景 |
|------|--------|------|--------|---------|---------|
| 2PC/XA | 强一致 | 低 | 中 | 低 | 同库多源、传统金融 |
| 3PC | 强一致 | 低 | 高 | 低 | 工程极少用 |
| TCC | 最终一致 | 高 | 高 | 高 | 支付、交易核心 |
| SAGA | 最终一致 | 高 | 中 | 中 | 长流程业务链 |
| 本地消息表+MQ | 最终一致 | 高 | 中 | 低 | **互联网通用（90%）** |
| 最大努力通知 | 弱一致 | 高 | 低 | 低 | 支付回调、对账 |

**① 2PC/XA：** 协调者两阶段——prepare（锁资源）→ commit/rollback。强一致但同步阻塞、协调者单点、性能差。Seata XA 模式即此方案。

**② TCC（Try-Confirm-Cancel）：**
- Try：预留资源（冻结库存/冻结金额）
- Confirm：确认提交（扣减冻结的）
- Cancel：回滚（解冻）
- 三个坑：**幂等**（Confirm/Cancel 可能重试，需记录状态）、**空回滚**（Try 未成功但 Cancel 被调用，需检查 Try 是否执行）、**悬挂**（Cancel 先于 Try，需 Cancel 后记录状态，Try 来时发现已 Cancel 则不执行）

**③ SAGA：** 长事务拆成一系列短事务 T1→T2→T3，每个配补偿 C1←C2←C3。失败时反序执行补偿。无隔离性（中间状态可见），适合旅行预订、订单履约链路。

**④ 本地消息表+MQ（最主流）：**
```
订单服务本地事务：INSERT 订单表 + INSERT 消息表（同事务保证同生共死）
    ↓ 后台定时扫描消息表，未发送的推 MQ
库存服务消费：本地事务扣库存 + 更新消息状态为已消费（幂等）
```
业务表和消息表在同一本地事务写入，要么都成功要么都失败，配合 MQ 异步解耦 + 消费方幂等。

**⑤ 最大努力通知：** 支付成功后发消息，按递增间隔重试（1m/5m/10m），仍失败则人工对账兜底。一致性最弱但实现简单。

**实战选型路径：** 优先避免跨库（合理设计聚合边界）→ 必须跨库用本地消息表+MQ → 核心资金链路用 TCC → 长流程用 SAGA → 跨系统通知用最大努力通知 → **所有方案都要幂等 + 对账兜底**。

### Q105. 幂等性如何设计？

幂等是分布式事务的基石——无论哪种方案，消费方都必须幂等。

**幂等模板：**
```java
// 方式一：唯一索引 + 状态机
本地事务 {
    INSERT 幂等表(请求ID, 状态) // 唯一索引冲突则已处理
    处理业务;
    更新状态;
}

// 方式二：Redis 标记 + DB 兜底
if (redis.setIfAbsent(reqId, "1", 30s)) {  // 防并发
    if (db.已处理(reqId)) return;           // 防重放
    本地事务 { 处理业务; 记录已处理; }
} else {
    return; // 正在处理或已处理
}
```

**常见幂等手段：**
1. **唯一索引：** DB 层兜底，重复插入报错走异常处理。
2. **状态机：** 业务状态只能单向流转（待支付→已支付→已发货），`UPDATE ... WHERE status='待支付'` 天然幂等。
3. **Token 机制：** 前端获取一次性 token，提交后失效（防重复提交）。
4. **乐观锁：** `UPDATE ... WHERE version=?`，版本不符则放弃。
5. **Redis 去重：** setnx 标记请求 ID，设置过期时间。

**对账兜底：**
```sql
-- 定时对账：找差集即异常
SELECT 订单号 FROM 订单表 WHERE 状态='已支付'
MINUS
SELECT 订单号 FROM 库存表 WHERE 状态='已扣减';
-- 差集 → 告警 + 自动/人工补偿
```

### Q106. Seata AT 模式的原理？

Seata 提供四种模式：AT（默认）、TCC、SAGA、XA。AT 是最常用、业务零侵入的方案。

**核心组件：**
- **TC（Transaction Coordinator）**：独立部署的 Server，维护全局事务状态。
- **TM（Transaction Manager）**：发起全局事务/提交或回滚决策（标注 `@GlobalTransactional` 的方法）。
- **RM（Resource Manager）**：各服务管理本地分支事务。

**AT 执行流程：**
```
1. TM 开启全局事务 → TC 生成 XID
2. XID 通过 RPC 传播到各分支服务
3. RM 解析 SQL，生成 undo log（前镜像/后镜像），与业务 SQL 在同一本地事务提交
4. 各分支本地事务已提交（非锁定），向 TC 注册分支
5. TM 决策：
   - 全局提交 → TC 异步通知各 RM 删除 undo log（已提交无需操作）
   - 全局回滚 → TC 通知各 RM 用 undo log 反向恢复数据
6. 恢复后校验后镜像，若已被修改则告警（需人工介入）
```

**关键设计：**
- **一阶段即提交本地事务**，不长时间锁定资源（与 XA 的区别）。
- **undo log 保证可回滚**，全局回滚时用前镜像反向恢复。
- **全局锁（可选）**：写隔离场景，分支提交前向 TC 注册全局锁，防脏写。
- **读隔离**：`@GlobalTransactional` + `SELECT ... FOR UPDATE` 读全局锁。

**与 XA 对比：** XA 在 prepare 阶段锁定资源直到 commit，阻塞时间长；AT 一阶段就提交本地事务，性能高，但写隔离依赖全局锁。

### Q107. 限流算法与实现详解

**四种主流限流算法：**

**① 计数器（固定窗口）**
- 每个时间窗口内计数，超过 limit 则拒绝，窗口切换时计数清零。
- 优点：实现最简单。缺点：**临界突刺**——窗口边界处 0.9s 来 1000 + 1.0s 来 1000 = 0.1s 内 2000 请求。
- Redis + Lua 实现：GET/INCR + EXPIRE。

**② 滑动窗口**
- 把 1s 划分为 N 个小窗口，统计过去 1s 内所有小窗口的总量。
- 优点：解决临界突刺。缺点：实现复杂，内存占用略大。
- Redis + Lua 实现：Sorted Set（score=时间戳），ZREMRANGEBYSCORE 删除过期 + ZCARD 统计。

**③ 令牌桶（最常用）**
- 按速率填充令牌（rate/s），桶有容量上限（capacity），请求消费令牌，没令牌则限流。
- **关键特性：允许突发**——桶积攒满令牌后，瞬间来大流量可一次性消费多个令牌全部放行。
- 优点：允许突发流量，大多数场景适用（Guava RateLimiter / Go x/time/rate 都是令牌桶）。
- Redis + Lua 实现：HMSET 存 tokens + time，按时间差计算补充量，min(capacity, tokens+delta)。

**④ 漏桶**
- 请求入队列排队，按固定速率消费（匀速出水），桶满则拒绝新请求。
- **关键特性：不允许突发**——不管来多少请求，后端始终匀速收到。
- 优点：流量绝对平滑，保护后端。缺点：不允许突发，队列增加延迟。
- Go channel 模拟：ticker 匀速消费 channel。

**四种算法对比：**

| 算法 | 突发流量 | 实现复杂度 | 适用场景 |
|------|---------|-----------|---------|
| 计数器 | ❌ 临界突刺 | ⭐ 最简单 | 简单限流 |
| 滑动窗口 | 中等（平滑） | ⭐⭐⭐ | 精确 QPS 控制 |
| 令牌桶 | ✅ 允许突发 | ⭐⭐ | **大多数场景（最常用）** |
| 漏桶 | ❌ 匀速不突发 | ⭐⭐ | 严格匀速（MQ 消费、API 网关） |

**单机 vs 分布式：**

| 维度 | 单机限流 | 分布式限流 |
|------|---------|-----------|
| 实现 | Guava RateLimiter / Go x/time/rate | Redis + Lua 原子操作 |
| 优点 | 无网络开销，快 | 全局精确，多节点共享 |
| 缺点 | 各机各限，总量不精确 | Redis 网络开销，故障影响 |
| 降级 | — | Redis 挂了降级到单机限流 |

**令牌桶 vs 固定窗口的核心区别：**

| 维度 | 固定窗口 | 令牌桶 |
|------|---------|--------|
| 限流思路 | 数请求（来了几个） | 数令牌（还剩几个可用） |
| 突发流量 | ❌ 临界突刺（窗口边界翻倍） | ✅ 允许突发（积攒令牌） |
| 时间敏感 | 窗口切换清零 | 持续填充，与窗口无关 |
| 行为 | 0.9s 1000 + 1.0s 1000 = 0.1s 内 2000（挡不住） | 突发可处理（积攒令牌），耗尽后限流等补充 |
| 适用 | 简单限流（临界突刺可容忍） | 大多数业务（Guava RateLimiter 就是令牌桶） |

**速记：** 计数器（简单有突刺）< 滑动窗口（平滑精确复杂）< 令牌桶（允许突发，最常用）< 漏桶（匀速不突发）。单机 Guava/Go rate，分布式 Redis+Lua，Redis 挂了降级单机。令牌桶 = 数令牌（允许突发），固定窗口 = 数请求（临界突刺）。

---

## 十一、计算机网络（Q108–Q117）

### Q108. TCP 三次握手过程？为什么是三次不是两次？

```
客户端                     服务端
  │                          │
  │── SYN, seq=x ──────────→│  第一次：客户端发 SYN，进入 SYN_SENT
  │                          │
  │←── SYN+ACK, seq=y, ack=x+1 ──│  第二次：服务端发 SYN+ACK，进入 SYN_RCVD
  │                          │
  │── ACK, ack=y+1 ────────→│  第三次：客户端发 ACK，双方 ESTABLISHED
```

**为什么不是两次？**
- 两次握手时，如果客户端发的第一个 SYN 延迟到达，服务端回复 SYN+ACK 后就进入 ESTABLISHED，但客户端不认这个连接 → 服务端白等，浪费资源。
- 三次握手让双方都确认"对方收到了我的确认"，防止历史无效连接。
- 两次无法确认服务端→客户端方向通信正常（只有客户端确认了服务端，服务端没确认客户端）。

### Q109. TCP 四次挥手过程？为什么需要 TIME_WAIT？

```
客户端                      服务端
  │                           │
  │── FIN, seq=u ───────────→│  第一次：客户端发 FIN，进入 FIN_WAIT_1
  │                           │
  │←── ACK, ack=u+1 ─────────│  第二次：服务端发 ACK，进入 CLOSE_WAIT
  │                           │  （服务端处理剩余数据）
  │←── FIN, seq=v ───────────│  第三次：服务端发 FIN，进入 LAST_ACK
  │                           │
  │── ACK, ack=v+1 ─────────→│  第四次：客户端发 ACK，进入 TIME_WAIT
  │                           │  → 等 2MSL 后 CLOSED
```

**为什么需要 TIME_WAIT（等 2MSL）？**
1. **确保最后一个 ACK 到达**：如果客户端的 ACK 丢失，服务端会重发 FIN，客户端还能响应。等 2MSL 保证服务端收到 ACK。
2. **防止旧连接的报文干扰**：让本次连接的所有报文在网络中消亡（MSL = 报文最大生存时间），防止被下一个相同端口的连接收到。

### Q110. TCP 和 UDP 的区别？

| 维度 | TCP | UDP |
|------|-----|-----|
| 连接 | 面向连接（三次握手） | 无连接 |
| 可靠性 | 可靠（确认、重传、排序） | 不可靠（尽最大努力） |
| 速度 | 慢（握手 + 确认开销） | 快（无握手无确认） |
| 顺序 | 有序 | 无序 |
| 头部 | 20 字节 | 8 字节 |
| 流控 | 滑动窗口 + 拥塞控制 | 无 |
| 适用 | HTTP/SSH/文件传输 | DNS/视频直播/游戏 |

### Q111. TCP 如何保证可靠传输？

1. **校验和**：检测数据完整性。
2. **序列号 + 确认应答**：保证有序、不丢失。
3. **超时重传**：发送后等 ACK，超时未收到则重传。
4. **滑动窗口**：流量控制，接收方告诉发送方自己能接收多少。
5. **拥塞控制**：慢启动→拥塞避免→快重传→快恢复，防止网络拥塞。
6. **连接管理**：三次握手 + 四次挥手。

### Q112. HTTP 和 HTTPS 的区别？HTTPS 的握手过程？

| 维度 | HTTP | HTTPS |
|------|------|-------|
| 协议 | 应用层 | HTTP + TLS/SSL |
| 端口 | 80 | 443 |
| 加密 | 明文传输 | TLS 加密 |
| 证书 | 无 | 需要 CA 证书 |
| 性能 | 快 | TLS 握手多 1-2 个 RTT |

**HTTPS 握手简化版：**
```
① 客户端发 Client Hello（支持的 TLS 版本、加密套件、随机数 1）
② 服务端回 Server Hello（选定加密套件、随机数 2、证书）
③ 客户端验证证书 → 生成预主密钥 → 用服务端公钥加密发送
④ 双方用随机数 1 + 随机数 2 + 预主密钥算出会话密钥
⑤ 后续用对称加密（会话密钥）通信
```

### Q113. HTTP 1.0 / 1.1 / 2.0 / 3.0 的区别？

| 版本 | 特性 |
|------|------|
| HTTP 1.0 | 每次请求建新连接（短连接） |
| HTTP 1.1 | 长连接（Keep-Alive）、管道化、Host 头、缓存控制 |
| HTTP 2.0 | 多路复用（一个连接并发多请求）、头部压缩（HPACK）、服务端推送、二进制分帧 |
| HTTP 3.0 | 基于 QUIC（UDP）、队头阻塞解决、0-RTT 连接迁移 |

### Q114. 浏览器输入 URL 到页面渲染的完整流程？

```
① DNS 解析：域名 → IP（本地缓存→本地 DNS→根 DNS→顶级 DNS→权威 DNS）
② TCP 三次握手：建立连接
③ TLS 握手（如果是 HTTPS）
④ 发送 HTTP 请求：GET / HTTP/1.1
⑤ 服务器处理请求 → 返回 HTTP 响应（HTML）
⑥ 浏览器解析 HTML → 构建 DOM 树
⑦ 解析 CSS → 构建 CSSOM 树
⑧ DOM + CSSOM → 渲染树（Render Tree）
⑨ 布局（Layout）→ 计算位置
⑩ 绘制（Paint）→ 像素到屏幕
⑪ 合成（Composite）→ GPU 合成图层
```

### Q115. TCP 粘包怎么解决？

**粘包原因**：TCP 是流式协议，没有消息边界，多个包连续发送时接收方无法区分。

**解决方案：**
1. **固定长度**：每个消息固定 N 字节，不足补齐。
2. **特殊分隔符**：用 `\n` 或 `\r\n` 分割消息。
3. **长度前缀**（最常用）：消息头中放消息体长度，接收方按长度读取。
```java
// 长度前缀协议
| 4 字节长度 | 消息体 |
```

### Q116. 跨域是什么？CORS 怎么解决？

**跨域**：浏览器的同源策略——协议+域名+端口三者必须一致，不一致就是跨域。

**CORS（跨域资源共享）**：服务端设置响应头允许跨域。
```
Access-Control-Allow-Origin: https://example.com
Access-Control-Allow-Methods: GET, POST, PUT, DELETE
Access-Control-Allow-Headers: Content-Type, Authorization
```

**预检请求（OPTIONS）**：非简单请求（如 PUT/DELETE 或带自定义头）浏览器先发 OPTIONS 预检，服务端返回允许的方法和头后，再发真实请求。

### Q117. 常见 HTTP 状态码？

| 状态码 | 含义 | 场景 |
|--------|------|------|
| 200 | 成功 | 正常返回 |
| 301 | 永久重定向 | 域名迁移 |
| 302 | 临时重定向 | 登录跳转 |
| 304 | 协商缓存命中 | ETag/Last-Modified 未变 |
| 400 | 请求语法错误 | 参数格式错 |
| 401 | 未认证 | 未登录 |
| 403 | 禁止访问 | 无权限 |
| 404 | 资源不存在 | URL 错 |
| 500 | 服务器内部错误 | 后端异常 |
| 502 | 网关错误 | 上游服务挂了 |
| 503 | 服务不可用 | 维护/过载 |
| 504 | 网关超时 | 上游响应超时 |

---

## 补充速记口诀

| 主题 | 速记 |
|------|------|
| 集合 | List 有序可重复（Array 随机快、Link 增删快）；Set 去重（Hash 无序、Tree 有序、Link 保序）；Map 键值对（Hash 无序、Tree 按 key、Link 保序、Concurrent 并发安全） |
| 并发 | 线程池七参：核心、最大、存活、单位、队列、工厂、策略；JMM 三大特性：可见性、有序性、原子性 |
| JVM | 内存五区：计栈本堆方；GC 判活：可达性分析，根为栈/常量/静态/Native；算法：标清、复制、标整、分代 |
| Spring | IoC 管对象、DI 注依赖、AOP 切面；Bean 生命周期五步：实例化→赋值→Aware→初始化前后→使用→销毁 |
| 事务 | ACID：原子、一致、隔离、持久；四级别：读未提交→读已提交→可重复读→串行；传播七种，默认 REQUIRED |
| Redis | 五类型：String/Hash/List/Set/ZSet；三问题：穿透（空值+布隆）、击穿（互斥锁/永不过期）、雪崩（随机 TTL+多级缓存） |
| 分布式锁 | 单节点 Redisson（AP）/ ZK+etcd（CP）/ 红锁（5 独立 master 多数派，主从下不生效） |
| 分布式事务 | 六方案：2PC（强一致阻塞）→TCC（三态+幂等/空回滚/悬挂）→SAGA（补偿）→本地消息表+MQ（最主流）→最大努力通知；选型：避免跨库>消息表>TCC>SAGA>2PC，都要幂等+对账 |
| 锁深入 | synchronized：JVM 锁升级（偏向→轻量→重量），自动释放；ReentrantLock：AQS+state，可中断/超时/公平/多 Condition |
| MySQL 优化 | ICP：WHERE 条件下推到引擎层，索引记录上先过滤再回表，看 Extra=Using index condition |
| 限流 | 计数器（简单有突刺）< 滑动窗口（平滑精确）< 令牌桶（允许突发，最常用）< 漏桶（匀速不突发）；单机 Guava/Go rate，分布式 Redis+Lua；令牌桶=数令牌（允许突发），固定窗口=数请求（临界突刺） |
| 网络 | 三次握手（防历史连接）四次挥手（TIME_WAIT 等 2MSL 防 ACK 丢失+旧报文）；TCP 可靠=校验+序列+重传+滑动窗口+拥塞控制；HTTP 1.1 长连接/2.0 多路复用/3.0 QUIC；粘包=长度前缀；跨域=CORS 响应头 |

---

> 文档共 117 题，覆盖 Java 基础、OOP、集合、并发、JVM、异常 IO、Spring、数据库缓存、微服务、高频深入题、计算机网络十一大主题。建议结合项目经历补充自己的实战案例，面试中以"原理 + 场景 + 踩坑"三段式回答效果最佳。

