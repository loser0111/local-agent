# SolidWorks + local-agent 接入指南

> **目标**：在 local-agent 中通过自然语言对话驱动 SolidWorks 自动绘图

## 一、环境准备

### 1.1 前置条件

| 项目 | 要求 |
|------|------|
| 操作系统 | Windows 10/11 |
| SolidWorks | 2022+（本项目以 2024 为准） |
| Python | 3.9+ |
| pywin32 | `pip install pywin32` |
| local-agent | 已编译运行的 Wails 桌面应用 |

### 1.2 安装 Python 依赖

```powershell
# 打开 PowerShell
pip install pywin32
```

### 1.3 验证 SolidWorks COM 连接

```powershell
cd C:\path\to\local-agent\cad\solidworks-mcp
python sw_com_check.py
```

预期输出：
```
[OK] pythoncom 初始化成功
[OK] 成功连接 SldWorks.Application
[OK] SolidWorks 版本: ...
连接检查完成。
```

> **如果连接失败**：
> - 确认 SolidWorks 已安装且至少启动过一次
> - 尝试以管理员身份运行 `"C:\Program Files\SolidWorks Corp\SldWorks.exe" /regserver`
> - 确认 pywin32 已正确安装：`python -c "import win32com.client; print('OK')"`

---

## 二、文件结构

```
cad/solidworks-mcp/
├── sw_lib.py          # SolidWorks win32com 封装层（核心库）
├── sw_cli.py          # CLI 入口（被 local-agent 工具调用）
├── sw_com_check.py    # COM 连接检查脚本
├── tools-config.json  # local-agent 工具配置（复制到设置页）
├── README.md          # MCP Server 预留说明
└── SETUP.md           # 本文件
```

---

## 三、注册工具到 local-agent

### 3.1 修改路径

打开 `tools-config.json`，把所有 `C:\\path\\to\\cad\\solidworks-mcp\\sw_cli.py` 替换为你的实际路径。

例如，如果 local-agent 在 `D:\projects\local-agent`：

```json
"command": "python \"D:\\projects\\local-agent\\cad\\solidworks-mcp\\sw_cli.py\" '{\"action\":\"create_part\",\"template\":\"{{template}}\"}'"
```

### 3.2 在 local-agent 设置页注册工具

1. 打开 local-agent → 设置 → 工具管理
2. 逐条添加工具（或批量导入 JSON）

> **替代方案**：直接编辑 `~/.local-agent/tools.json`，在 `sources` 数组中加入 `tools-config.json` 里的条目。

### 3.3 注册的工具清单

| 工具名 | 功能 | 关键参数 |
|--------|------|----------|
| `sw_create_part` | 创建新零件 | template(mm/in) |
| `sw_sketch_rectangle` | 画矩形 | width, height (mm) |
| `sw_sketch_circle` | 画圆 | radius (mm) |
| `sw_extrude` | 拉伸 | depth (mm) |
| `sw_extrude_cut` | 切除拉伸 | depth (mm) |
| `sw_save` | 保存文档 | path |
| `sw_get_info` | 获取文档信息 | — |
| `sw_create_flange` | 一步创建法兰盘 | outer_radius, inner_radius, bolt_count, ... |

---

## 四、使用示例

### 4.1 基础操作流程

在 local-agent 对话中输入：

```
帮我画一个 100×60×20 的长方体零件

```

Agent 会依次调用：
1. `sw_create_part` → 创建新零件
2. `sw_sketch_rectangle(width=100, height=60)` → 画矩形草图
3. `sw_extrude(depth=20)` → 拉伸成实体
4. `sw_save(path="C:\...\box.SLDPRT")` → 保存

### 4.2 法兰盘（一步完成）

```
帮我画一个法兰盘，外径100mm，内径30mm，6个螺栓孔，厚度10mm

```

Agent 调用：
```
sw_create_flange(outer_radius=50, inner_radius=15, bolt_count=6, thickness=10)
```

### 4.3 分步操作（更灵活）

```
先创建一个新零件，画一个半径25的圆，拉伸10mm，然后保存到桌面叫disc.SLDPRT
```

---

## 五、工作原理

```
用户对话
    ↓
local-agent (LLM Function Calling)
    ↓
CLI 工具 (命令模板 + {{参数}} 替换)
    ↓
sw_cli.py (Python 入口，解析 JSON 参数)
    ↓
sw_lib.py (SWSession 类，封装 COM 调用)
    ↓
win32com.client.Dispatch("SldWorks.Application")
    ↓
SolidWorks COM Automation API
    ↓
SolidWorks 执行建模操作
```

### 关键设计点

1. **单位处理**：用户输入毫米，`sw_cli.py` 自动转为米（SolidWorks API 原生单位）
2. **JSON 通信**：所有操作接收 JSON 参数、返回 JSON 结果，便于 LLM 解析
3. **单例连接**：`SWConnection` 复用同一个 COM 连接，避免重复启动 SolidWorks
4. **复合操作**：`create_flange` 等高级操作封装多步调用，减少 LLM 轮次

---

## 六、直接测试（不通过 Agent）

可以跳过 local-agent，直接在命令行测试：

```powershell
# 创建零件
python sw_cli.py "{\"action\":\"create_part\"}"

# 画圆（半径 25mm）
python sw_cli.py "{\"action\":\"sketch_circle\",\"radius\":25,\"unit\":\"mm\"}"

# 拉伸 10mm
python sw_cli.py "{\"action\":\"extrude\",\"depth\":10,\"unit\":\"mm\"}"

# 保存
python sw_cli.py "{\"action\":\"save\",\"path\":\"C:\\Users\\Desktop\\test.SLDPRT\"}"

# 创建法兰盘
python sw_cli.py "{\"action\":\"create_flange\",\"outer_radius\":50,\"inner_radius\":15,\"bolt_count\":6,\"thickness\":10}"

# 查看文档信息
python sw_cli.py "{\"action\":\"get_info\"}"
```

---

## 七、扩展操作

### 7.1 在 sw_lib.py 中添加新操作

```python
# sw_lib.py 中添加方法
class SWSession:
    def my_new_operation(self, param1: float) -> str:
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))
        # ... COM 调用 ...
        return _ok("操作成功", {"param1": param1})
```

### 7.2 在 sw_cli.py 中注册分发

```python
# sw_cli.py 的 dispatch() 函数中添加
if action == "my_new_operation":
    return session.my_new_operation(args.get("param1"))
```

### 7.3 在 tools-config.json 中注册工具

```json
{
  "id": "sw_my_op",
  "name": "sw_my_op",
  "label": "我的操作",
  "description": "做某件事。参数: param1(值mm)。",
  "kind": "cli",
  "enabled": true,
  "exposure": "direct",
  "parameters": [
    {"name": "param1", "description": "参数1(毫米)", "required": true}
  ],
  "cli": {
    "command": "python \"C:\\path\\to\\sw_cli.py\" '{\"action\":\"my_new_operation\",\"param1\":{{param1}},\"unit\":\"mm\"}'",
    "timeout": 30
  }
}
```

---

## 八、后续路线

| 阶段 | 内容 | 状态 |
|------|------|------|
| **阶段 1** | CLI 工具 + Python win32com 封装 | ✅ 本次完成 |
| **阶段 2** | 升级为 MCP Server（精细工具 + 状态查询 + 截图） | 📋 计划中 |
| **阶段 3** | 齿轮专用工具（渐开线齿廓生成器） | 📋 计划中 |
| **阶段 4** | VLM 截图验证（渲染截图 → LLM 看图 → 修正） | 📋 计划中 |

### 阶段 2 预览：MCP Server

将 `sw_lib.py` 包装为 MCP Server，好处：
- 工具粒度更精细（每个操作独立注册）
- 支持状态查询（模型结构 / 特征树 / 尺寸）
- 支持截图反馈（渲染当前视图 → 返回图片 → LLM 验证）
- 标准化协议，可在 Claude Desktop / 其他 Agent 中复用

---

## 九、常见问题

### Q: PowerShell 转义引号很麻烦

A: `tools-config.json` 里的命令模板已经处理好了转义。直接复制即可。
如果手动测试，建议用单引号包裹 JSON：

```powershell
python sw_cli.py '{"action":"create_part"}'
```

### Q: SolidWorks 没有自动打开

A: COM 调用会自动启动 SolidWorks 后台进程。设置 `swApp.Visible = True` 可显示界面。
在 `sw_lib.py` 的 `SWConnection.get()` 中已默认获取实例，如果需要显示界面，在调用后添加：

```python
swApp = SWConnection.get()
swApp.Visible = True
```

### Q: 工具执行超时

A: 编辑 `tools-config.json`，增大 `timeout` 值（单位：秒）。SolidWorks 首次启动可能较慢。

### Q: 如何查看 SolidWorks API 文档

A: SolidWorks API Help 随软件安装。路径通常为：
`C:\Program Files\SolidWorks Corp\SolidWorks\api\apihelp.chm`
也可以访问在线版：`https://help.solidworks.com/`
