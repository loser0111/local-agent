#!/usr/bin/env python3
"""
SolidWorks CLI 工具入口

被 local-agent 的 CLI 工具模板调用，通过 win32com 驱动 SolidWorks 建模。
每个操作接收 JSON 参数，返回 JSON 结果。

用法（直接测试）:
    python sw_cli.py '{"action":"create_part"}'
    python sw_cli.py '{"action":"sketch_circle","radius":0.025}'
    python sw_cli.py '{"action":"extrude","depth":0.01}'

在 local-agent 工具配置中:
    命令模板: python "C:\\path\\to\\sw_cli.py" '{{args}}'
    参数: args (JSON 字符串，包含 action 和操作参数)
"""

import json
import sys
import os

# 确保能找到同目录下的 sw_lib.py
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from sw_lib import SWSession, mm_to_m


def main():
    if len(sys.argv) < 2:
        print(json.dumps({"status": "error",
                          "message": "缺少参数。用法: python sw_cli.py '{\"action\":\"...\"}'"}))
        sys.exit(1)

    # 解析参数 JSON
    raw_args = sys.argv[1]
    try:
        args = json.loads(raw_args)
    except json.JSONDecodeError as e:
        print(json.dumps({"status": "error",
                          "message": f"参数 JSON 解析失败: {e}"}))
        sys.exit(1)

    action = args.get("action", "").strip()
    if not action:
        print(json.dumps({"status": "error", "message": "缺少 action 参数"}))
        sys.exit(1)

    # 创建会话并执行操作
    try:
        session = SWSession()
    except Exception as e:
        print(json.dumps({"status": "error",
                          "message": f"连接 SolidWorks 失败: {e}"}))
        sys.exit(1)

    result = dispatch(session, action, args)
    print(result)

    # 根据结果状态退出
    try:
        parsed = json.loads(result)
        sys.exit(0 if parsed.get("status") == "ok" else 1)
    except Exception:
        sys.exit(1)


def dispatch(session: SWSession, action: str, args: dict) -> str:
    """根据 action 分发到对应的 SWSession 方法"""

    # ---- 文档管理 ----
    if action == "create_part":
        return session.create_part(template=args.get("template", "mm"))

    if action == "get_info":
        return session.get_info()

    if action == "save":
        return session.save(path=args.get("path", ""))

    if action == "close":
        return session.close(save=args.get("save", True))

    # ---- 草图操作 ----
    if action == "sketch_rectangle":
        width = args.get("width")
        height = args.get("height")
        if width is None or height is None:
            return _err("sketch_rectangle 需要 width 和 height 参数")
        # 支持 mm 输入（自动转 m）
        unit = args.get("unit", "m")
        if unit == "mm":
            width = mm_to_m(width)
            height = mm_to_m(height)
        return session.sketch_rectangle(width, height)

    if action == "sketch_circle":
        radius = args.get("radius")
        if radius is None:
            return _err("sketch_circle 需要 radius 参数")
        unit = args.get("unit", "m")
        if unit == "mm":
            radius = mm_to_m(radius)
        return session.sketch_circle(radius)

    if action == "sketch_line":
        x1 = args.get("x1", 0)
        y1 = args.get("y1", 0)
        x2 = args.get("x2", 0)
        y2 = args.get("y2", 0)
        unit = args.get("unit", "m")
        if unit == "mm":
            x1, y1, x2, y2 = mm_to_m(x1), mm_to_m(y1), mm_to_m(x2), mm_to_m(y2)
        return session.sketch_line(x1, y1, x2, y2)

    if action == "sketch_polygon":
        radius = args.get("radius")
        sides = args.get("sides")
        if radius is None or sides is None:
            return _err("sketch_polygon 需要 radius 和 sides 参数")
        unit = args.get("unit", "m")
        if unit == "mm":
            radius = mm_to_m(radius)
        return session.sketch_polygon(radius, sides)

    # ---- 特征操作 ----
    if action == "extrude":
        depth = args.get("depth")
        if depth is None:
            return _err("extrude 需要 depth 参数")
        unit = args.get("unit", "m")
        if unit == "mm":
            depth = mm_to_m(depth)
        return session.extrude(depth, thin_feature=args.get("thin_feature", False))

    if action == "extrude_cut":
        depth = args.get("depth")
        if depth is None:
            return _err("extrude_cut 需要 depth 参数")
        unit = args.get("unit", "m")
        if unit == "mm":
            depth = mm_to_m(depth)
        return session.extrude_cut(depth)

    if action == "revolve":
        return session.revolve(axis_x=args.get("axis_x", 0),
                               axis_y=args.get("axis_y", 1))

    if action == "fillet":
        radius = args.get("radius")
        if radius is None:
            return _err("fillet 需要 radius 参数")
        unit = args.get("unit", "m")
        if unit == "mm":
            radius = mm_to_m(radius)
        return session.fillet(radius, edges=args.get("edges"))

    if action == "circular_pattern":
        count = args.get("count")
        if count is None:
            return _err("circular_pattern 需要 count 参数")
        return session.circular_pattern(count, total_angle=args.get("angle", 360.0))

    if action == "rebuild":
        return session.rebuild()

    # ---- 感知能力：几何状态查询 ----

    if action == "get_feature_tree":
        return session.get_feature_tree()

    if action == "get_dimensions":
        return session.get_dimensions()

    if action == "get_bounding_box":
        return session.get_bounding_box()

    if action == "get_mass_properties":
        return session.get_mass_properties()

    if action == "get_last_errors":
        return session.get_last_errors()

    if action == "diagnose":
        return session.diagnose()

    # ---- 感知能力：截图渲染 ----

    if action == "render_screenshot":
        return session.render_screenshot(
            view=args.get("view", "iso"),
            width=args.get("width", 800),
            height=args.get("height", 600),
            output_path=args.get("output_path", ""),
        )

    if action == "render_multi_view":
        return session.render_multi_view(output_dir=args.get("output_dir", ""))

    # ---- 复合操作（高级封装）----

    if action == "create_flange":
        """一步创建法兰盘：圆盘 + 中心孔 + 螺栓孔阵列"""
        return _create_flange(session, args)

    if action == "create_gear_blank":
        """创建齿轮毛坯：圆盘 + 中心孔"""
        return _create_gear_blank(session, args)

    # ---- 未知操作 ----
    return _err(f"未知操作: {action}。可用操作: create_part, sketch_rectangle, "
                 f"sketch_circle, sketch_polygon, sketch_line, extrude, extrude_cut, "
                 f"revolve, fillet, circular_pattern, rebuild, save, close, get_info, "
                 f"get_feature_tree, get_dimensions, get_bounding_box, get_mass_properties, "
                 f"get_last_errors, diagnose, render_screenshot, render_multi_view, "
                 f"create_flange, create_gear_blank")


def _create_flange(session: SWSession, args: dict) -> str:
    """
    复合操作：创建法兰盘

    参数:
        outer_radius: 外圆半径 (mm)
        inner_radius: 中心孔半径 (mm)
        bolt_radius: 螺栓孔半径 (mm)
        bolt_count: 螺栓孔数量
        bolt_circle_radius: 螺栓圆半径 (mm)
        thickness: 厚度 (mm)
    """
    import math
    outer_r = mm_to_m(args.get("outer_radius", 50))
    inner_r = mm_to_m(args.get("inner_radius", 15))
    bolt_r = mm_to_m(args.get("bolt_radius", 5))
    bolt_count = args.get("bolt_count", 6)
    bolt_circle_r = mm_to_m(args.get("bolt_circle_radius", 35))
    thickness = mm_to_m(args.get("thickness", 10))

    steps = []

    # 1. 创建零件
    r = session.create_part()
    steps.append(("create_part", r))

    # 2. 画外圆并拉伸
    r = session.sketch_circle(outer_r)
    steps.append(("sketch_circle(outer)", r))
    r = session.extrude(thickness)
    steps.append(("extrude", r))

    # 3. 在顶面画中心孔并切除
    r = session.sketch_circle(inner_r)
    steps.append(("sketch_circle(inner)", r))
    r = session.extrude_cut(thickness * 1.2)
    steps.append(("extrude_cut(center)", r))

    # 4. 画螺栓孔并阵列
    r = session.sketch_circle_at(bolt_circle_r, 0, bolt_r)
    steps.append(("sketch_circle(bolt)", r))
    r = session.extrude_cut(thickness * 1.2)
    steps.append(("extrude_cut(bolt)", r))
    r = session.circular_pattern(bolt_count, 360.0)
    steps.append(("circular_pattern", r))

    # 汇总结果
    errors = [s for s in steps if '"error"' in s[1]]
    if errors:
        return json.dumps({
            "status": "error",
            "message": f"法兰盘创建部分失败: {errors[-1][0]}: {errors[-1][1]}",
            "steps": [s[0] for s in steps],
        }, ensure_ascii=False)
    return json.dumps({
        "status": "ok",
        "message": f"法兰盘创建完成: 外径{args.get('outer_radius',50)}mm, "
                   f"内径{args.get('inner_radius',15)}mm, "
                   f"{bolt_count}个螺栓孔, 厚度{args.get('thickness',10)}mm",
        "data": {"steps": [s[0] for s in steps]},
    }, ensure_ascii=False)


def _create_gear_blank(session: SWSession, args: dict) -> str:
    """
    复合操作：创建齿轮毛坯（圆盘+中心孔，不含齿廓）

    参数:
        outer_radius: 齿顶圆半径 (mm)
        inner_radius: 中心孔半径 (mm)
        thickness: 厚度 (mm)
    """
    outer_r = mm_to_m(args.get("outer_radius", 40))
    inner_r = mm_to_m(args.get("inner_radius", 10))
    thickness = mm_to_m(args.get("thickness", 8))

    steps = []

    r = session.create_part()
    steps.append(("create_part", r))

    r = session.sketch_circle(outer_r)
    steps.append(("sketch_circle(outer)", r))

    r = session.extrude(thickness)
    steps.append(("extrude", r))

    r = session.sketch_circle(inner_r)
    steps.append(("sketch_circle(inner)", r))

    r = session.extrude_cut(thickness * 1.2)
    steps.append(("extrude_cut(center)", r))

    errors = [s for s in steps if '"error"' in s[1]]
    if errors:
        return json.dumps({
            "status": "error",
            "message": f"齿轮毛坯创建部分失败: {errors[-1][0]}: {errors[-1][1]}",
            "steps": [s[0] for s in steps],
        }, ensure_ascii=False)
    return json.dumps({
        "status": "ok",
        "message": f"齿轮毛坯创建完成: 外径{args.get('outer_radius',40)}mm, "
                   f"内径{args.get('inner_radius',10)}mm, 厚度{args.get('thickness',8)}mm",
        "data": {"steps": [s[0] for s in steps]},
    }, ensure_ascii=False)


def _err(msg: str) -> str:
    return json.dumps({"status": "error", "message": msg}, ensure_ascii=False)


if __name__ == "__main__":
    main()
