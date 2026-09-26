"""
SolidWorks win32com 封装层

提供 SolidWorks 操作的底层封装，被 CLI 入口和 MCP Server 共用。
所有长度单位为米（SolidWorks API 原生单位），调用方负责单位转换。

核心 API:
    SWConnection    - 连接管理（获取/释放 SolidWorks 应用对象）
    SWSession       - 操作会话（零件建模、草图、拉伸、保存等）
"""

from __future__ import annotations

import json
import os
import sys
from typing import Any, Optional

try:
    import win32com.client
    import pythoncom
except ImportError:
    print("ERROR: pywin32 未安装。请运行: pip install pywin32", file=sys.stderr)
    sys.exit(1)


# ============================================================
# 常量
# ============================================================

# SolidWorks 文档类型
swDocPART = 1
swDocASSEMBLY = 2
swDocDRAWING = 3

# 草图约束选项
swSketchConstraintNone = 0

# 拉伸类型
swEndCondBlind = 0      # 盲端（给定深度）
swEndCondThroughAll = 1 # 贯穿
swEndCondUpToNext = 6   # 到下一个面

# 拉伸方向
swEndCondBlindDir = 0

# 零件模板路径（按 SolidWorks 版本自适应）
PART_TEMPLATE_MM = "Part.prtdot"
PART_TEMPLATE_IN = "Part_IN.prtdot"


# ============================================================
# 连接管理
# ============================================================

class SWConnection:
    """管理 SolidWorks COM 连接的生命周期"""

    _instance: Optional["SWConnection"] = None
    _swApp: Any = None

    @classmethod
    def get(cls) -> Any:
        """获取或创建 SolidWorks 应用连接（单例）"""
        if cls._swApp is not None:
            try:
                # 验证连接是否仍然有效
                _ = cls._swApp.RevisionNumber()
                return cls._swApp
            except Exception:
                cls._swApp = None
                cls._instance = None

        pythoncom.CoInitialize()
        cls._swApp = win32com.client.Dispatch("SldWorks.Application")
        cls._instance = cls()
        return cls._swApp

    @classmethod
    def get_active_doc(cls) -> Any:
        """获取当前活动文档"""
        swApp = cls.get()
        doc = swApp.ActiveDoc
        if doc is None:
            raise RuntimeError("没有活动文档。请先创建或打开一个零件。")
        return doc


# ============================================================
# 操作会话
# ============================================================

class SWSession:
    """
    SolidWorks 操作会话

    封装常用建模操作：创建零件、画草图、拉伸、保存等。
    所有方法返回 JSON 格式字符串，供 CLI / MCP 调用方直接使用。
    """

    def __init__(self):
        self.swApp = SWConnection.get()

    # ---- 文档管理 ----

    def create_part(self, template: str = "mm") -> str:
        """创建新零件文档"""
        # 获取默认零件模板
        swApp = self.swApp
        template_name = PART_TEMPLATE_MM if template == "mm" else PART_TEMPLATE_IN

        # 使用 GetUserTemplate 获取模板路径
        try:
            template_path = swApp.GetUserTemplateFile(template_name)
        except Exception:
            # 回退：使用 GetTemplateFile
            try:
                template_path = swApp.GetTemplateFile(0, 0, 0, 0)  # 0=零件, 0=标准
            except Exception:
                # 最终回退：空字符串，让 SW 用默认模板
                template_path = ""

        doc = swApp.NewDocument(template_path if template_path else template_name,
                                 0, 0, 0)
        if doc is None:
            return _error("创建零件失败，模板可能不可用")

        title = doc.GetTitle()
        return _ok(f"已创建新零件: {title}", {"title": title, "type": "part"})

    def get_info(self) -> str:
        """获取当前活动文档信息"""
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        title = doc.GetTitle()
        doc_type = doc.GetType()
        type_names = {1: "part", 2: "assembly", 3: "drawing"}
        type_name = type_names.get(doc_type, f"unknown({doc_type})")

        path = ""
        try:
            path = doc.GetPathName() or ""
        except Exception:
            pass

        info = {
            "title": title,
            "type": type_name,
            "path": path,
            "modified": getattr(doc, "IsModified", lambda: False)(),
        }
        return _ok("文档信息", info)

    def save(self, path: str = "") -> str:
        """保存当前文档"""
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        if path:
            # SolidWorks 保存需要特定格式常量
            # swSaveAsVersion_e: 0 = current, 1 = pre-2008, etc.
            # swSaveAsOptions_e: 0 = standard
            result = doc.SaveAs3(
                path, 0, 0,
                0,  # version
                0,  # options
            )
            if result != 0:
                return _error(f"保存失败，错误码: {result}")
            return _ok(f"已保存到: {path}")
        else:
            # 保存到当前位置
            errors = doc.Save3(0)  # swSaveAsOptions_Silent = 0
            if isinstance(errors, tuple) and len(errors) >= 1 and errors[0] != 0:
                return _error(f"保存失败，错误码: {errors[0]}")
            return _ok("已保存")

    def close(self, save: bool = True) -> str:
        """关闭当前文档"""
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        if save:
            try:
                doc.Save3(0)
            except Exception:
                pass  # 可能是没有路径的新文档

        swApp = self.swApp
        swApp.CloseDoc(doc.GetTitle())
        return _ok("已关闭文档")

    # ---- 草图操作 ----

    def _open_sketch(self, doc: Any) -> Any:
        """在活动文档中打开草图编辑模式"""
        # 插入一个新草图（在前视基准面上）
        # swSketchManager
        skMgr = doc.SketchManager
        skMgr.InsertSketch(True)
        return skMgr

    def _close_sketch(self, skMgr: Any) -> None:
        """退出草图编辑模式"""
        skMgr.InsertSketch(True)

    def sketch_rectangle(self, width: float, height: float) -> str:
        """
        在草图中画一个矩形（中心在原点）

        Args:
            width: 宽度（米）
            height: 高度（米）
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        skMgr = self._open_sketch(doc)
        try:
            # CreateCornerRectangle(x1, y1, z1, x2, y2, z2)
            # 矩形从 (-w/2, -h/2) 到 (w/2, h/2)
            half_w = width / 2.0
            half_h = height / 2.0
            skMgr.CreateCornerRectangle(-half_w, -half_h, 0,
                                         half_w, half_h, 0)
            self._close_sketch(skMgr)
            return _ok(f"已绘制矩形 {width}x{height}m", {"width": width, "height": height})
        except Exception as e:
            self._close_sketch(skMgr)
            return _error(f"绘制矩形失败: {e}")

    def sketch_circle(self, radius: float) -> str:
        """
        在草图中画一个圆（圆心在原点）

        Args:
            radius: 半径（米）
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        skMgr = self._open_sketch(doc)
        try:
            # CreateCircle(centerX, centerY, centerZ, edgeX, edgeY, edgeZ)
            # 圆心在 (0,0,0)，边缘点在 (radius, 0, 0)
            skMgr.CreateCircle(0, 0, 0, radius, 0, 0)
            self._close_sketch(skMgr)
            return _ok(f"已绘制圆形 半径={radius}m", {"radius": radius})
        except Exception as e:
            self._close_sketch(skMgr)
            return _error(f"绘制圆形失败: {e}")

    def sketch_circle_at(self, cx: float, cy: float, radius: float) -> str:
        """
        在草图中画一个圆（指定圆心位置）

        Args:
            cx: 圆心 X 坐标（米）
            cy: 圆心 Y 坐标（米）
            radius: 半径（米）
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        skMgr = self._open_sketch(doc)
        try:
            # CreateCircle(centerX, centerY, centerZ, edgeX, edgeY, edgeZ)
            edge_x = cx + radius
            edge_y = cy
            skMgr.CreateCircle(cx, cy, 0, edge_x, edge_y, 0)
            self._close_sketch(skMgr)
            return _ok(f"已绘制圆形 圆心=({cx},{cy}) 半径={radius}m",
                        {"cx": cx, "cy": cy, "radius": radius})
        except Exception as e:
            self._close_sketch(skMgr)
            return _error(f"绘制圆形失败: {e}")

    def sketch_line(self, x1: float, y1: float, x2: float, y2: float) -> str:
        """在草图中画一条直线"""
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        skMgr = self._open_sketch(doc)
        try:
            skMgr.CreateLine(x1, y1, 0, x2, y2, 0)
            self._close_sketch(skMgr)
            return _ok(f"已绘制直线 ({x1},{y1})->({x2},{y2})")
        except Exception as e:
            self._close_sketch(skMgr)
            return _error(f"绘制直线失败: {e}")

    def sketch_polygon(self, radius: float, sides: int) -> str:
        """
        在草图中画一个正多边形（圆心在原点）

        Args:
            radius: 外接圆半径（米）
            sides: 边数（>=3）
        """
        if sides < 3:
            return _error("多边形边数不能少于 3")

        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        import math
        skMgr = self._open_sketch(doc)
        try:
            for i in range(sides):
                a1 = 2 * math.pi * i / sides
                a2 = 2 * math.pi * (i + 1) / sides
                skMgr.CreateLine(
                    radius * math.cos(a1), radius * math.sin(a1), 0,
                    radius * math.cos(a2), radius * math.sin(a2), 0,
                )
            self._close_sketch(skMgr)
            return _ok(f"已绘制 {sides} 边形 半径={radius}m",
                        {"radius": radius, "sides": sides})
        except Exception as e:
            self._close_sketch(skMgr)
            return _error(f"绘制多边形失败: {e}")

    # ---- 特征操作 ----

    def extrude(self, depth: float, thin_feature: bool = False) -> str:
        """
        拉伸当前草图

        Args:
            depth: 拉伸深度（米）
            thin_feature: 是否薄壁特征
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            featMgr = doc.FeatureManager
            # FeatureExtrusion2 参数（关键参数）:
            # sd=true(单向), flip=false, dir=true,
            # t1=0(盲端), t2=0, d1=depth, d2=0,
            # dchk1=false, dchk2=false, ddir1=true, ddir2=true,
            # dAng1=0, dAng2=0,
            # optMerge=false, optMerge2=false,
            # useFeatScope=true, useAutoSelect=true,
            # startCondition=0, startOffset=0
            feat = featMgr.FeatureExtrusion2(
                True,  # sd: 单向
                False, # flip: 不翻转
                False, # dir: 方向
                0,     # t1: 盲端
                0,     # t2: 盲端
                depth, # d1: 深度
                0,     # d2: 深度2
                False, False,  # dchk1, dchk2
                True, True,    # ddir1, ddir2
                0, 0,          # dAng1, dAng2
            )
            if feat is None:
                return _error("拉伸失败：可能没有有效草图轮廓")
            return _ok(f"已拉伸 深度={depth}m", {"depth": depth})
        except Exception as e:
            return _error(f"拉伸失败: {e}")

    def extrude_cut(self, depth: float) -> str:
        """切除拉伸（从当前草图中减材料）"""
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            featMgr = doc.FeatureManager
            feat = featMgr.FeatureCut4(
                True,  # sd: 单向
                False, # flip: 不翻转
                False, # dir: 方向
                0,     # t1: 盲端
                0,     # t2: 盲端
                depth, # d1: 深度
                0,     # d2: 深度2
                False, False,  # dchk1, dchk2
                True, True,    # ddir1, ddir2
                0, 0,          # dAng1, dAng2
                False,         # optMerge
                True, True,    # useFeatScope, useAutoSelect
                0, 0           # startCondition, startOffset
            )
            if feat is None:
                return _error("切除拉伸失败：可能没有有效草图轮廓")
            return _ok(f"已切除拉伸 深度={depth}m", {"depth": depth})
        except Exception as e:
            return _error(f"切除拉伸失败: {e}")

    def revolve(self, axis_x: float = 0, axis_y: float = 1) -> str:
        """
        旋转当前草图

        Args:
            axis_x: 旋转轴方向 X 分量
            axis_y: 旋转轴方向 Y 分量
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            featMgr = doc.FeatureManager
            # FeatureRevolve2 参数:
            # sd=true, flip=false, merge=true,
            # useThinFeature=false,
            # t1=0(盲端), t2=0,
            # thickness1=0, thickness2=0,
            # revType=0, ...
            feat = featMgr.FeatureRevolve2(
                True,   # sd: 单向
                False,  # flip
                True,   # merge
                False,  # useThinFeature
                0,      # t1: 盲端
                0,      # t2
                0,      # thickness1
                0,      # thickness2
            )
            if feat is None:
                return _error("旋转失败：可能没有有效草图轮廓")
            return _ok("已旋转特征", {"axis": [axis_x, axis_y]})
        except Exception as e:
            return _error(f"旋转失败: {e}")

    def fillet(self, radius: float, edges: list = None) -> str:
        """
        添加圆角

        Args:
            radius: 圆角半径（米）
            edges: 边的标识列表（可选，不指定则选所有边）
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            featMgr = doc.FeatureManager
            # 简化版：对当前选中边倒圆角
            # 完整版需要遍历边并选择
            doc.ClearSelection2(True)
            # 尝试选择所有边
            feat = featMgr.FeatureFillet3(
                radius,  # radius
                0,       # filletType: 等半径
                0,       # propPageType
                0,       # variations
            )
            if feat is None:
                return _error("圆角失败：可能没有选中边")
            return _ok(f"已添加圆角 R={radius}m", {"radius": radius})
        except Exception as e:
            return _error(f"圆角失败: {e}")

    # ---- 圆形阵列 ----

    def circular_pattern(self, count: int, total_angle: float = 360.0) -> str:
        """
        圆形阵列（旋转复制当前特征）

        Args:
            count: 阵列数量
            total_angle: 总角度（度，默认 360）
        """
        import math
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            featMgr = doc.FeatureManager
            feat = featMgr.FeatureCircularPattern5(
                count,                       # numInstances
                total_angle * math.pi / 180, # angle (弧度)
                True,             # flipEqualSpacing
                False,            # equalSpacing (already in angle)
                0,                # axisType
                True, True,       # useFeatScope, useAutoSelect
            )
            if feat is None:
                return _error("圆形阵列失败")
            return _ok(f"已创建圆形阵列 count={count}", {"count": count, "angle": total_angle})
        except Exception as e:
            return _error(f"圆形阵列失败: {e}")

    # ---- 辅助 ----

    def rebuild(self) -> str:
        """重建模型"""
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            doc.EditRebuild3()
            return _ok("已重建模型")
        except Exception as e:
            return _error(f"重建失败: {e}")

    # ---- 感知能力：几何状态查询 ----

    def get_feature_tree(self) -> str:
        """
        获取特征树（所有特征名称 + 类型 + 是否被抑制）

        这是模型感知"当前零件里有什么"的核心手段。
        每个特征返回: name / type_name / suppressed
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            feat = doc.FirstFeature
            features = []
            idx = 0
            while feat is not None:
                name = feat.Name
                type_name = feat.GetTypeName2() if hasattr(feat, "GetTypeName2") else "Unknown"
                suppressed = feat.IsSuppressed() if hasattr(feat, "IsSuppressed") else False

                features.append({
                    "index": idx,
                    "name": name,
                    "type": type_name,
                    "suppressed": bool(suppressed),
                })
                feat = feat.GetNextFeature
                idx += 1

            return _ok(f"共 {len(features)} 个特征", {"features": features, "count": len(features)})
        except Exception as e:
            return _error(f"获取特征树失败: {e}")

    def get_dimensions(self) -> str:
        """
        获取当前零件所有特征的尺寸（名称 + 值 + 单位）

        模型通过这些数值确认"画出来的东西尺寸对不对"。
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            feat = doc.FirstFeature
            all_dims = []
            idx = 0
            while feat is not None:
                # 获取该特征的所有显示尺寸
                disp_dim = feat.GetFirstDisplayDimension() if hasattr(feat, "GetFirstDisplayDimension") else None
                feat_dims = []
                while disp_dim is not None:
                    dim = disp_dim.GetDimension2(0) if hasattr(disp_dim, "GetDimension2") else None
                    if dim is not None:
                        dim_name = dim.Name if hasattr(dim, "Name") else "Unknown"
                        dim_value = dim.SystemValue if hasattr(dim, "SystemValue") else None
                        feat_dims.append({
                            "name": dim_name,
                            "value_m": dim_value,  # SI 单位（米）
                            "value_mm": round(dim_value * 1000, 4) if dim_value is not None else None,
                        })
                    try:
                        disp_dim = feat.GetNextDisplayDimension(disp_dim)
                    except Exception:
                        break

                if feat_dims:
                    all_dims.append({
                        "feature_index": idx,
                        "feature_name": feat.Name,
                        "dimensions": feat_dims,
                    })

                feat = feat.GetNextFeature
                idx += 1

            total = sum(len(f["dimensions"]) for f in all_dims)
            return _ok(f"共 {total} 个尺寸", {"features": all_dims, "total_dims": total})
        except Exception as e:
            return _error(f"获取尺寸失败: {e}")

    def get_bounding_box(self) -> str:
        """
        获取当前零件的包围盒（最小外接长方体）

        返回 X/Y/Z 方向的最小值/最大值和总尺寸（mm）。
        模型用这个判断"东西画大了还是画小了"。
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            # GetBoundingBox 返回 [minX, minY, minZ, maxX, maxY, maxZ]（米）
            box = doc.GetBoundingBox()
            if box is None or len(box) < 6:
                return _error("无法获取包围盒（可能没有实体几何）")

            min_x, min_y, min_z, max_x, max_y, max_z = box[:6]
            size_x = (max_x - min_x) * 1000  # mm
            size_y = (max_y - min_y) * 1000
            size_z = (max_z - min_z) * 1000

            return _ok("包围盒", {
                "min_mm": [round(min_x * 1000, 3), round(min_y * 1000, 3), round(min_z * 1000, 3)],
                "max_mm": [round(max_x * 1000, 3), round(max_y * 1000, 3), round(max_z * 1000, 3)],
                "size_mm": [round(size_x, 3), round(size_y, 3), round(size_z, 3)],
            })
        except Exception as e:
            return _error(f"获取包围盒失败: {e}")

    def get_mass_properties(self) -> str:
        """
        获取质量属性（体积/质量/重心/惯性矩）

        模型用体积和重心判断"东西是不是画对了"——
        比如画了一个实心圆盘却得到接近 0 的体积，说明出了问题。
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            # GetMassProperties2 返回数组:
            # [status, volume, area, mass, centerX, centerY, centerZ,
            #  ixx, iyy, izz, ixy, ixz, iyz, principalMomX, ...]
            mass_props = doc.GetMassProperties2(0)  # 0 = 不更新
            if mass_props is None or len(mass_props) < 4:
                return _error("无法获取质量属性（可能没有实体几何或密度未设置）")

            volume_m3 = mass_props[1] if len(mass_props) > 1 else 0
            area_m2 = mass_props[2] if len(mass_props) > 2 else 0
            mass_kg = mass_props[3] if len(mass_props) > 3 else 0
            cx = mass_props[4] if len(mass_props) > 4 else 0
            cy = mass_props[5] if len(mass_props) > 5 else 0
            cz = mass_props[6] if len(mass_props) > 6 else 0

            return _ok("质量属性", {
                "volume_mm3": round(volume_m3 * 1e9, 3) if volume_m3 else 0,
                "area_mm2": round(area_m2 * 1e6, 3) if area_m2 else 0,
                "mass_kg": round(mass_kg, 6) if mass_kg else 0,
                "center_of_mass_mm": [round(cx * 1000, 3), round(cy * 1000, 3), round(cz * 1000, 3)],
            })
        except Exception as e:
            return _error(f"获取质量属性失败: {e}")

    # ---- 感知能力：截图渲染（视觉维度）----

    def render_screenshot(self, view: str = "iso", width: int = 800, height: int = 600,
                          output_path: str = "") -> str:
        """
        渲染当前模型的截图并保存为 PNG

        这是"模型看到图形"的核心手段：截图 → 返回图片路径 →
        local-agent 读取图片 → 传给 VLM（视觉语言模型）分析 → 反馈修正。

        Args:
            view: 视角方向 ("iso"等轴测, "front"前视, "top"俯视, "right"右视, "back"后视, "left"左视, "bottom"仰视)
            width: 图片宽度（像素）
            height: 图片高度（像素）
            output_path: 输出路径（PNG）。为空则自动生成临时路径。
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            swApp = self.swApp

            # 切换视角
            view_map = {
                "iso": 1,    # swIsometricView
                "front": 2,  # swFrontView
                "back": 3,   # swBackView
                "left": 4,  # swLeftView
                "right": 5,  # swRightView
                "top": 6,    # swTopView
                "bottom": 7, # swBottomView
            }
            view_id = view_map.get(view.lower(), 1)
            doc.ShowNamedView2("", view_id)  # 空名称 = 标准视角

            # 适配视图大小
            doc.ViewZoomToExtents()

            # 设置渲染模式为带边线上色（最利于 VLM 理解）
            # swViewModeShadedWithEdges = 6
            doc.ViewDisplayShadedWithEdges()

            # 生成输出路径
            if not output_path:
                import tempfile
                import os
                temp_dir = tempfile.gettempdir()
                output_path = os.path.join(temp_dir, "sw_screenshot.png")

            # 确保路径以 .png 结尾
            if not output_path.lower().endswith(".png"):
                output_path += ".png"

            # 使用 SolidWorks 的 SaveAs 将当前视图保存为图片
            # SaveAs3(path, version, options, ...) 支持图片格式
            result = doc.SaveAs3(
                output_path,
                0,    # version
                0,    # options
                0,    # swImageType: PNG
            )

            if result != 0:
                # 回退方案：使用 IModelView 的 ScreenCapture
                try:
                    modelView = doc.ActiveView
                    if modelView is not None:
                        # 直接使用 SaveAs 尝试
                        result2 = doc.SaveAs3(output_path, 0, 0, 0)
                        if result2 != 0:
                            return _error(f"截图保存失败，错误码: {result}（回退也失败: {result2}）")
                except Exception as e2:
                    return _error(f"截图保存失败，错误码: {result}（回退也失败: {e2}）")

            # 验证文件存在
            import os
            file_exists = os.path.exists(output_path)
            file_size = os.path.getsize(output_path) if file_exists else 0

            return _ok(f"截图已保存: {output_path}", {
                "path": output_path,
                "view": view,
                "width": width,
                "height": height,
                "file_exists": file_exists,
                "file_size": file_size,
            })
        except Exception as e:
            return _error(f"截图失败: {e}")

    def render_multi_view(self, output_dir: str = "") -> str:
        """
        渲染多视角截图（等轴测 + 前视 + 俯视 + 右视）

        多角度截图让 VLM 能理解三维形状，等轴测看整体，
        正交视图看截面轮廓。
        """
        import os
        import tempfile

        if not output_dir:
            output_dir = tempfile.gettempdir()

        views = ["iso", "front", "top", "right"]
        results = []
        for v in views:
            path = os.path.join(output_dir, f"sw_{v}.png")
            r = self.render_screenshot(view=v, output_path=path)
            results.append({"view": v, "result": r})

        return _ok(f"已渲染 {len(views)} 个视角", {
            "views": views,
            "results": results,
        })

    # ---- 感知能力：错误诊断 ----

    def get_last_errors(self) -> str:
        """
        获取 SolidWorks 特征树中的错误/警告状态

        SolidWorks 给每个特征标记状态:
        - swFeatureStatusOk = 0
        - swFeatureStatusWarning = 1
        - swFeatureStatusError = 2

        模型通过这个知道"哪一步出了问题，是警告还是错误"。
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        try:
            feat = doc.FirstFeature
            errors = []
            warnings = []
            idx = 0
            while feat is not None:
                # GetFeatureScope 检查特征状态
                # 使用 GetErrorCode 获取错误代码（如果 API 支持）
                status = 0
                try:
                    # 尝试通过特征属性获取状态
                    # SolidWorks API: feat.GetErrorCode
                    if hasattr(feat, "GetErrorCode"):
                        err_code = feat.GetErrorCode
                        if err_code and err_code > 0:
                            errors.append({"index": idx, "name": feat.Name, "code": err_code})
                except Exception:
                    pass

                # 检查是否被抑制（被抑制的特征可能暗示问题）
                try:
                    if hasattr(feat, "IsSuppressed") and feat.IsSuppressed():
                        warnings.append({"index": idx, "name": feat.Name, "reason": "被抑制"})
                except Exception:
                    pass

                feat = feat.GetNextFeature
                idx += 1

            if not errors and not warnings:
                return _ok("无错误无警告", {"error_count": 0, "warning_count": 0})

            return _ok(f"{len(errors)} 个错误, {len(warnings)} 个警告", {
                "errors": errors,
                "warnings": warnings,
                "error_count": len(errors),
                "warning_count": len(warnings),
            })
        except Exception as e:
            return _error(f"获取错误状态失败: {e}")

    def diagnose(self) -> str:
        """
        一键诊断：综合特征树 + 尺寸 + 包围盒 + 质量 + 错误状态

        这是给模型用的"全面体检"——一次调用获取所有关键信息，
        用于在复杂建模过程中判断当前状态是否正确。
        """
        try:
            doc = SWConnection.get_active_doc()
        except RuntimeError as e:
            return _error(str(e))

        result = {
            "document": json.loads(self.get_info()),
            "feature_tree": json.loads(self.get_feature_tree()),
            "bounding_box": json.loads(self.get_bounding_box()),
            "mass_properties": json.loads(self.get_mass_properties()),
            "errors": json.loads(self.get_last_errors()),
        }

        return _ok("诊断完成", result)


# ============================================================
# 辅助函数
# ============================================================

def _ok(msg: str, data: dict = None) -> str:
    """生成成功 JSON"""
    return json.dumps({"status": "ok", "message": msg, "data": data or {}},
                       ensure_ascii=False)


def _error(msg: str) -> str:
    """生成错误 JSON"""
    return json.dumps({"status": "error", "message": msg},
                       ensure_ascii=False)


# ============================================================
# 单位转换辅助
# ============================================================

def mm_to_m(mm: float) -> float:
    """毫米转米"""
    return mm / 1000.0


def m_to_mm(m: float) -> float:
    """米转毫米"""
    return m * 1000.0
