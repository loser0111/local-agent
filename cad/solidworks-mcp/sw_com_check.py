"""
SolidWorks COM 连接检查脚本
在正式使用前，先运行此脚本确认 win32com 能正常连接 SolidWorks。

用法: python sw_com_check.py
"""

import sys

try:
    import win32com.client
except ImportError:
    print("ERROR: pywin32 未安装。请运行: pip install pywin32")
    sys.exit(1)

import pythoncom


def check():
    """尝试连接 SolidWorks 并报告状态"""
    print("=" * 50)
    print("SolidWorks COM 连接检查")
    print("=" * 50)

    # 1. 检查 pythoncom
    try:
        pythoncom.CoInitialize()
        print("[OK] pythoncom 初始化成功")
    except Exception as e:
        print(f"[FAIL] pythoncom 初始化失败: {e}")
        return False

    # 2. 尝试连接 SolidWorks
    try:
        swApp = win32com.client.Dispatch("SldWorks.Application")
        print("[OK] 成功连接 SldWorks.Application")
    except Exception as e:
        print(f"[FAIL] 无法连接 SldWorks.Application: {e}")
        print()
        print("可能原因：")
        print("  1. SolidWorks 未安装")
        print("  2. SolidWorks 版本太旧（需要 2022+）")
        print("  3. COM 注册异常（尝试以管理员运行: sldworks.exe /regserver）")
        return False

    # 3. 获取版本
    try:
        version = swApp.RevisionNumber()
        print(f"[OK] SolidWorks 版本: {version}")
    except Exception as e:
        print(f"[WARN] 获取版本失败: {e}")

    # 4. 获取活动文档
    try:
        doc = swApp.ActiveDoc
        if doc is not None:
            title = doc.GetTitle()
            doc_type = doc.GetType()
            type_names = {1: "Part", 2: "Assembly", 3: "Drawing"}
            type_name = type_names.get(doc_type, f"Unknown({doc_type})")
            print(f"[OK] 活动文档: {title} ({type_name})")
        else:
            print("[INFO] 当前无活动文档（正常，SW 可能未打开文件）")
    except Exception as e:
        print(f"[WARN] 获取活动文档失败: {e}")

    # 5. 列出已打开的文档
    try:
        doc_count = swApp.GetDocumentCount()
        print(f"[INFO] 已打开 {doc_count} 个文档")
    except Exception as e:
        print(f"[WARN] 获取文档数量失败: {e}")

    print()
    print("=" * 50)
    print("连接检查完成。如果全部 OK，可以开始使用了。")
    print("=" * 50)
    return True


if __name__ == "__main__":
    ok = check()
    sys.exit(0 if ok else 1)
