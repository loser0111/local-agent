#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""merge_json.py —— 合并多个 JSON 文件（简洁但功能齐全）。

用法：
  python merge_json.py a.json b.json -o out.json             # 对象递归合并（默认）
  python merge_json.py data_*.json -o merged.json            # 支持通配符
  python merge_json.py p*.json -s list -o all.json           # 数组拼接
  python merge_json.py p*.json -s list -k id -o all.json     # 数组按 id 去重

作为库使用：from merge_json import merge_files, deep_merge
"""
import argparse
import glob
import json


def deep_merge(base, override):
    """递归合并两个字典，override 中的值优先。"""
    return {
        k: deep_merge(base[k], v)
        if isinstance(base.get(k), dict) and isinstance(v, dict) else v
        for k, v in {**base, **override}.items()
    }


def load(path):
    """读取 JSON 文件，出错时给出友好提示。"""
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        raise SystemExit(f"读取 {path} 失败: {e}")


def expand(patterns):
    """把通配符展开成文件列表，保持顺序并去重。"""
    files = []
    for p in patterns:
        found = sorted(glob.glob(p)) or [p]
        for f in found:
            if f not in files:
                files.append(f)
    return files


def merge_files(files, output="merged.json", strategy="deep", key=None, indent=2):
    """读取、合并多个 JSON 文件并写出结果。"""
    datas = [load(f) for f in files]

    if strategy == "list":
        merged = [x for d in datas for x in d]
        if key:  # 按字段去重，保留首次出现
            seen, uniq = set(), []
            for x in merged:
                if x.get(key) not in seen:
                    seen.add(x.get(key))
                    uniq.append(x)
            merged = uniq
    else:
        merged = {}
        for d, f in zip(datas, files):
            if not isinstance(d, dict):
                raise SystemExit(f"{f} 不是 JSON 对象，无法用 deep 策略合并")
            merged = deep_merge(merged, d)

    with open(output, "w", encoding="utf-8") as f:
        json.dump(merged, f, ensure_ascii=False, indent=indent or None)
    return merged


def main():
    ap = argparse.ArgumentParser(description="合并多个 JSON 文件")
    ap.add_argument("files", nargs="+", help="JSON 文件（支持通配符）")
    ap.add_argument("-o", "--output", default="merged.json", help="输出文件（默认 merged.json）")
    ap.add_argument("-s", "--strategy", choices=["deep", "list"], default="deep",
                    help="deep=对象递归合并（默认），list=数组拼接")
    ap.add_argument("-k", "--key", help="list 策略下按该字段去重")
    ap.add_argument("--indent", type=int, default=2, help="缩进，0 表示压缩输出")
    a = ap.parse_args()

    files = expand(a.files)
    merge_files(files, a.output, a.strategy, a.key, a.indent)
    print(f"已合并 {len(files)} 个文件 -> {a.output}")


if __name__ == "__main__":
    main()
