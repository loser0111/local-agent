# Agent 接入 CAD：典型架构与核心技术路线分析

> 基于步骤 2（学术文献）与步骤 3（工业界/开源项目）调研成果，本文档系统分析 LLM/Agent 接入 CAD 的典型架构模式、核心技术组件、CAD 交互技术路径，以及各方案对比。

---

## 一、Agent 架构全景图

当前 Agent 接入 CAD 的方案可从两个正交维度分类：**LLM 调度范式**（Agent 如何决策与执行）和 **CAD 交互路径**（Agent 如何与 CAD 软件通信）。

```
┌─────────────────────────────────────────────────────────┐
│                   用户自然语言指令                         │
│         "创建一个带四个φ10孔的法兰盘，外径100，厚10"          │
└──────────────────────────┬──────────────────────────────┘
                           │
              ┌────────────▼────────────┐
              │    LLM 调度范式 (横轴)    │
              │                         │
              │  ① 端到端代码生成         │  (Text2CAD, CAD-MLLM)
              │  ② Function Calling     │  (CAD-Agent, LangChain)
              │  ③ ReAct 推理-行动循环    │  (3D-GPT, Open Interpreter)
              │  ④ 多 Agent 协作         │  (3D-GPT dispatcher→agent→verifier)
              │  ⑤ 对话式迭代            │  (ChatCAD, BlenderGPT)
              └────────────┬────────────┘
                           │
              ┌────────────▼────────────┐
              │   CAD 交互路径 (纵轴)     │
              │                         │
              │  A. 脚本生成执行         │  (Python/AutoLISP/VBA → CAD引擎)
              │  B. API 工具调用         │  (Function Calling → CAD API)
              │  C. 文件解析与生成        │  (DWG/STEP → 解析; LLM → 生成文件)
              │  D. 多模态视觉理解       │  (工程图图像 → VLM → 结构化信息)
              │  E. RAG 知识增强         │  (设计规范/标准件库 → 检索 → 增强)
              └─────────────────────────┘
```

---

## 二、LLM 调度范式分析

### 范式 ①：端到端代码生成（End-to-End Code Generation）

**架构**:
```
自然语言 → [微调LLM/通用LLM] → CAD代码(OpenSCAD/Python) → CAD引擎执行
```

**代表案例**:
| 案例 | 模型 | 输出语言 | CAD 引擎 |
|------|------|---------|---------|
| Text2CAD | CodeLlama/DeepSeek-Coder 微调 | OpenSCAD | OpenSCAD |
| CAD-MLLM | Llama-2 微调 + 3D编码 | OpenSCAD | OpenSCAD |
| DeepCAD | Transformer 自回归 | CAD 命令序列 | 自定义引擎 |
| Open-Vocab CAD | Transformer 自回归 | CAD 命令序列 | DeepCAD 引擎 |

**特征**:
- **优点**: 流程简洁，端到端可训练，适合标准化零件生成
- **缺点**: 无反馈闭环，生成后无法自动纠错；输出受限为脚本式 CAD（OpenSCAD），难以直接控制 AutoCAD/SolidWorks
- **适用场景**: 标准化参数化零件生成、批量设计自动化
- **LLM 依赖**: 通常需要领域数据微调，通用模型 zero-shot 能力有限

### 范式 ②：Function Calling（工具调用）

**架构**:
```
用户指令 → LLM (Function Calling)
  → 选择 CAD 工具: create_sketch(width, height)
  → 执行 → 返回 sketch_id
  → LLM → 选择下一步: extrude(sketch_id, depth)
  → 执行 → 返回 body_id
  → LLM → 选择: fillet(edge_ids, radius)
  → ... 循环直到完成
```

**代表案例**:
| 案例 | LLM | 工具封装形式 | CAD 软件 |
|------|-----|------------|---------|
| CAD-Agent | GPT-4 | CAD API 封装为 function | AutoCAD/SolidWorks/FreeCAD |
| AutoCAD-Agent | GPT-4 | AutoLISP/.NET 封装为 function | AutoCAD |
| LangChain CAD Tool | 任意 | CAD API → LangChain Tool | 通用 |
| FusionGPT (Autodesk) | GPT-4 | Fusion 360 Python API 封装 | Fusion 360 |

**特征**:
- **优点**: 精细控制每步操作，可观测性强，支持错误处理和状态管理
- **缺点**: 工具封装工作量大；LLM 需理解工具语义，工具过多时选择准确率下降
- **适用场景**: 复杂多步建模、需要中间状态反馈的场景
- **关键挑战**: 工具粒度设计——太粗（如单个 `create_model`）则 LLM 无法有效控制，太细（如逐点画线）则规划步数过多

### 范式 ③：ReAct 推理-行动循环（Reasoning + Acting）

**架构**:
```
用户指令 → LLM 进入循环:
  Thought: "用户需要法兰盘，先创建圆柱体作为基体"
  Action: create_cylinder(diameter=100, height=10)
  Observation: "圆柱体创建成功, body_id=B1"
  Thought: "现在需要在外圆周上打4个孔，先计算孔位置"
  Action: create_circular_pattern_holes(body=B1, count=4, pitch_diameter=80, hole_diameter=10)
  Observation: "4个孔创建成功"
  Thought: "任务完成"
  → 输出最终结果
```

**代表案例**:
| 案例 | 框架 | CAD 软件 | 反馈来源 |
|------|------|---------|---------|
| 3D-GPT | 自定义多Agent | Blender | 渲染截图 |
| Open Interpreter + CAD | Open Interpreter | AutoCAD/FreeCAD/SolidWorks | 执行输出 |
| BlenderGPT | 自定义 | Blender | 控制台输出 |

**特征**:
- **优点**: 显式推理过程可解释，LLM 可根据反馈动态调整策略
- **缺点**: 对话轮次多，延迟高；Observation 质量决定整体效果
- **适用场景**: 开放式设计任务、需要探索性推理的场景
- **关键挑战**: Observation 设计——如何将 CAD 状态有效编码为 LLM 可理解的文本/图像

### 范式 ④：多 Agent 协作（Multi-Agent）

**架构**:
```
用户指令 → [Dispatcher Agent] 分配任务
  → [Planning Agent] 分解建模步骤
  → [Modeling Agent] 生成代码执行建模
  → [Verification Agent] 渲染/检查输出
  → 反馈 → [Modeling Agent] 修正
  → 循环直到验证通过
```

**代表案例**:
| 案例 | Agent 角色 | CAD 软件 |
|------|-----------|---------|
| 3D-GPT | Dispatcher + Agent + Verifier | Blender |
| SceneCraft | Planning + Modeling + Constraint Solving | Blender/Maya |
| CAD-Agent (扩展) | Planner + Executor + Validator | AutoCAD/FreeCAD |

**特征**:
- **优点**: 职责分离，单个 Agent 专注度高；可并行执行独立子任务
- **缺点**: 通信开销大，系统复杂度高，调试困难
- **适用场景**: 复杂场景级建模、多约束设计任务
- **关键挑战**: Agent 间信息传递的标准化——几何状态如何在 Agent 间共享

### 范式 ⑤：对话式迭代设计（Conversational Iteration）

**架构**:
```
Round 1: 用户 "创建一个100×100×10的板" → LLM生成代码 → 执行
Round 2: 用户 "在四角打φ8的孔" → LLM修改参数 → 重新执行
Round 3: 用户 "边缘倒R5圆角" → LLM修改参数 → 重新执行
```

**代表案例**:
| 案例 | LLM | CAD 软件 | 对话方式 |
|------|-----|---------|---------|
| ChatCAD | GPT-4 | 通用(Python API) | 多轮对话+参数修改 |
| BlenderGPT | GPT-4 | Blender | 插件面板对话 |
| Autodesk AI Assistant | 厂商模型 | AutoCAD/Fusion 360 | 软件内置面板 |

**特征**:
- **优点**: 最接近设计师自然工作流，支持渐进式设计
- **缺点**: 对话管理复杂，需维护完整设计上下文（参数历史）
- **适用场景**: 概念设计阶段、设计探索与迭代
- **关键挑战**: 上下文持久化——如何在多轮对话中维护 CAD 模型的完整状态

---

## 三、CAD 交互技术路径分析

### 路径 A：脚本生成与执行（Script Generation & Execution）

**核心机制**: LLM 生成 CAD 软件原生脚本语言代码，直接在 CAD 引擎中执行。

| CAD 软件 | 脚本语言 | 执行方式 | 典型代码模式 |
|---------|---------|---------|-------------|
| AutoCAD | AutoLISP | `APPLOAD` 加载 .lsp | `(command "CIRCLE" '(0 0) 50)` |
| AutoCAD | Python (pyautocad) | COM 接口 | `acad.model.AddCircle(center, radius)` |
| Fusion 360 | Python API | Scripts and Add-Ins 面板 | `sketches.add(baseFace); profile = ...` |
| SolidWorks | VBA Macro | 宏执行 | `swModel.CreateFeature(...)` |
| SolidWorks | C# (COM) | 编译DLL加载 | `CreateFeature(...)` |
| FreeCAD | Python API | 内置Python控制台 | `Part.makeBox(10,10,10)` |
| OpenSCAD | OpenSCAD DSL | 命令行 | `cylinder(h=10, r=50);` |

**技术细节**:
- **代码注入安全**: 需沙箱执行，防止恶意代码；AutoCAD 的 AutoLISP 本身有限沙箱，Python 需额外控制
- **错误处理**: LLM 生成的代码可能包含 API 调用错误，需 try-catch + 错误信息回传 LLM
- **代码模板**: 实践中常用 few-shot prompt 提供代码模板，提高生成准确性
- **执行反馈**: 执行成功/失败信息 + CAD 对象状态（几何参数）需编码回传

**典型 pipeline 详解（以 FreeCAD + LLM 为例）**:
```
1. 用户: "创建一个直径100、高度50的圆柱体"
2. LLM 生成:
   import FreeCAD, Part
   doc = FreeCAD.newDocument("AgentDesign")
   cylinder = Part.makeCylinder(50, 100)  # radius, height
   Part.show(cylinder)
   doc.recompute()
3. FreeCAD 执行脚本
4. 反馈: "Document 'AgentDesign' created, 1 solid object, volume=392699mm³"
5. 用户: "在顶面打一个φ20的通孔"
6. LLM 生成增量代码:
   top_face = cylinder.Shape.Faces[-1]  # 获取顶面
   hole = Part.makeCylinder(10, 100)    # φ20×100 孔
   result = cylinder.cut(hole)
   ...
7. 迭代执行
```

### 路径 B：API 工具调用（API Tool Calling）

**核心机制**: 将 CAD 操作封装为粒度合适的工具函数，LLM 通过 Function Calling 逐个调用。

**工具粒度设计的三种层次**:

| 粒度层次 | 工具示例 | 优点 | 缺点 | 适用 |
|---------|---------|------|------|------|
| **粗粒度** | `create_flange(diameter, hole_count, hole_diameter, thickness)` | LLM 调用简单，步骤少 | 灵活性差，无法处理非标准设计 | 标准件生成 |
| **中粒度** | `create_sketch()`, `extrude()`, `fillet()`, `hole()` | 平衡灵活性和调用效率 | 工具设计需仔细定义接口 | **主流推荐** |
| **细粒度** | `add_point(x,y)`, `add_line(p1,p2)`, `add_arc(...)` | 最大灵活性 | 步骤过多，LLM 规划负担重 | 精确几何控制 |

**中粒度工具集设计示例（Fusion 360）**:
```python
tools = [
    {
        "name": "create_sketch",
        "description": "在指定平面上创建新草图",
        "parameters": {
            "plane": "XY/XZ/YZ/face_id",
            "profile": "JSON格式的草图轮廓定义"
        }
    },
    {
        "name": "extrude",
        "description": "将草图轮廓拉伸为实体",
        "parameters": {
            "sketch_id": "string",
            "distance": "number(mm)",
            "operation": "new_body/join/cut/intersect"
        }
    },
    {
        "name": "create_hole",
        "description": "在指定面上创建孔",
        "parameters": {
            "face_id": "string",
            "position": "[x, y]",
            "diameter": "number(mm)",
            "depth": "number(mm)|through"
        }
    },
    {
        "name": "fillet",
        "description": "对指定边倒圆角",
        "parameters": {
            "edge_ids": "string[]",
            "radius": "number(mm)"
        }
    },
    {
        "name": "get_model_info",
        "description": "获取当前模型的特征树、尺寸、体积等信息",
        "parameters": {}
    }
]
```

**关键设计原则**:
1. **状态管理**: 每个工具执行后返回对象 ID，后续工具引用此 ID
2. **查询能力**: 必须包含 `get_model_info` 类工具，让 LLM 能感知当前 CAD 状态
3. **错误回传**: 工具执行失败时返回结构化错误信息，而非简单异常
4. **幂等性**: 相同参数重复调用应产生相同结果，支持重试

### 路径 C：文件解析与生成（File Parsing & Generation）

**核心机制**: LLM/Agent 解析 CAD 文件提取信息，或直接生成 CAD 文件。

**解析方向（CAD → 信息）**:

| 文件格式 | 解析方式 | 提取信息 | 工具/API |
|---------|---------|---------|---------|
| DWG (AutoCAD) | APS Model Derivative API / Open Design Alliance | 图层、实体、标注 | APS REST / Teigha |
| STEP (通用) | Python OCC / FreeCAD Python | 拓扑结构、B-rep、参数 | pythonOCC |
| IPT (Inventor) | APS / Inventor API | 特征树、参数、约束 | APS / Inventor .NET |
| SLDPRT (SolidWorks) | SolidWorks Doc Manager API | 特征树、参数 | C# Doc Manager |
| DXF (2D) | ezdxf (Python) | 图元、图层、标注 | ezdxf 库 |

**生成方向（信息 → CAD）**:

| 生成方式 | 输出 | 适用场景 | 工具 |
|---------|------|---------|------|
| 生成 OpenSCAD 脚本 | .scad 文件 | 参数化零件 | LLM 直接生成 |
| 生成 Python 脚本 | 通过 API 生成模型 | AutoCAD/Fusion/FreeCAD | LLM 生成 → API 执行 |
| 生成 STEP/IGES | 通用 CAD 文件 | 跨平台交换 | pythonOCC / FreeCAD |
| 生成 DXF | 2D 图纸 | 平面设计 | ezdxf |
| 直接构造 B-rep | 精确几何模型 | 高精度需求 | pythonOCC |

**关键挑战**:
- CAD 文件格式复杂（DWG 二进制、STEP 文本但结构复杂），LLM 难以直接"理解"原始文件
- 实际做法：先用 API/库解析为结构化数据（JSON/特征树），再交给 LLM 处理

### 路径 D：多模态视觉理解（Multimodal Vision）

**核心机制**: 使用 VLM（视觉语言模型）理解 CAD 图纸图像、3D 渲染截图、模型预览图。

**三大应用场景**:

**场景 1: 工程图纸理解**
```
工程图截图 → VLM (GPT-4V/LLaVA)
  → 识别: 尺寸标注、公差、表面粗糙度、技术要求
  → 输出: 结构化参数表 {外径: 100±0.1, 孔径: φ10H7, ...}
  → 后续: 传入 LLM Agent 进行建模
```

**场景 2: 3D 模型渲染反馈（ReAct 中的 Observation）**
```
建模操作 → 渲染当前模型 → 截图
  → VLM 判断: "模型看起来正确/缺少特征/几何异常"
  → 反馈给 LLM: "当前模型为圆柱体，未看到孔特征"
  → LLM: 生成打孔代码
```

**场景 3: 草图/手绘 → 3D 模型**
```
手绘草图 → VLM 识别几何意图
  → "这是一个L型截面，长50宽30，厚10"
  → LLM 生成参数化建模代码
```

**代表案例**:
| 案例 | VLM 模型 | 输入 | 输出 |
|------|---------|------|------|
| CAD-VLM | Llama-2 + 视觉编码 | 工程图 + 文字 | 图纸问答/参数提取 |
| 3D-GPT Verifier | 渲染截图 | 3D模型截图 | 几何验证 |
| GPT-4V 工程图 | GPT-4V | 工程图照片 | 结构化参数 |

### 路径 E：RAG 知识增强（Retrieval-Augmented Generation）

**核心机制**: 检索设计规范、标准件库、历史设计文档，增强 LLM 的 CAD 设计知识。

**知识来源**:

| 知识类型 | 内容 | 检索方式 | 增强效果 |
|---------|------|---------|---------|
| **设计规范** | GB/ISO 标准、行业规范 | 向量检索 + 精确检索 | 确保设计合规 |
| **标准件库** | 螺栓、轴承、齿轮参数 | 参数化检索 | 直接引用标准参数 |
| **材料库** | 材料属性、力学参数 | 结构化查询 | 辅助设计决策 |
| **历史设计** | 企业已有 CAD 模型/参数 | 语义检索 | 设计复用、避免重复 |
| **API 文档** | CAD API 参考文档 | 向量检索 | 提高代码生成准确率 |
| **设计案例** | 已解决的建模问题 | RAG + few-shot | 类比推理 |

**RAG + CAD Agent 架构**:
```
用户指令 → [意图理解]
  → [RAG 检索] ← 标准件库/设计规范/API文档/历史设计
  → 检索结果 + 指令 → [LLM 规划]
  → [代码生成/工具调用]
  → [CAD 执行]
  → [反馈] → [LLM 迭代]
```

**关键挑战**:
- CAD 知识的向量表示：3D 几何/参数化模型如何编码为可检索的嵌入
- 检索精度：设计规范有精确数值要求，纯语义检索可能不够，需结合精确匹配
- 实际案例：CAD-Retriever（学术）、Autodesk 智能搜索（工业）是早期探索

---

## 四、架构模式组合矩阵

实际系统中，上述范式和路径通常组合使用。以下是已调研案例的组合分析：

| 案例 | LLM范式 | CAD交互路径 | 反馈机制 | CAD软件 |
|------|---------|-----------|---------|---------|
| **Text2CAD** | 端到端生成 | A(脚本生成) | 无(开环) | OpenSCAD |
| **CAD-MLLM** | 端到端生成 + 多模态 | A + D(视觉) | 无(开环) | OpenSCAD |
| **ChatCAD** | 对话式迭代 | A(脚本生成) | 执行结果 | 通用 |
| **CAD-Agent** | Function Calling | B(API工具) | 状态反馈 | AutoCAD/SW |
| **3D-GPT** | 多Agent + ReAct | A(脚本) + D(渲染反馈) | 渲染截图验证 | Blender |
| **AutoCAD-Agent** | Function Calling | A(AutoLISP) + B | 执行错误反馈 | AutoCAD |
| **BlenderGPT** | ReAct | A(bpy Python) | 控制台输出 | Blender |
| **FusionGPT** | Function Calling | B(Fusion API工具) | 模型状态 | Fusion 360 |
| **Open Interpreter** | ReAct | A(Python通用) | 执行输出 | 通用 |
| **APS + LLM** | ReAct/FC | A+云端B | 云端执行结果 | AutoCAD/Fusion |
| **CAD-VLM** | 多模态理解 | D(视觉理解) | - | 通用 |
| **Autodesk AI** | 厂商内置 | D+B(内置) | 软件原生 | AutoCAD/Fusion |

**洞察**:
- **学术研究**偏向范式①端到端生成 + 路径A脚本，追求模型能力提升
- **工业/开源项目**偏向范式②③Function Calling/ReAct + 路径B API工具，追求可控性
- **多模态(路径D)**正从纯学术走向工业应用（3D-GPT的渲染验证已被工业项目借鉴）
- **RAG(路径E)**目前成熟案例最少，是未来重要增长点

---

## 五、核心技术组件深度分析

### 5.1 状态管理（State Management）

CAD Agent 的核心挑战之一是管理设计状态的持久化和传递。

**状态类型**:

| 状态类型 | 内容 | 管理方式 | 挑战 |
|---------|------|---------|------|
| **几何状态** | 实体ID、拓扑关系、参数 | CAD引擎原生管理 | 需通过API查询，编码为LLM可理解文本 |
| **参数历史** | 每步操作的参数 | Agent 维护 JSON/dict | 多轮对话中的增量修改 |
| **特征树** | 建模操作序列 | CAD引擎特征树 | 需同步为文本表示 |
| **上下文** | 用户意图、设计约束 | LLM 对话历史 | 长对话上下文窗口限制 |

**状态传递格式（Feature Tree → LLM 可读文本）**:
```json
{
  "model": "flange_v1",
  "features": [
    {"id": "F1", "type": "sketch", "profile": "circle", "diameter": 100},
    {"id": "F2", "type": "extrude", "sketch": "F1", "distance": 10, "result": "B1"},
    {"id": "F3", "type": "hole_pattern", "face": "B1.top", "count": 4, "diameter": 10, "pitch_diameter": 80}
  ],
  "bodies": [{"id": "B1", "volume": 74600, "mass": 0.585}],
  "errors": []
}
```

### 5.2 反馈机制设计（Feedback Loop）

反馈是 Agent 从"开环生成"走向"闭环修正"的关键。

| 反馈类型 | 信息来源 | LLM 利用方式 | 代表案例 |
|---------|---------|-------------|---------|
| **执行成功/失败** | CAD API 返回值/异常 | 失败则修改代码重试 | BlenderGPT, Open Interpreter |
| **几何状态** | API 查询模型参数 | 判断是否满足设计要求 | CAD-Agent |
| **渲染截图** | 渲染引擎截图 → VLM | 视觉判断几何是否正确 | 3D-GPT |
| **工程规则** | 规则引擎检查 | 判断是否符合设计规范 | (未来方向) |
| **用户反馈** | 用户对话 | 修正设计意图理解 | ChatCAD |

**反馈循环的典型设计**:
```
LLM 生成代码 → 执行
  → 成功? 
    → Yes: 查询模型状态 → 符合设计? → Yes: 完成
                              → No: LLM 修正
    → No: 错误信息回传 LLM → LLM 修正代码 → 重新执行
```

### 5.3 错误处理与恢复

| 错误类型 | 原因 | 处理策略 |
|---------|------|---------|
| **API 调用错误** | 参数类型/范围不合法 | LLM 根据错误信息修正参数 |
| **几何冲突** | 面不存在、边不匹配 | LLM 先查询模型拓扑再操作 |
| **代码语法错误** | LLM 生成无效代码 | 语法检查 + few-shot 纠正 |
| **设计意图错误** | LLM 理解偏差 | 用户反馈修正 + 对话澄清 |
| **死循环** | Agent 反复失败 | 设置最大重试次数 + 人工介入 |

### 5.4 工具/MCP 封装层设计

将 CAD API 封装为 Agent 可调用的工具是工程核心。

**封装层次**:
```
Layer 4: Agent 工具接口 (JSON Schema, LLM 可理解)
Layer 3: 业务逻辑层 (参数校验、状态管理、错误处理)
Layer 2: API 适配层 (AutoCAD/Fusion/SolidWorks/FreeCAD 统一接口)
Layer 1: CAD 原生 API (AutoLISP/Python/COM/.NET)
```

**多 CAD 统一接口设计思路**:
```python
# 统一抽象层
class CADAgent:
    def create_sketch(self, plane, profile): ...
    def extrude(self, sketch_id, distance, operation): ...
    def create_hole(self, face_id, position, diameter, depth): ...
    def fillet(self, edge_ids, radius): ...
    def get_model_info(self): ...

# 各 CAD 适配器
class AutoCADAdapter(CADAgent): ...  # pyautocad / AutoLISP
class Fusion360Adapter(CADAgent): ...  # Fusion Python API
class SolidWorksAdapter(CADAgent): ...  # win32com COM
class FreeCADAdapter(CADAgent): ...  # FreeCAD Python API
```

**MCP Server 封装**:
```
MCP Server
  ├── Tools: create_sketch, extrude, hole, fillet, get_info, ...
  ├── Resources: 当前模型状态、特征树、参数表
  └── Prompts: CAD 建模最佳实践模板
```

---

## 六、技术路线对比与推荐

### 6.1 各路径综合对比

| 维度 | 脚本生成(A) | API工具调用(B) | 文件解析(C) | 多模态(D) | RAG(E) |
|------|------------|--------------|------------|----------|--------|
| **可控性** | ★★★ | ★★★★★ | ★★★★ | ★★ | ★★★ |
| **灵活性** | ★★★★ | ★★★★ | ★★★ | ★★★★★ | ★★★ |
| **实现难度** | ★★ (低) | ★★★★ (高) | ★★★ | ★★★ | ★★★★ |
| **延迟** | 低 | 中 | 高 | 中 | 中 |
| **错误恢复** | ★★ | ★★★★★ | ★★★ | ★★★ | ★★★ |
| **适用CAD** | 全部 | 有API的全部 | 全部 | 全部 | 全部 |
| **成熟度** | ★★★★ | ★★★ | ★★★★ | ★★ | ★★ |

### 6.2 按场景推荐技术组合

| 应用场景 | 推荐范式 | 推荐路径 | CAD软件 | 理由 |
|---------|---------|---------|---------|------|
| **标准件快速生成** | 端到端生成 | A(脚本) | OpenSCAD/FreeCAD | 参数固定，端到端效率高 |
| **复杂零件参数化建模** | Function Calling | B(API工具) + E(RAG标准件库) | Fusion 360/FreeCAD | 多步操作需精细控制 |
| **对话式概念设计** | 对话式迭代 | A(脚本) + D(渲染反馈) | FreeCAD/AutoCAD | 渐进式设计，可视化反馈 |
| **工程图纸信息提取** | 多模态理解 | D(VLM) + C(文件解析) | AutoCAD/SolidWorks | 图纸图像识别为主 |
| **设计规范合规检查** | RAG增强 | E(RAG规范库) + D(图纸理解) | 通用 | 规范检索+图纸对比 |
| **批量自动化建模** | ReAct | A(脚本) + B(API) | AutoCAD(Script) | 无交互，全自动闭环 |
| **CAD 模型语义检索** | RAG | E(嵌入+检索) | 通用 | 向量检索为主 |
| **教学/辅助设计** | 对话式 | A+B+D | FreeCAD/AutoCAD | 交互式引导 |

### 6.3 推荐技术栈（以构建通用 CAD Agent 为目标）

**最优组合**: ReAct/Function Calling 范式 + API工具调用(B) + 脚本生成(A) + 多模态反馈(D)

```
┌──────────────────────────────────────────────────────┐
│                  推荐架构                             │
│                                                      │
│  用户指令                                             │
│    ↓                                                 │
│  [LLM Agent (ReAct + Function Calling)]              │
│    ├── RAG 检索 (设计规范 + 标准件 + API文档)          │
│    ├── 工具调用层 (MCP Server / 统一CAD抽象层)         │
│    │     ├── create_sketch / extrude / hole / ...    │
│    │     ├── get_model_info (状态查询)                │
│    │     └── render_screenshot (渲染反馈)             │
│    ├── 代码生成 (Python 脚本作为补充)                  │
│    └── 多模态反馈 (VLM 验证渲染截图)                   │
│    ↓                                                 │
│  [CAD 适配层]                                        │
│    ├── AutoCAD (pyautocad / AutoLISP)                │
│    ├── Fusion 360 (Python API / APS)                 │
│    ├── SolidWorks (win32com COM)                     │
│    └── FreeCAD (Python API)                          │
│    ↓                                                 │
│  CAD 引擎执行 → 模型生成                              │
└──────────────────────────────────────────────────────┘
```

---

## 七、当前技术瓶颈与未来方向

### 7.1 当前瓶颈

| 瓶颈 | 描述 | 影响 |
|------|------|------|
| **几何推理能力不足** | LLM 难以精确推理3D空间关系（如面在哪、边如何连接） | 复杂建模需多次重试 |
| **API 文档覆盖** | LLM 对 CAD API 的掌握不够精确，调用参数常出错 | 代码生成成功率低 |
| **状态表示鸿沟** | CAD 几何状态→LLM 可理解文本的转换有信息损失 | Agent 难以准确感知模型状态 |
| **长程规划** | 复杂零件需数十步操作，LLM 长序列规划能力不足 | 超过 5-10 步后错误累积 |
| **标准化缺失** | 无统一的 CAD Agent 工具接口标准 | 各项目重复封装 |
| **评测缺失** | 缺乏统一、全面的 CAD Agent 评测基准 | 难以量化比较不同方案 |

### 7.2 未来方向预判

| 方向 | 预期发展 | 时间线 |
|------|---------|--------|
| **MCP 标准化** | CAD 操作工具通过 MCP 协议标准化，LLM 即插即用 | 2025 早期 |
| **CAD 专用 LLM** | 基于 CAD 数据微调的专用模型（如 CAD-MLLM 演进） | 2025 中期 |
| **3D 原生多模态** | LLM 直接理解 3D 几何（B-rep/点云），不依赖文本中转 | 2025-2026 |
| **RAG 工程化** | 标准件库/规范库/材料库的 RAG 基础设施成熟 | 2025 |
| **厂商开放平台** | Autodesk/SolidWorks 进一步开放 AI Agent API | 2025-2026 |
| **Agent-原生 CAD** | 为 AI Agent 交互设计的新一代 CAD 引擎 | 2026+ |

---

## 八、总结

1. **主流 Agent 架构收敛于 "Function Calling / ReAct + API工具调用 + 反馈循环"** 模式，端到端代码生成适合简单标准化场景，多 Agent 协作适合复杂场景但工程复杂度高。

2. **CAD 交互路径以"脚本生成执行"和"API工具调用"为主**，多模态视觉理解作为反馈机制日益重要，RAG 是增强设计知识的关键但成熟度最低。

3. **代码作为中间表示（Code-as-IR）是核心范式**：LLM 生成 Python/AutoLISP/VBA 代码 → CAD 引擎执行 → 结果反馈。Python 因其通用性和各 CAD 软件的支持度成为首选中间语言。

4. **工具粒度设计是工程关键**：中粒度工具（create_sketch/extrude/hole/fillet + get_model_info）在灵活性和可控性间取得最佳平衡。

5. **反馈闭环是从"玩具"到"可用"的分水岭**：无反馈的开环生成（如 Text2CAD）适合标准化场景；有反馈的闭环 Agent（如 3D-GPT/CAD-Agent）才能处理真实复杂设计任务。

6. **MCP 协议有望解决标准化问题**：将 CAD API 统一封装为 MCP Server，是降低 Agent 接入门槛的最有前景方向。

7. **当前最大瓶颈是 LLM 的几何推理能力和长程规划能力**，需通过 RAG（注入领域知识）、多模态（视觉反馈）、专用微调（CAD 数据）多管齐下解决。
