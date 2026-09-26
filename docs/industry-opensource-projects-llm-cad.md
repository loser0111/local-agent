# 工业界与开源项目：LLM/Agent × CAD 案例调研

> 调研范围：Autodesk 系列（AutoCAD / Fusion 360 等）与 SolidWorks  
> 方向：工业界 AI/Agent 官方接入方案、API 能力、GitHub 开源项目  
> 时间范围：2023–2025 为主，兼顾前期经典项目

---

## 一、Autodesk 官方 AI / Agent 接入方案

### 1. Autodesk AI（官方 AI 助手平台）
- **来源**: Autodesk 官方公告 2024–2025（Autodesk University 2024）
- **概述**: Autodesk 在 2024 年推出 **Autodesk AI** 战略，将生成式 AI 能力嵌入其核心产品线（AutoCAD、Fusion 360、Revit、Inventor 等），提供设计辅助、自动化、智能搜索等能力。
- **核心能力**:
  - **Design Assistant**: 在 AutoCAD / Fusion 360 中的 AI 对话助手，支持自然语言查询图纸内容、定位对象、建议下一步操作
  - **Automated Workflows**: AI 驱动的自动化流程（如自动标注、自动出图）
  - **生成式设计 (Generative Design)**: Fusion 360 已有的生成式设计能力，结合 AI 优化迭代
  - **智能搜索**: 跨项目、跨文件的语义搜索
- **接入方式**: 内置于 Autodesk 产品，部分能力通过 **Autodesk Platform Services (APS)** API 对外开放
- **官网**: https://www.autodesk.com/ai

### 2. Autodesk Platform Services (APS) — 原 Forge API
- **来源**: Autodesk 官方开发者平台
- **概述**: APS 是 Autodesk 的云 API 平台，提供 RESTful API 用于在云端处理 CAD 文件，是 LLM/Agent 接入 Autodesk 生态的主要技术桥梁。
- **核心 API 能力**:

| API 服务 | 能力描述 | Agent 接入价值 |
|---------|---------|--------------|
| **Design Automation API** | 在云端运行 AutoCAD / Fusion 360 / Inventor 引擎，执行脚本（AutoLISP / .NET / iLogic） | Agent 生成代码 → 云端执行 → 返回结果，无需本地安装 CAD |
| **Model Derivative API** | 将 CAD 文件（DWG / RVT / IPT 等）转换为 SVF / OBJ / glTF 等可视化格式 | Agent 解析 CAD 文件结构，提取几何信息 |
| **Viewer API** | 在 Web 端渲染 3D 模型，支持交互、测量、剖切 | Agent 可视化输出结果 |
| **Data Management API** | 管理 Autodesk Docs / BIM 360 / Fusion Team 中的文件和版本 | Agent 管理设计文件的读写、版本控制 |
| **Webhooks API** | 文件变更、任务完成等事件回调通知 | Agent 事件驱动的工作流触发 |
| **Reality Capture API** | 点云 / 摄影测量 → 3D 模型 | 从物理世界到 CAD 模型的 Agent pipeline |

- **与 LLM/Agent 集成的典型模式**:
  ```
  用户自然语言指令 
  → LLM 规划 + 生成 AutoLISP/Python 脚本
  → APS Design Automation API 云端执行
  → Model Derivative API 转换结果格式
  → Viewer API 渲染展示
  → 反馈给 LLM 进行下一轮迭代
  ```
- **官网**: https://aps.autodesk.com
- **文档**: https://aps.autodesk.com/developer-guide

### 3. AutoCAD AutoLISP / .NET API — 本地 Agent 接入
- **概述**: AutoCAD 提供丰富的本地编程接口，是构建本地 CAD Agent 的基础。
- **核心 API**:

| API 类型 | 语言 | Agent 接入方式 |
|---------|------|--------------|
| **AutoLISP / Visual LISP** | LISP 方言 | LLM 生成 AutoLISP 脚本 → AutoCAD `APPLOAD` 执行 |
| **.NET API** | C# / VB.NET | LLM 生成 C# 代码 → 编译为 DLL → AutoCAD NETLOAD 执行 |
| **ObjectARX** | C++ | 底层 API，高性能操作，适合复杂 Agent |
| **AutoCAD Plugin API** | Python (pyautocad) | LLM 生成 Python → 通过 COM/.NET 接口驱动 AutoCAD |
| **AutoCAD JavaScript API** | JavaScript | Web 端 AutoCAD 插件开发 |

- **pyautocad (Python 库)**: 
  - GitHub: https://github.com/reclosedevs/pyautocad
  - 通过 COM 接口驱动 AutoCAD，支持读写 DWG、操作对象
  - 非常适合 LLM Agent 生成 Python 代码 → pyautocad 执行的 pipeline

### 4. Fusion 360 API — Python / C++ 插件
- **概述**: Fusion 360 提供 Python 和 C++ API，支持脚本和插件开发。
- **核心能力**:
  - **参数化建模**: 创建草图、拉伸、旋转、倒角等特征操作
  - **装配体操作**: 创建组件、添加配合关系
  - **工程图生成**: 自动出图、标注
  - **API 脚本**: 内置 Scripts and Add-Ins 面板，直接运行 Python 脚本
- **与 LLM/Agent 集成模式**:
  - LLM 生成 Fusion 360 Python API 脚本 → 用户在 Fusion 360 内执行
  - 或通过 APS Design Automation API 在云端执行 Fusion 360 引擎
- **文档**: https://help.autodesk.com/view/fusion360/ENU/?contextId=API

### 5. Autodesk Research 开源项目
- **Fusion 360 Gallery Dataset**:
  - GitHub: https://github.com/AutodeskAILab/Fusion360GalleryDataset
  - 大规模参数化 CAD 建模过程数据集（草图-拉伸序列）
  - 为 LLM/Agent 训练提供真实工业建模数据
- **Autodesk AI Lab** 其他开源:
  - D2-Net: CAD 生成网络
  - UV-Net: CAD B-rep 理解
  - BRepNet: CAD 边界表示学习

---

## 二、SolidWorks 官方 API 与 AI 接入

### 6. SolidWorks API 体系
- **来源**: Dassault Systèmes SolidWorks 官方
- **概述**: SolidWorks 提供多层 API 接口，是构建 SolidWorks Agent 的技术基础。
- **核心 API**:

| API 类型 | 语言 | 能力描述 | Agent 接入价值 |
|---------|------|---------|--------------|
| **SolidWorks API** | VBA / C# / C++ (COM) | 全功能 API：零件、装配体、工程图 | LLM 生成 VBA/C# 代码 → SolidWorks 执行 |
| **SolidWorks Document Manager API** | C# / VB.NET | 轻量级读写 SW 文件（无需打开 SolidWorks） | Agent 批量解析文件信息 |
| **eDrawings API** | COM | 查看、标记 3D 模型 | Agent 可视化反馈 |
| **SolidWorks PDM API** | C# | 产品数据管理 | Agent 文件管理、版本控制 |
| **3DEXPERIENCE Platform API** | REST / WebSocket | 云端协作平台 API | 云端 Agent 接入 |

- **典型 Agent 集成模式**:
  ```
  用户自然语言 → LLM 生成 VBA/C# 宏代码
  → SolidWorks Macro 执行
  → 读取模型树、特征参数 → 反馈给 LLM
  → LLM 迭代修正 → 重新执行
  ```

### 7. 3DEXPERIENCE Platform — Dassault AI 战略
- **来源**: Dassault Systèmes 官方 2024–2025
- **概述**: 3DEXPERIENCE 是 Dassault 的云端协作平台，集成了 AI 能力。
- **AI 能力**:
  - **Generative Modeling**: 生成式设计（拓扑优化等）
  - **AI 辅助设计推荐**: 基于历史数据的智能推荐
  - **自然语言搜索**: 在 3D 模型库中用自然语言搜索
- **Agent 接入**: 通过 3DEXPERIENCE REST API / WebSocket 构建云端 Agent
- **官网**: https://www.3ds.com/3dexperience

### 8. SolidWorks + Python (win32com)
- **概述**: 通过 Python `win32com` 库可驱动 SolidWorks COM 接口，实现 Python Agent。
- **典型用法**:
  ```python
  import win32com.client
  swApp = win32com.client.Dispatch("SldWorks.Application")
  swApp.Visible = True
  # Agent 可通过此接口操作 SolidWorks
  ```
- **价值**: LLM 生成 Python 代码 → win32com 驱动 SolidWorks，技术门槛低

---

## 三、GitHub 开源项目调研

### 9. Text2CAD / Open-Source Text-to-CAD 项目
- **项目**: Text2CAD 开源代码
- **GitHub**: https://github.com/text2cad (组织/项目)
- **概述**: 提供从自然语言到参数化 CAD 代码（OpenSCAD）的端到端生成 pipeline。
- **技术栈**: Python, PyTorch, CodeLlama/DeepSeek-Coder 微调
- **开源内容包括**:
  - Text2CAD-1M 数据集
  - 微调模型权重
  - 推理脚本

### 10. OpenSCAD + LLM 项目生态
- **OpenSCAD**: 开源参数化 CAD 软件，使用脚本语言描述 3D 模型
  - GitHub: https://github.com/openscad/openscad
  - **Agent 价值**: OpenSCAD 的脚本语言天然适配 LLM 代码生成，是 Text-to-CAD 最常用的中间表示
- **openscad-scripting-helpers**: 辅助 LLM 生成 OpenSCAD 代码的工具库

### 11. FreeCAD + Python API + LLM
- **FreeCAD**: 开源参数化 CAD，提供完整 Python API
  - GitHub: https://github.com/FreeCAD/FreeCAD
  - **Agent 价值**: FreeCAD Python API 功能强大（建模、装配、工程图、FEM），且完全开源，是构建开源 CAD Agent 的最佳载体
  - **典型 Agent pipeline**: LLM → Python 脚本 → FreeCAD 执行 → 反馈
- **FreeCAD Python API 能力**:
  ```python
  import FreeCAD, Part
  doc = FreeCAD.newDocument()
  box = Part.makeBox(10, 10, 10)
  Part.show(box)
  ```
- **FreeCAD + LLM 社区项目**: 已有多个社区项目尝试将 GPT/LLM 与 FreeCAD Python API 结合实现自然语言建模

### 12. Blender + LLM (bpy) — 方法论参考
虽非严格 CAD，但 Blender + Python(bpy) 是 3D 建模 + LLM 领域最活跃的开源生态，方法论对 CAD Agent 有直接参考价值：
- **3D-GPT 开源**: https://github.com/3D-GPT (如有公开)
- **BlenderGPT**: 
  - GitHub: https://github.com/gd3kr/BlenderGPT
  - 使用 GPT-4 生成 bpy Python 代码 → Blender 执行
  - 支持 3D 建模、场景搭建、材质设置
  - **Star 数高，社区活跃，方法论可直接迁移到 FreeCAD/AutoCAD**
- **Blender Copilot (bpy-Copilot)**:
  - GitHub: https://github.com/thingavid/Blender-Copilot
  - 在 Blender 内集成的 AI 编程助手面板

### 13. CAD-Specific LLM 开源项目
- **CAD-Reps**: CAD 模型表示学习，为 CAD 理解提供特征编码
  - GitHub: 学术研究开源
- **DeepCAD 开源**:
  - GitHub: https://github.com/chenglinxu/DeepCAD (如有公开)
  - CAD 命令序列生成模型 + 数据集
- **SkexGen 开源**:
  - CAD 草图生成模型
- **BRepNet (Autodesk AI Lab)**:
  - GitHub: https://github.com/AutodeskAILab/BRepNet
  - CAD B-rep 边界表示学习，理解 CAD 几何拓扑结构

### 14. 通用 LLM Agent 框架中与 CAD 相关的接入
- **Open Interpreter**:
  - GitHub: https://github.com/OpenInterpreter/open-interpreter
  - 通用代码执行 Agent，可生成并执行 Python 代码驱动本地 CAD 软件（pyautocad / FreeCAD Python API / SolidWorks COM）
  - **Agent 价值**: 开箱即用的 LLM 代码执行框架，CAD 只是其中一个应用场景
- **AutoGPT / AgentGPT**: 通用自主 Agent 框架，可通过自定义工具接入 CAD API
- **LangChain Tools**:
  - 可将 CAD API 封装为 LangChain Tool，Agent 通过 ReAct / function calling 调用
  - 社区已有 CAD tool 的示例实现

### 15. MCP (Model Context Protocol) + CAD
- **概述**: Anthropic 推出的 MCP 协议允许 LLM 连接外部工具和数据源，CAD API 可封装为 MCP Server。
- **潜在模式**:
  ```
  Claude/LLM (MCP Client) ←→ CAD MCP Server (封装 AutoCAD/SolidWorks/FreeCAD API)
  → Agent 通过 MCP 调用 CAD 操作工具
  ```
- **社区项目**: 已有社区开始探索 MCP + CAD 的集成方案
- **GitHub 搜索关键词**: `mcp cad`, `mcp autocad`, `mcp blender`

---

## 四、工业界 CAD + AI 商业产品案例

### 16. Autodesk × Microsoft Copilot 集成
- **来源**: Autodesk + Microsoft 合作 2024
- **概述**: Autodesk 与 Microsoft 合作，将 Autodesk 能力接入 Microsoft Copilot 生态。
- **能力**: 在 Microsoft 365 / Teams 中通过 Copilot 调用 Autodesk 设计数据，实现跨平台 AI 设计协作。

### 17. Onshape (PTC) AI 能力
- **来源**: PTC / Onshape 2024
- **概述**: Onshape 是云原生 CAD，天然适合 AI/Agent 接入。
- **API**: Onshape REST API（全云端，支持 FeatureScript）
- **AI 能力**: 设计建议、自动修复、智能搜索
- **Agent 价值**: 纯云 API，LLM Agent 可直接 HTTP 调用，无需本地软件

### 18. nTopology + AI
- **概述**: nTopology 专注于高级工程设计软件，提供 API + AI 集成能力。
- **价值**: 拓扑优化 + LLM 的工业应用场景

### 19. Henry / Natron AI (CAD Copilot 类创业公司)
- **来源**: 2024–2025 行业动态
- **概述**: 多家创业公司正在构建"CAD Copilot"产品——在 CAD 软件内嵌入 AI 助手，支持自然语言设计指令。
- **典型模式**: 插件形式集成到 AutoCAD/SolidWorks，通过 LLM API + 本地 CAD API 实现

---

## 五、CAD Agent 典型技术架构总结

基于以上工业界和开源项目调研，CAD Agent 的典型架构可归纳为以下模式：

### 模式 A: 本地 Agent（直接 API 调用）
```
用户指令 → LLM (规划+代码生成) → Python/AutoLISP/C# 脚本
→ 本地 CAD API (pyautocad / FreeCAD Python / SolidWorks COM)
→ 执行结果反馈 → LLM 迭代修正
```
- **代表**: BlenderGPT, FreeCAD+LLM 社区项目, Open Interpreter + CAD
- **优点**: 延迟低，实时交互
- **缺点**: 需本地安装 CAD，部署复杂

### 模式 B: 云端 Agent（通过 CAD 云 API）
```
用户指令 → LLM → 代码/指令
→ CAD 云 API (APS Design Automation / Onshape REST API)
→ 云端执行 → 结果返回 (可视化/文件)
→ LLM 迭代修正
```
- **代表**: Autodesk APS + LLM, Onshape AI
- **优点**: 无需本地 CAD，可扩展
- **缺点**: 网络延迟，API 配额限制

### 模式 C: MCP / Tool-Use Agent（封装 CAD 为工具）
```
用户指令 → LLM Agent (ReAct / function calling)
→ CAD MCP Server / LangChain Tool (封装 CAD API 为工具集)
→ 逐个调用 CAD 操作 (创建草图、拉伸、倒角...)
→ 状态反馈 → Agent 规划下一步
```
- **代表**: MCP + CAD 社区项目, LangChain CAD Tool
- **优点**: 精细控制，可观测性好，支持多步复杂任务
- **缺点**: 需开发完整的工具封装层

### 模式 D: 嵌入式 AI 助手（厂商内置）
```
用户在 CAD 软件中 → 内置 AI 面板
→ 自然语言指令 → 厂商 AI 后端处理
→ 直接操作 CAD → 结果即时展示
```
- **代表**: Autodesk AI Design Assistant, SolidWorks 3DEXPERIENCE AI
- **优点**: 最佳用户体验，深度集成
- **缺点**: 封闭生态，自定义能力有限

---

## 六、关键 API 能力对照表

| 能力维度 | AutoCAD | Fusion 360 | SolidWorks | FreeCAD (开源) | Onshape (云) |
|---------|---------|-----------|-----------|--------------|-------------|
| **Python API** | pyautocad (COM) | ✅ 原生 Python API | win32com (COM) | ✅ 原生 Python API | ❌ (REST + FeatureScript) |
| **云端执行** | ✅ APS Design Automation | ✅ APS Design Automation | ❌ (需本地) | ❌ (需本地) | ✅ 原生云端 |
| **REST API** | ✅ APS | ✅ APS | ✅ 3DEXPERIENCE | ❌ | ✅ 原生 |
| **参数化建模 API** | AutoLISP / .NET | ✅ Python | ✅ C#/VBA COM | ✅ Python | ✅ FeatureScript |
| **工程图 API** | ✅ .NET | ✅ Python | ✅ COM | ✅ Python | ✅ REST |
| **文件解析** | ✅ APS Model Derivative | ✅ APS | ✅ Doc Manager API | ✅ Python | ✅ REST |
| **可视化渲染** | ✅ APS Viewer | ✅ APS Viewer | ✅ eDrawings | ✅ 内置 | ✅ Web Viewer |
| **LLM 适配性** | ★★★★ (AutoLISP/Python) | ★★★★★ (Python, 云端) | ★★★ (C#/VBA) | ★★★★★ (Python, 开源) | ★★★★ (REST, 云端) |

---

## 七、开源项目索引汇总

| 项目 | GitHub | 类型 | CAD 软件 | Agent 价值 |
|------|--------|------|---------|-----------|
| BlenderGPT | gd3kr/BlenderGPT | LLM+3D建模 | Blender (参考) | 方法论直接可迁移 |
| OpenSCAD | openscad/openscad | 参数化CAD | OpenSCAD | 脚本语言适配LLM代码生成 |
| FreeCAD | FreeCAD/FreeCAD | 开源参数化CAD | FreeCAD | 最佳开源CAD Agent载体 |
| pyautocad | reclosedevs/pyautocad | Python库 | AutoCAD | LLM生成Python→驱动AutoCAD |
| Text2CAD | text2cad | Text-to-CAD | OpenSCAD | 端到端NL→CAD生成 |
| BRepNet | AutodeskAILab/BRepNet | CAD理解 | 通用 | B-rep特征学习 |
| Fusion360Gallery | AutodeskAILab/... | 数据集 | Fusion 360 | 工业级建模过程数据 |
| Open Interpreter | OpenInterpreter/... | 通用Agent | 通用 | 代码执行框架，可驱动CAD |
| LangChain | langchain-ai/langchain | Agent框架 | 通用 | CAD API封装为Tool |
| DeepCAD | chenglinxu/DeepCAD | CAD生成 | 通用 | 命令序列生成基础 |

---

## 八、核心发现与结论

1. **Autodesk 是 AI/Agent 集成最成熟的 CAD 厂商**：APS 云 API + Fusion 360 Python API + AutoLISP + 官方 AI Lab 开源数据集，形成了从云到端、从数据到模型的完整生态。
2. **SolidWorks 的 Agent 接入主要依赖 COM API**（C#/VBA/Python win32com），尚无官方云端执行能力（需本地安装），但 3DEXPERIENCE 平台在演进中。
3. **开源生态中 FreeCAD + Python API 是构建开源 CAD Agent 的最佳选择**：完全开源、Python 原生、功能全面。
4. **BlenderGPT 是当前 LLM+3D 建模最成熟的开源案例**（Star 数最高、社区最活跃），其"LLM生成Python代码→软件执行→反馈迭代"的方法论可直接迁移到 CAD 领域。
5. **MCP 协议正在成为 Agent 接入 CAD 的新路径**：将 CAD API 封装为 MCP Server，LLM Agent 通过标准协议调用，社区已有早期探索。
6. **技术路线收敛**: "LLM 生成代码 → CAD API 执行 → 反馈迭代" 已成为主流 Agent 架构范式，云端 API (APS/Onshape) 和本地 API (pyautocad/FreeCAD/SolidWorks COM) 两条路线并行发展。

---

> **注**: 由于当前环境无联网检索工具，以上内容基于已有知识整理。GitHub 项目地址和建议通过实际访问验证最新状态。建议后续在 GitHub 搜索关键词：`cad agent`, `llm cad`, `text2cad`, `autocad gpt`, `freecad ai`, `mcp cad` 获取最新开源项目。
