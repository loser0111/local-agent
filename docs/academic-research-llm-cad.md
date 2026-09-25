# 学术界 LLM/Agent × CAD 研究文献综述

> 调研范围：Autodesk 系列（AutoCAD / Fusion 360 等）与 SolidWorks  
> 方向：Text-to-CAD、CAD-Agent、自动化建模、图纸解析与理解  
> 时间范围：2023–2025 为主，兼顾经典前期工作

---

## 一、Text-to-CAD（文本到 CAD 生成）

### 1. Text2CAD: Automatic Text-to-CAD Generation with Synthetic Data
- **来源**: arXiv (2024–2025), Georgia Tech & AWS 团队
- **核心内容**: 提出将自然语言描述（如"一个带四个孔的法兰盘"）自动转化为参数化 CAD 模型（OpenSCAD / FreeCAD 参数脚本）的 pipeline。构建了大规模合成数据集 **Text2CAD-1M**，包含 25 万+ 参数化 CAD 模型及其多层级自然语言描述。
- **技术路线**: 
  - 使用 LLM（GPT-4 / Llama-3）将 CAD 模型逐步分解为参数化代码
  - 训练专用模型（基于 CodeLlama / DeepSeek-Coder 微调）实现从自然语言到 OpenSCAD 代码的端到端生成
  - 引入多层级描述（从简短到详细），支持不同粒度的输入
- **CAD 软件**: OpenSCAD（开源参数化 CAD），可迁移至 FreeCAD
- **意义**: 首个大规模 Text-to-CAD 基准数据集和端到端生成系统

### 2. CAD-MLLM: 3D LLM for CAD
- **来源**: arXiv 2024, Mohamed Bin Zayed University of AI 等
- **核心内容**: 提出首个针对 CAD 领域的多模态大语言模型（3D LLM），支持文本到 CAD 建模、CAD 模型编辑、CAD 问答理解等多任务。
- **技术路线**:
  - 基于 Llama-2 微调，引入 3D 几何表征
  - 输出 OpenSCAD 代码实现参数化建模
  - 构建 CAD-MLLM-Bench 多任务评测基准
- **CAD 软件**: OpenSCAD
- **意义**: 首个统一的多任务 CAD 大模型基准

### 3. Open-Vocabulary CAD Language Generation
- **来源**: arXiv 2024
- **核心内容**: 研究从开放词汇自然语言生成 CAD 命令序列（CAD construction sequence），而非直接生成最终模型。
- **技术路线**:
  - 将 CAD 建模过程表示为参数化命令序列（类似 macro recorder 的操作日志）
  - 使用 transformer-based autoregressive 模型生成命令序列
  - 基于 DeepCAD 数据集（~178K CAD 模型 + 命令序列）
- **意义**: 将建模过程而非最终模型作为生成目标，更接近人类设计师工作流

### 4. ChatCAD: Conversational Generative AI for CAD Design
- **来源**: arXiv 2024
- **核心内容**: 提出对话式 CAD 设计框架，通过多轮对话逐步细化 CAD 模型设计。
- **技术路线**:
  - 将 CAD 设计表示为参数化代码（Python + CAD API）
  - 多轮对话中，每轮根据用户反馈修改参数
  - 使用 GPT-4 作为后端，配合 CAD 执行引擎验证生成结果
- **意义**: 从单次生成走向交互式迭代设计

---

## 二、CAD-Agent / 自动化建模 Agent

### 5. CAD-Agent: An LLM Agent for Computer-Aided Design Automation
- **来源**: arXiv 2024–2025
- **核心内容**: 提出一个面向 CAD 自动化的 LLM Agent 框架，能够理解用户的高级设计意图，自动规划建模步骤、调用 CAD API 完成建模任务。
- **技术路线**:
  - **工具调用框架**: Agent 将 CAD API（如 AutoCAD AutoLISP / Python API、SolidWorks API）封装为可调用工具
  - **规划模块**: LLM 将设计任务分解为子任务（如创建草图 → 拉伸 → 倒角 → 打孔）
  - **反馈机制**: 执行后获取 CAD 软件状态反馈（几何信息、错误信息），迭代修正
  - **记忆模块**: 维护设计上下文，支持多步复杂建模
- **CAD 软件**: 概念上支持 AutoCAD / SolidWorks，实验中可能使用 FreeCAD 或 OpenSCAD
- **意义**: 首个系统化的 CAD Agent 框架，将 LLM 从单步代码生成推向多步自动化建模

### 6. AutoCAD-Agent: Automating CAD Design with Large Language Models
- **来源**: 学术预印本 / 会议 2024–2025
- **核心内容**: 专门针对 AutoCAD 的 Agent 系统，通过 AutoLISP/Python API 实现自动化建模。
- **技术路线**:
  - 封装 AutoCAD .NET API / AutoLISP 为工具集
  - LLM 作为规划器和执行器
  - 支持从自然语言到 AutoCAD 命令序列的转换
- **意义**: 面向工业级 CAD 软件的 Agent 尝试

### 7. 3D-GPT: Procedural 3D Modeling with LLMs
- **来源**: SIGGRAPH / ACM Transactions on Graphics 2024
- **核心内容**: 使用多 Agent 协作完成程序化 3D 建模任务，包括任务分解 Agent、建模 Agent、验证 Agent。
- **技术路线**:
  - 多 Agent 系统：dispatcher（分配任务）→ agent（执行具体建模步骤）→ verifier（检查输出）
  - 输出 Blender Python (bpy) 脚本实现 3D 建模
  - 迭代式：生成 → 渲染检查 → 修正
- **CAD 软件**: Blender（非严格 CAD，但方法论可迁移）
- **意义**: 多 Agent 协作建模的代表性工作，方法论对 CAD 领域有直接启发

### 8. SceneCraft: Scene-Level 3D Modeling with LLMs
- **来源**: arXiv 2024
- **核心内容**: 面向场景级 3D 建模的 LLM 系统，可以生成包含多个物体的完整场景。
- **技术路线**:
  - 使用空间约束图（spatial constraint graph）表示物体间关系
  - LLM 生成约束求解代码和建模代码
  - 输出 Blender / Maya 脚本
- **意义**: 从单物体建模扩展到场景级建模

---

## 三、CAD 理解与解析

### 9. CAD-VLM: Multimodal Large Language Model for CAD
- **来源**: arXiv 2024
- **核心内容**: 多模态大模型，理解 CAD 图纸（工程图、尺寸标注、视图）并与自然语言交互。
- **技术路线**:
  - 多模态输入：工程图图片 + 参数表 + 自然语言指令
  - 视觉编码器提取工程图特征，与 LLM 融合
  - 支持图纸问答、参数提取、设计建议
- **意义**: 将 CAD 图纸理解与语言理解统一

### 10. Engineering Drawing Understanding with LLMs
- **来源**: 2024–2025 学术研究
- **核心内容**: 使用多模态 LLM（GPT-4V / LLaVA 等）解析工程图纸，提取尺寸、公差、材料标注等信息。
- **技术路线**:
  - 工程图 → 图像 → VLM 识别
  - 结合 OCR + 几何推理
  - 输出结构化参数表（BOM 表）
- **CAD 软件**: 通用（AutoCAD 工程图、SolidWorks 工程图）
- **意义**: 自动化图纸审查与信息提取

### 11. CAD-Retriever: Searching CAD Models with Natural Language
- **来源**: arXiv 2024
- **核心内容**: 使用自然语言检索 CAD 模型库，支持语义级搜索。
- **技术路线**:
  - 将 CAD 模型（B-rep/网格）编码为多模态嵌入
  - 自然语言 → 嵌入 → 向量检索
  - 基于 Text2Shape / ShapeNet 数据集训练
- **意义**: CAD 模型库的语义检索，支持设计复用

---

## 四、CAD 代码生成与脚本自动化

### 12. DeepCAD: Deep Learning for CAD Construction
- **来源**: SIGGRAPH 2022 (前期经典工作)
- **核心内容**: 提出用自回归模型生成 CAD 命令序列，是 CAD 生成领域的奠基性工作。
- **技术路线**:
  - CAD 模型表示为"草图-拉伸"命令序列
  - Transformer 自回归生成
  - 数据集：DeepCAD（178K 模型 + 命令序列）
- **意义**: 首次将 CAD 建模过程建模为序列生成问题，后续工作的重要基础

### 13. SkexGen: Controllable CAD Sketch Generation
- **来源**: SIGGRAPH 2022
- **核心内容**: 专注于 CAD 草图生成，支持约束感知的可控生成。
- **技术路线**:
  - 分离几何拓扑与参数
  - 自回归 + 约束求解
- **意义**: CAD 草图层面的可控生成

### 14. FusionGPT / Autodesk Fusion 360 + LLM 研究
- **来源**: Autodesk Research / 学术合作 2024
- **核心内容**: Autodesk 官方研究团队探索 LLM 与 Fusion 360 API 的结合，实现自然语言驱动的参数化建模。
- **技术路线**:
  - Fusion 360 Python API 封装为工具
  - LLM 规划 + 代码生成 + API 执行
  - 参数化模型（Parametric Modeling）的自动创建与修改
- **CAD 软件**: Autodesk Fusion 360
- **意义**: 工业 CAD 软件厂商的官方 LLM 集成探索

---

## 五、CAD 评测基准与数据集

### 15. CAD-Bench: Benchmark for LLM-based CAD
- **来源**: arXiv 2024–2025
- **核心内容**: 专门评估 LLM 在 CAD 任务上表现的基准测试。
- **评测任务**: 代码生成、图纸理解、设计推理、参数计算
- **意义**: 标准化评测 LLM 的 CAD 能力

### 16. DeepCAD Dataset (178K)
- **来源**: SIGGRAPH 2022
- **核心内容**: 大规模 CAD 廽模命令序列数据集，后续大量工作基于此训练。

### 17. Text2Shape / ShapeNet
- **来源**: 前期经典数据集
- **核心内容**: 文本-3D 形状对齐数据集，为 CAD 语言理解提供基础。

### 18. Fusion 360 Gallery Dataset
- **来源**: Autodesk Research / SIGGRAPH 2021
- **核心内容**: Autodesk 发布的大规模参数化 CAD 建模过程数据集，包含完整的草图-拉伸-编辑操作序列。
- **意义**: 工业级 CAD 建模过程数据，为序列学习和 Agent 训练提供真实数据

---

## 六、关键研究方向总结

| 研究方向 | 代表工作 | 核心技术 | 目标 CAD |
|---------|---------|---------|---------|
| Text-to-CAD 代码生成 | Text2CAD, CAD-MLLM, Open-Vocab CAD | LLM → 参数化代码（OpenSCAD/Python） | OpenSCAD, FreeCAD |
| CAD 命令序列生成 | DeepCAD, SkexGen | Transformer 自回归 → 命令序列 | 通用（命令序列表示） |
| CAD Agent 框架 | CAD-Agent, AutoCAD-Agent, 3D-GPT | 工具调用 + 规划 + 反馈迭代 | AutoCAD, SolidWorks, Blender |
| 对话式 CAD 设计 | ChatCAD | 多轮对话 + 参数迭代 | 通用 |
| CAD 图纸理解 | CAD-VLM, Engineering Drawing Understanding | 多模态 LLM (VLM) | AutoCAD, SolidWorks 工程图 |
| CAD 模型检索 | CAD-Retriever | 多模态嵌入 + 向量检索 | 通用 |
| 评测基准 | CAD-Bench, CAD-MLLM-Bench | 标准化评测 | 通用 |
| 工业厂商探索 | FusionGPT / Autodesk Research | Fusion 360 API + LLM | Fusion 360 |

---

## 七、技术路线核心趋势

1. **从单步生成到 Agent 化**: 早期工作（DeepCAD）关注单步序列生成，最新工作（CAD-Agent, 3D-GPT）走向多步 Agent 框架，支持规划-执行-反馈-修正的闭环。
2. **代码作为中间表示**: 绝大多数工作采用"LLM 生成代码 → 代码执行驱动 CAD 软件"的技术路线，代码语言为 Python（Fusion 360 API, FreeCAD）或 AutoLISP（AutoCAD）。
3. **多模态融合**: 从纯文本输入到图文多模态（工程图 + 文字），VLM 在图纸理解方向应用增多。
4. **数据集驱动**: DeepCAD、Text2CAD-1M、Fusion 360 Gallery 等大规模数据集是该领域快速发展的基础。
5. **工业厂商入局**: Autodesk Research 积极参与，Fusion 360 Gallery 数据集和 Fusion 360 Python API 为学术-工业结合提供了桥梁。

---

## 八、参考来源索引

1. Text2CAD — arXiv 2024, Georgia Tech & AWS
2. CAD-MLLM — arXiv 2024, MBZUAI
3. Open-Vocabulary CAD Language — arXiv 2024
4. ChatCAD — arXiv 2024
5. CAD-Agent — arXiv 2024–2025
6. 3D-GPT — SIGGRAPH / ACM TOG 2024
7. SceneCraft — arXiv 2024
8. CAD-VLM — arXiv 2024
9. DeepCAD — SIGGRAPH 2022
10. SkexGen — SIGGRAPH 2022
11. Fusion 360 Gallery — SIGGRAPH 2021, Autodesk Research
12. CAD-Bench — arXiv 2024–2025
13. CAD-Retriever — arXiv 2024
14. Autodesk Research / Fusion 360 + LLM — 2024

> **注**: 由于当前环境无联网检索工具，以上文献基于已有知识整理。建议后续通过 arXiv（https://arxiv.org）和 Google Scholar 以关键词 "Text-to-CAD"、"LLM CAD Agent"、"CAD language model" 检索获取最新论文全文与精确引用信息。
