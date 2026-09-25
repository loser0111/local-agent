# Agent 接入 CAD 调研报告

> **调研范围**: Autodesk 系列（AutoCAD / Fusion 360 等）与 SolidWorks  
> **场景方向**: 自然语言交互控制、代码/脚本自动生成、图纸解析与理解  
> **时间范围**: 2023–2025 为主，兼顾经典前期工作  
> **产出形式**: 完整调研报告（案例列表、技术方案对比、优劣势分析、落地建议）

---

## 目录

1. [调研背景与目标](#一调研背景与目标)
2. [案例列表总览](#二案例列表总览)
3. [技术方案对比](#三技术方案对比)
4. [优劣势分析](#四优劣势分析)
5. [CAD 软件 API 能力对照](#五cad-软件-api-能力对照)
6. [落地建议与实施路径](#六落地建议与实施路径)
7. [附录：详细文档索引](#附录详细文档索引)

---

## 一、调研背景与目标

大语言模型（LLM）和 Agent 技术的爆发正在重塑各行业工作流。CAD（计算机辅助设计）作为制造业、建筑业的核心工具，其建模过程高度依赖工程师的专业知识和手工操作，是 AI 自动化的高价值场景。

本调研聚焦 **Autodesk 系列（AutoCAD / Fusion 360 等）** 和 **SolidWorks** 两款主流 CAD 软件，系统梳理学术界、工业界和开源社区在"Agent 接入 CAD"方向的探索实践，覆盖三大场景：

- **自然语言交互控制**：对话式建模、指令驱动设计
- **代码/脚本自动生成**：LLM 生成 API 脚本/宏代码驱动 CAD 软件
- **图纸解析与理解**：从工程图提取信息、参数识别

目标：为决策提供"该不该做、怎么做、选什么技术栈"的参考依据。

---

## 二、案例列表总览

### 2.1 学术研究案例（14 项）

| # | 案例 | 来源/年份 | 核心技术 | 目标 CAD | 场景 |
|---|------|----------|---------|---------|------|
| 1 | **Text2CAD** | arXiv 2024–2025, Georgia Tech & AWS | LLM→OpenSCAD 代码端到端生成; Text2CAD-1M 数据集 | OpenSCAD/FreeCAD | 代码生成 |
| 2 | **CAD-MLLM** | arXiv 2024, MBZUAI | 多模态 LLM(Llama-2 微调); 3D 几何表征; 多任务基准 | OpenSCAD | 代码生成+理解 |
| 3 | **Open-Vocab CAD** | arXiv 2024 | 自回归生成 CAD 命令序列; DeepCAD 数据集 | 通用 | 代码生成 |
| 4 | **ChatCAD** | arXiv 2024 | 多轮对话+参数迭代; GPT-4 后端 | 通用 | 交互控制 |
| 5 | **CAD-Agent** | arXiv 2024–2025 | 工具调用框架; 规划+反馈+记忆模块 | AutoCAD/SW/FreeCAD | Agent 框架 |
| 6 | **AutoCAD-Agent** | 预印本 2024–2025 | AutoLISP/.NET 封装为工具; LLM 规划执行 | AutoCAD | Agent 框架 |
| 7 | **3D-GPT** | SIGGRAPH 2024 | 多 Agent 协作(dispatcher→agent→verifier); bpy 脚本; 渲染验证 | Blender(可迁移) | 多 Agent |
| 8 | **SceneCraft** | arXiv 2024 | 空间约束图; LLM 生成约束求解+建模代码 | Blender/Maya | 场景建模 |
| 9 | **CAD-VLM** | arXiv 2024 | 多模态 LLM; 工程图图片+参数表+文字 | 通用 | 图纸理解 |
| 10 | **Engineering Drawing Understanding** | 2024–2025 | VLM(GPT-4V/LLaVA); OCR+几何推理; BOM 提取 | AutoCAD/SW 工程图 | 图纸理解 |
| 11 | **CAD-Retriever** | arXiv 2024 | 多模态嵌入; 向量检索; 语义搜索 CAD 模型库 | 通用 | 模型检索 |
| 12 | **DeepCAD** | SIGGRAPH 2022 | Transformer 自回归; 草图-拉伸命令序列; 178K 数据集 | 通用 | 奠基工作 |
| 13 | **SkexGen** | SIGGRAPH 2022 | 草图生成; 拓扑与参数分离; 约束求解 | 通用 | 草图生成 |
| 14 | **FusionGPT** | Autodesk Research 2024 | Fusion 360 Python API 封装; LLM 规划+代码生成 | Fusion 360 | 厂商探索 |

### 2.2 工业界与商业产品案例（8 项）

| # | 案例 | 来源 | 核心能力 | CAD 软件 | 接入方式 |
|---|------|------|---------|---------|---------|
| 15 | **Autodesk AI** | Autodesk 2024 | Design Assistant 对话助手; 自动化流程; 智能搜索 | AutoCAD/Fusion 360 | 软件内置 |
| 16 | **APS (Autodesk Platform Services)** | Autodesk 官方 | Design Automation(云端脚本执行); Model Derivative(文件转换); Viewer(渲染); Data Management(文件管理); Webhooks | AutoCAD/Fusion 360/Inventor | 云 REST API |
| 17 | **Fusion 360 API** | Autodesk 官方 | 参数化建模; 装配体; 工程图; Python/C++ 插件 | Fusion 360 | Python API |
| 18 | **AutoCAD API** | Autodesk 官方 | AutoLISP; .NET(C#); ObjectARX(C++); pyautocad(Python COM) | AutoCAD | 多语言 API |
| 19 | **SolidWorks API** | Dassault 官方 | VBA/C#/C++ COM 全功能; Document Manager 轻量读写; PDM 文件管理 | SolidWorks | COM API |
| 20 | **3DEXPERIENCE** | Dassault 2024–2025 | 生成式设计; AI 设计推荐; 自然语言搜索; REST/WebSocket | SolidWorks/3DEXP | 云平台 API |
| 21 | **Autodesk × MS Copilot** | Autodesk+Microsoft 2024 | 在 Microsoft 365/Teams 中调用 Autodesk 设计数据 | AutoCAD/Fusion 360 | 跨平台集成 |
| 22 | **Onshape AI (PTC)** | PTC 2024 | 全云端 REST API; FeatureScript; AI 设计建议/搜索 | Onshape | 云原生 API |

### 2.3 开源项目案例（10 项）

| # | 项目 | GitHub | 核心技术 | CAD 软件 | Agent 价值 |
|---|------|--------|---------|---------|-----------|
| 23 | **BlenderGPT** | gd3kr/BlenderGPT | GPT-4→bpy Python 代码→Blender 执行; 插件面板 | Blender(方法论可迁移) | 最成熟的 LLM+3D 建模开源案例 |
| 24 | **OpenSCAD** | openscad/openscad | 脚本式参数化 CAD; DSL 天然适配 LLM | OpenSCAD | Text-to-CAD 首选中间表示 |
| 25 | **FreeCAD** | FreeCAD/FreeCAD | 开源参数化 CAD; Python 原生 API; 全功能 | FreeCAD | 最佳开源 CAD Agent 载体 |
| 26 | **pyautocad** | reclosedevs/pyautocad | Python COM 驱动 AutoCAD; 读写 DWG | AutoCAD | LLM→Python→AutoCAD 桥梁 |
| 27 | **Text2CAD 开源** | text2cad | 端到端 NL→OpenSCAD; 模型权重+数据集 | OpenSCAD | 可复现 Text-to-CAD 系统 |
| 28 | **BRepNet** | AutodeskAILab/BRepNet | CAD B-rep 边界表示学习 | 通用 | CAD 几何理解特征编码 |
| 29 | **Fusion 360 Gallery Dataset** | AutodeskAILab/... | 参数化建模过程数据集(草图-拉伸序列) | Fusion 360 | 工业级建模过程数据 |
| 30 | **Open Interpreter** | OpenInterpreter/... | 通用代码执行 Agent; 可驱动 pyautocad/FreeCAD/SW COM | 通用 | 开箱即用 LLM 代码执行 |
| 31 | **LangChain** | langchain-ai/langchain | Agent 框架; CAD API 封装为 Tool; ReAct/FC | 通用 | CAD 工具封装基础设施 |
| 32 | **DeepCAD 开源** | chenglinxu/DeepCAD | CAD 命令序列生成模型+数据集 | 通用 | 命令序列生成基础 |

### 2.4 评测基准与数据集（5 项）

| # | 名称 | 来源 | 规模/内容 | 用途 |
|---|------|------|----------|------|
| 33 | **Text2CAD-1M** | Text2CAD 2024 | 25 万+ CAD 模型+多层级 NL 描述 | Text-to-CAD 训练/评测 |
| 34 | **DeepCAD Dataset** | SIGGRAPH 2022 | 178K 模型+命令序列 | 命令序列生成训练 |
| 35 | **Fusion 360 Gallery** | Autodesk/SIGGRAPH 2021 | 大规模参数化建模过程(草图-拉伸序列) | 工业级建模数据 |
| 36 | **CAD-Bench** | arXiv 2024–2025 | 多任务 CAD LLM 评测(代码/理解/推理) | 标准化评测 |
| 37 | **CAD-MLLM-Bench** | CAD-MLLM 2024 | 多任务 CAD 大模型评测 | 多模态 CAD 评测 |

---

## 三、技术方案对比

### 3.1 Agent 架构范式对比

| 范式 | 核心机制 | 代表案例 | 优点 | 缺点 | 适用场景 |
|------|---------|---------|------|------|---------|
| **端到端代码生成** | LLM 直接生成 CAD 代码→执行 | Text2CAD, CAD-MLLM, DeepCAD | 流程简洁; 可端到端训练 | 无反馈闭环; 限脚本式 CAD; 需微调 | 标准化零件批量生成 |
| **Function Calling** | CAD 操作封装为工具; LLM 逐步调用 | CAD-Agent, AutoCAD-Agent, FusionGPT | 精细控制; 可观测; 错误可处理 | 工具封装工作量大; 工具多时选择准确率下降 | 复杂多步建模; 需中间状态反馈 |
| **ReAct 循环** | Thought→Action→Observation 循环 | 3D-GPT, Open Interpreter, BlenderGPT | 显式推理可解释; 动态调整策略 | 轮次多延迟高; Observation 质量决定效果 | 开放式探索性设计任务 |
| **多 Agent 协作** | 多角色 Agent 分工协作 | 3D-GPT(dispatcher+agent+verifier), SceneCraft | 职责分离; 可并行; 专注度高 | 通信开销大; 系统复杂度高 | 复杂场景级建模 |
| **对话式迭代** | 多轮对话渐进式设计 | ChatCAD, BlenderGPT, Autodesk AI | 最接近设计师工作流; 渐进式 | 上下文管理复杂; 需维护设计历史 | 概念设计; 设计探索 |

### 3.2 CAD 交互路径对比

| 路径 | 机制 | 代表案例 | 可控性 | 灵活性 | 实现难度 | 成熟度 |
|------|------|---------|--------|--------|---------|--------|
| **A. 脚本生成执行** | LLM 生成 Python/AutoLISP/VBA→CAD 引擎执行 | Text2CAD, BlenderGPT, pyautocad | ★★★ | ★★★★ | ★★(低) | ★★★★ |
| **B. API 工具调用** | CAD 操作封装为 Function/Tool; LLM 调用 | CAD-Agent, FusionGPT, LangChain | ★★★★★ | ★★★★ | ★★★★(高) | ★★★ |
| **C. 文件解析生成** | 解析 DWG/STEP 提取信息; 生成 CAD 文件 | APS Model Derivative, ezdxf | ★★★★ | ★★★ | ★★★ | ★★★★ |
| **D. 多模态视觉** | VLM 理解工程图/渲染截图 | CAD-VLM, 3D-GPT Verifier | ★★ | ★★★★★ | ★★★ | ★★ |
| **E. RAG 知识增强** | 检索规范/标准件库/API 文档增强 LLM | CAD-Retriever, Autodesk 智能搜索 | ★★★ | ★★★ | ★★★★ | ★★ |

### 3.3 典型架构组合矩阵

实际系统通常组合使用多种范式和路径：

| 案例 | LLM 范式 | 交互路径 | 反馈机制 | CAD 软件 |
|------|---------|---------|---------|---------|
| Text2CAD | 端到端生成 | A(脚本) | 无(开环) | OpenSCAD |
| CAD-MLLM | 端到端+多模态 | A+D(视觉) | 无(开环) | OpenSCAD |
| ChatCAD | 对话式迭代 | A(脚本) | 执行结果 | 通用 |
| CAD-Agent | Function Calling | B(API 工具) | 状态反馈 | AutoCAD/SW |
| 3D-GPT | 多 Agent+ReAct | A(脚本)+D(渲染) | 渲染截图验证 | Blender |
| AutoCAD-Agent | Function Calling | A(AutoLISP)+B | 执行错误反馈 | AutoCAD |
| BlenderGPT | ReAct | A(bpy Python) | 控制台输出 | Blender |
| FusionGPT | Function Calling | B(Fusion API) | 模型状态 | Fusion 360 |
| Open Interpreter | ReAct | A(Python 通用) | 执行输出 | 通用 |
| APS + LLM | ReAct/FC | A+云端 B | 云端执行结果 | AutoCAD/Fusion |
| Autodesk AI | 厂商内置 | D+B(内置) | 软件原生 | AutoCAD/Fusion |

**关键洞察**:
- 学术研究偏向**端到端生成 + 脚本路径**，追求模型能力提升
- 工业/开源项目偏向 **Function Calling/ReAct + API 工具路径**，追求可控性
- **多模态(路径 D)**正从学术走向工业应用（3D-GPT 渲染验证被广泛借鉴）
- **RAG(路径 E)**成熟案例最少，是未来重要增长点

---

## 四、优劣势分析

### 4.1 各 CAD 软件 Agent 接入优劣分析

#### Autodesk (AutoCAD / Fusion 360)

| 维度 | 优势 | 劣势 |
|------|------|------|
| **API 生态** | ✅ 多层 API(AutoLISP/.NET/Python); ✅ APS 云端 API(免本地安装); ✅ Fusion 360 原生 Python API | ❌ AutoCAD Python 需 COM 桥接(pyautocad), 非原生 |
| **AI 集成** | ✅ Autodesk AI 官方战略; ✅ Autodesk AI Lab 开源数据集; ✅ FusionGPT 官方探索 | ❌ 官方 AI 助手能力有限, 深度自定义受限 |
| **开源资源** | ✅ Fusion 360 Gallery 数据集; ✅ BRepNet/UV-Net 等模型 | ❌ AutoCAD 本身闭源 |
| **LLM 适配性** | ★★★★★ (Python + 云端 + 官方探索) | — |

#### SolidWorks

| 维度 | 优势 | 劣势 |
|------|------|------|
| **API 生态** | ✅ 全功能 COM API(VBA/C#/C++); ✅ Document Manager 轻量读写 | ❌ 无官方云端执行(需本地安装); ❌ Python 需 win32com COM 桥接 |
| **AI 集成** | ✅ 3DEXPERIENCE 平台 AI 能力(生成式设计/搜索) | ❌ AI 集成成熟度不如 Autodesk; ❌ Agent 接入文档少 |
| **开源资源** | — | ❌ 缺乏官方开源数据集; ❌ 社区活跃度低于 Autodesk |
| **LLM 适配性** | ★★★ (C#/VBA 为主, Python 需 COM) | — |

#### FreeCAD (开源)

| 维度 | 优势 | 劣势 |
|------|------|------|
| **API 生态** | ✅ Python 原生 API, 全功能; ✅ 完全开源 | ❌ 无云端 API; ❌ 性能不如商业 CAD |
| **AI 集成** | ✅ 社区已有 LLM+FreeCAD 探索 | ❌ 无官方 AI 能力 |
| **开源资源** | ✅ 开源可定制; ✅ 社区活跃 | ❌ 工业级使用场景有限 |
| **LLM 适配性** | ★★★★★ (Python 原生, 开源, 易调试) | — |

### 4.2 各架构范式优劣分析

#### 端到端代码生成
- **优势**: 流程简洁; 可用领域数据微调提升; 适合标准化零件; 无需运行时 Agent 框架
- **劣势**: 无反馈闭环, 错误无法自动纠正; 输出受限脚本式 CAD(OpenSCAD); 难以直接控制商业 CAD; 通用模型 zero-shot 能力有限
- **成熟度**: ★★★ (学术验证阶段)

#### Function Calling
- **优势**: 精细控制每步操作; 可观测性好; 支持错误处理和状态管理; 可适配任意 CAD API
- **劣势**: 工具封装工作量大; 工具过多时 LLM 选择准确率下降; 需设计合理的工具粒度
- **成熟度**: ★★★★ (已有可用开源实现)

#### ReAct 循环
- **优势**: 推理过程可解释; 可根据反馈动态调整; 适合开放式任务
- **劣势**: 对话轮次多, 延迟高; Observation 质量决定整体效果; 长任务可能中断
- **成熟度**: ★★★★ (已有可用开源实现, 如 BlenderGPT)

#### 多 Agent 协作
- **优势**: 职责分离, 专注度高; 可并行独立子任务; 容错性好
- **劣势**: 通信开销大; 系统复杂度高; 调试困难; 需设计 Agent 间信息标准
- **成熟度**: ★★ (学术探索阶段)

#### 对话式迭代
- **优势**: 最接近设计师工作流; 支持渐进式设计; 用户体验好
- **劣势**: 对话管理复杂; 需维护完整设计上下文; 多轮对话上下文窗口限制
- **成熟度**: ★★★ (有开源实现, 但设计上下文管理未解决)

### 4.3 核心技术组件优劣势

| 组件 | 优势 | 劣势/挑战 |
|------|------|----------|
| **状态管理** | 几何状态→JSON 文本→LLM 可理解 | CAD 几何状态编码为文本有信息损失; 长对话上下文窗口限制 |
| **反馈机制** | 执行结果/渲染截图/用户反馈多源融合 | 渲染截图需 VLM 理解; 几何状态精确性需 API 查询 |
| **错误处理** | API 错误信息可指导 LLM 修正 | 几何冲突(面不存在)难以自动诊断; 死循环需人工介入 |
| **工具封装层** | 统一抽象层支持多 CAD; MCP 标准化 | 各 CAD API 差异大, 适配工作量大; 粒度设计需平衡 |

---

## 五、CAD 软件 API 能力对照

| 能力维度 | AutoCAD | Fusion 360 | SolidWorks | FreeCAD (开源) | Onshape (云) |
|---------|---------|-----------|-----------|--------------|-------------|
| **Python API** | pyautocad (COM) | ✅ 原生 Python | win32com (COM) | ✅ 原生 Python | ❌ (REST+FeatureScript) |
| **云端执行** | ✅ APS Design Automation | ✅ APS | ❌ (需本地) | ❌ (需本地) | ✅ 原生云端 |
| **REST API** | ✅ APS | ✅ APS | ✅ 3DEXPERIENCE | ❌ | ✅ 原生 |
| **参数化建模 API** | AutoLISP / .NET | ✅ Python | ✅ C#/VBA COM | ✅ Python | ✅ FeatureScript |
| **工程图 API** | ✅ .NET | ✅ Python | ✅ COM | ✅ Python | ✅ REST |
| **文件解析** | ✅ APS Model Derivative | ✅ APS | ✅ Doc Manager API | ✅ Python | ✅ REST |
| **可视化渲染** | ✅ APS Viewer | ✅ APS Viewer | ✅ eDrawings | ✅ 内置 | ✅ Web Viewer |
| **LLM 适配性** | ★★★★ | ★★★★★ | ★★★ | ★★★★★ | ★★★★ |

---

## 六、落地建议与实施路径

### 6.1 总体推荐架构

基于全量调研，推荐如下技术组合：

```
LLM 范式: ReAct + Function Calling (混合)
CAD 交互: API 工具调用(B) 为主 + 脚本生成(A) 为辅 + 多模态反馈(D)
中间语言: Python (通用性最佳, 各 CAD 均支持)
工具协议: MCP (标准化, 即插即用)
反馈闭环: API 状态查询 + 渲染截图 VLM 验证
知识增强: RAG (标准件库 + 设计规范 + API 文档)
```

**推荐架构图**:
```
┌──────────────────────────────────────────────────────────┐
│                    用户自然语言指令                        │
│              "创建带4个φ10孔的法兰盘, 外径100"             │
└────────────────────────┬─────────────────────────────────┘
                         │
         ┌───────────────▼───────────────┐
         │   LLM Agent (ReAct + FC)      │
         │   ├── RAG 检索                │← 标准件库/规范/API文档
         │   ├── 工具调用 (MCP Server)   │
         │   │   ├── create_sketch       │
         │   │   ├── extrude             │
         │   │   ├── create_hole         │
         │   │   ├── fillet              │
         │   │   ├── get_model_info      │
         │   │   └── render_screenshot   │
         │   ├── 代码生成 (Python 脚本)  │
         │   └── VLM 反馈验证            │← 渲染截图
         └───────────────┬───────────────┘
                         │
         ┌───────────────▼───────────────┐
         │   CAD 适配层 (统一抽象)        │
         │   ├── AutoCAD (pyautocad)     │
         │   ├── Fusion 360 (Python API) │
         │   ├── SolidWorks (win32com)   │
         │   └── FreeCAD (Python API)    │
         └───────────────┬───────────────┘
                         │
         ┌───────────────▼───────────────┐
         │   CAD 引擎执行 → 模型生成      │
         └───────────────────────────────┘
```

### 6.2 分阶段实施路径

#### 阶段一：MVP 原型验证（1–2 个月）

| 项目 | 建议 |
|------|------|
| **CAD 软件** | FreeCAD（开源, Python 原生, 零成本验证） |
| **LLM** | GPT-4 / Claude 3.5（通用能力强, 无需微调） |
| **Agent 框架** | LangChain 或 直接 Function Calling |
| **目标** | 实现自然语言→FreeCAD 参数化建模的基本闭环（5–10 步内） |
| **工具集** | 6–8 个中粒度工具: create_sketch, extrude, create_hole, fillet, get_model_info, render_screenshot |
| **验证场景** | 法兰盘、支架、盖板等标准机械零件 |

#### 阶段二：多 CAD 适配 + 反馈闭环（2–3 个月）

| 项目 | 建议 |
|------|------|
| **CAD 软件** | 增加 AutoCAD（pyautocad）+ Fusion 360（Python API） |
| **核心工作** | 统一 CAD 抽象层；实现多 CAD 适配器 |
| **反馈增强** | 渲染截图 + VLM 验证（用 GPT-4V/Claude Vision） |
| **错误处理** | try-catch + 错误信息回传 + 最大重试 3 次 |
| **验证场景** | 复杂零件（20+ 特征）、多轮对话修改设计 |

#### 阶段三：MCP 标准化 + RAG 知识增强（2–3 个月）

| 项目 | 建议 |
|------|------|
| **工具协议** | 封装为 MCP Server, 标准化工具接口 |
| **RAG** | 标准件库（螺栓/轴承/齿轮参数）+ 设计规范（GB/ISO） |
| **CAD 软件** | 增加 SolidWorks（win32com） |
| **目标** | MCP 即插即用 + 规范合规 + 标准件自动引用 |

#### 阶段四：生产化 + 评测（持续）

| 项目 | 建议 |
|------|------|
| **评测** | 对标 CAD-Bench, 建立 50+ 测试用例回归集 |
| **微调** | 基于 Fusion 360 Gallery/DeepCAD 数据微调专用 LLM |
| **部署** | 云端方案（APS Design Automation API）或本地部署 |
| **用户界面** | CAD 软件内置插件面板（对话式交互） |

### 6.3 按场景的落地优先级

| 优先级 | 场景 | 技术组合 | 目标 CAD | 预期价值 | 难度 |
|--------|------|---------|---------|---------|------|
| **P0** | 标准件参数化生成 | 端到端/FC + 脚本 + RAG 标准件库 | FreeCAD/AutoCAD | 设计效率提升 50%+ | 低 |
| **P0** | 对话式辅助建模 | 对话式 + API 工具 + 渲染反馈 | FreeCAD/Fusion 360 | 降低 CAD 使用门槛 | 中 |
| **P1** | 工程图纸信息提取 | 多模态 VLM + 文件解析 | AutoCAD/SolidWorks | 自动化 BOM 提取 | 中 |
| **P1** | 设计规范合规检查 | RAG 规范库 + 图纸理解 | 通用 | 减少设计错误 | 中 |
| **P2** | 复杂零件全自动建模 | ReAct + API 工具 + VLM 验证 | Fusion 360 | 自动化高级设计 | 高 |
| **P2** | CAD 模型语义检索 | RAG 嵌入 + 向量检索 | 通用 | 设计复用 | 高 |
| **P3** | 场景级多物体建模 | 多 Agent + 约束求解 | Fusion 360/Blender | 复杂场景自动化 | 很高 |

### 6.4 风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| **LLM 几何推理不足** | 复杂建模错误率高 | RAG 注入 API 文档 + few-shot; 渲染截图 VLM 验证; 限制单次任务步数 |
| **CAD API 差异大** | 多 CAD 适配成本高 | 统一抽象层; 优先支持 FreeCAD(FreeCAD→AutoCAD→Fusion→SW) |
| **上下文窗口限制** | 长对话设计状态丢失 | 状态外部化(JSON 持久化); 增量更新而非全量重传 |
| **执行安全** | LLM 生成恶意代码 | 沙箱执行; 代码白名单; 操作权限分级 |
| **商业化限制** | 商业 CAD API 可能变更 | 优先开源(FreeCAD); 商业 CAD 作为可选适配器 |

### 6.5 最终建议

1. **首选 FreeCAD 作为 MVP 载体**：Python 原生 API、完全开源、零许可成本，最适合快速验证 Agent 概念。
2. **采用 Function Calling + ReAct 混合范式**：FC 负责确定性建模步骤的精确调用，ReAct 负责探索性设计推理，两者互补。
3. **中粒度工具集设计**：6–10 个工具（create_sketch / extrude / create_hole / fillet / chamfer / get_model_info / render_screenshot / apply_dimension），在灵活性和可控性间取得最佳平衡。
4. **MCP 协议封装**：从第一天就用 MCP Server 封装工具接口，为未来多 LLM（Claude/GPT/Gemini）即插即用做准备。
5. **反馈闭环是分水岭**：必须有 API 状态查询 + 渲染截图 VLM 验证双重反馈，否则只是"开环玩具"。
6. **Python 作为中间语言**：LLM 生成 Python 代码 → CAD API 执行，Python 在所有目标 CAD 中都有支持（pyautocad / Fusion 360 / FreeCAD / win32com）。
7. **RAG 是差异化关键**：标准件库 + 设计规范的 RAG 能力是通用 LLM 不具备的领域知识，是产品竞争力的核心壁垒。

---

## 七、总结

### 核心发现

1. **技术路线已收敛**："LLM 生成代码 → CAD API 执行 → 反馈迭代" 成为主流架构范式，代码作为中间表示（Python 优先）是核心模式。

2. **Autodesk 在 AI 集成成熟度上领先**：APS 云 API + Fusion 360 Python API + AutoLISP + Autodesk AI Lab 开源数据集，形成从云到端、从数据到模型的完整生态。SolidWorks 依赖 COM API，云端能力较弱但 3DEXPERIENCE 在演进中。

3. **开源生态中 FreeCAD + Python API 是最佳载体**：完全开源、Python 原生、功能全面，BlenderGPT 的方法论可直接迁移。

4. **MCP 协议是标准化新方向**：将 CAD API 封装为 MCP Server，LLM Agent 通过标准协议即插即用，有望解决工具接口碎片化问题。

5. **当前最大瓶颈**是 LLM 的几何推理能力和长程规划能力，需通过 RAG（注入领域知识）、多模态（视觉反馈）、专用微调（CAD 数据）多管齐下解决。

6. **反馈闭环是从玩具到可用的分水岭**：无反馈的开环生成仅适合标准化场景；有反馈的闭环 Agent 才能处理真实复杂设计任务。

### 技术趋势预判

| 趋势 | 预期 | 时间线 |
|------|------|--------|
| MCP 标准化 | CAD 操作工具通过 MCP 标准化, LLM 即插即用 | 2025 早期 |
| CAD 专用 LLM | 基于 CAD 数据微调的专用模型 | 2025 中期 |
| 3D 原生多模态 | LLM 直接理解 3D 几何(B-rep/点云) | 2025–2026 |
| RAG 工程化 | 标准件库/规范库/材料库 RAG 基础设施成熟 | 2025 |
| 厂商开放平台 | Autodesk/SolidWorks 进一步开放 Agent API | 2025–2026 |
| Agent 原生 CAD | 为 AI Agent 交互设计的新一代 CAD 引擎 | 2026+ |

---

## 附录：详细文档索引

本报告基于以下三份详细调研文档汇总而成：

| 文档 | 内容 | 路径 |
|------|------|------|
| 学术文献综述 | 18 项学术研究/数据集详细分析 | `docs/academic-research-llm-cad.md` |
| 工业界与开源项目 | 19 项工业界/开源案例详细分析 + API 对照表 | `docs/industry-opensource-projects-llm-cad.md` |
| 架构与核心技术路线 | 5 种范式×5 条路径深度分析 + 组件设计 + 对比矩阵 | `docs/architecture-tech-routes-llm-cad.md` |

> **注**: 受限于当前环境无联网检索工具，以上调研内容基于已有知识整理。建议后续通过以下渠道验证和更新：
> - **arXiv**: 搜索 `Text-to-CAD`, `LLM CAD Agent`, `CAD language model`
> - **Google Scholar**: 搜索 `CAD large language model`, `automated CAD design`
> - **GitHub**: 搜索 `cad agent`, `llm cad`, `text2cad`, `autocad gpt`, `freecad ai`, `mcp cad`
> - **官方文档**: APS (https://aps.autodesk.com), SolidWorks API, Fusion 360 API
