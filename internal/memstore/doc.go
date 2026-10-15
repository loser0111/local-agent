// Package memstore 是记忆库本体（MemoryStore）的引擎域。
//
// 从顶层 wails-tmp/memory 迁入，与 internal/memory（记忆工具层，提供
// memory_search / memory_save / memory_forget 三个内置工具）区分：
//   - 本包只负责持久化与检索（读盘/落盘、索引、条目模型），
//     不认识 App，也不认识工具层。
//   - internal/memory 依赖本包，反向依赖不允许出现。
//
// 迁入前的顶层包名是 memory，与 internal/memory 同名，靠 memstore 别名共存；
// 归位后顶层包名统一为 memstore，别名随之取消。
package memstore
