#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""xhs_search.py - 小红书关键词搜索（封装为工具，自动无头运行）

用已登录的本地 Chrome 配置，无窗口搜索并返回笔记。
用法:
  python xhs_search.py --keyword "杭州攻略" --limit 10 --content 3
  python xhs_search.py --login      # 首次需要扫码登录
输出: JSON (stdout)
"""
import argparse
import json
import os
import sys
import urllib.parse


def as_int(v, default):
    try:
        return int(str(v).strip())
    except Exception:
        return default


def default_profile():
    return os.environ.get("XHS_PROFILE_DIR") or os.path.join(
        os.path.expanduser("~"), ".local-agent", "xhs_profile")


LOGIN_JS = "()=>({m:document.querySelectorAll('.login-container').length,a:document.querySelectorAll('.reds-avatar,.avatar').length})"
ITEMS_JS = ("()=>Array.from(document.querySelectorAll('section.note-item')).map(el=>{"
            "const a=el.querySelector('a.cover')||el.querySelector('a[href]');"
            "const t=el.querySelector('.title');const au=el.querySelector('.author .name');"
            "const lk=el.querySelector('.like-wrapper .count');"
            "return {title:t?t.innerText.trim():'',author:au?au.innerText.trim():'',"
            "like:lk?lk.innerText.trim():'',href:a?a.getAttribute('href'):''};})")
CONTENT_JS = "()=>{const el=document.querySelector('#detail-desc')||document.querySelector('.desc');return el?el.innerText.trim():'';}"


BLOCK_TYPES = {"image", "media", "font"}


def install_block(ctx):
    def _handler(route):
        try:
            if route.request.resource_type in BLOCK_TYPES:
                route.abort()
            else:
                route.continue_()
        except Exception:
            try:
                route.continue_()
            except Exception:
                pass
    ctx.route("**/*", _handler)


def do_login(profile):
    from playwright.sync_api import sync_playwright
    with sync_playwright() as p:
        ctx = p.chromium.launch_persistent_context(
            user_data_dir=profile, channel="chrome", headless=False,
            locale="zh-CN", viewport={"width": 1300, "height": 880},
            args=["--disable-blink-features=AutomationControlled"])
        pg = ctx.pages[0] if ctx.pages else ctx.new_page()
        pg.goto("https://www.xiaohongshu.com/explore", timeout=45000,
                wait_until="domcontentloaded")
        logged = False
        for _ in range(100):
            pg.wait_for_timeout(3000)
            try:
                r = pg.evaluate(LOGIN_JS)
                if r["m"] == 0 and r["a"] > 0:
                    logged = True
                    break
            except Exception:
                pass
        ctx.close()
    return {"logged_in": logged, "profile": profile}


def do_search(profile, keyword, limit, ncontent, headless):
    from playwright.sync_api import sync_playwright
    result = {"keyword": keyword, "limit": limit, "notes": []}
    url = ("https://www.xiaohongshu.com/search_result?keyword="
           + urllib.parse.quote(keyword))
    with sync_playwright() as p:
        ctx = p.chromium.launch_persistent_context(
            user_data_dir=profile, channel="chrome", headless=headless,
            locale="zh-CN", viewport={"width": 1300, "height": 900},
            args=["--disable-blink-features=AutomationControlled"])
        install_block(ctx)
        try:
            pg = ctx.pages[0] if ctx.pages else ctx.new_page()
            pg.goto(url, timeout=45000, wait_until="commit")
            pg.wait_for_timeout(6000)
            for _try in range(2):
                try:
                    pg.wait_for_selector("section.note-item", timeout=15000)
                    break
                except Exception:
                    pg.goto(url, timeout=45000, wait_until="commit")
                    pg.wait_for_timeout(5000)
            if pg.eval_on_selector_all(".login-container", "e=>e.length") > 0:
                result["error"] = "未登录或登录已失效，请先运行 --login 扫码登录"
                print(json.dumps(result, ensure_ascii=False))
                return result
            seen = -1
            for _ in range(12):
                cnt = pg.eval_on_selector_all("section.note-item", "e=>e.length")
                if cnt >= limit or cnt == seen:
                    break
                seen = cnt
                pg.mouse.wheel(0, 3000)
                pg.wait_for_timeout(1800)
            items = pg.evaluate(ITEMS_JS)
            items = [x for x in items if x.get("href")][:limit]
            for it in items:
                it["url"] = "https://www.xiaohongshu.com" + it.pop("href")
            result["notes"] = items
            for i, it in enumerate(items):
                if i >= ncontent:
                    break
                try:
                    pg.goto(it["url"], timeout=40000, wait_until="commit")
                    pg.wait_for_timeout(3500)
                    it["content"] = pg.evaluate(CONTENT_JS)
                except Exception as e:
                    it["content"] = ""
                    it["content_error"] = str(e)[:120]
            result["count"] = len(items)
        except Exception as e:
            result["error"] = str(e)[:300]
        finally:
            ctx.close()
    return result


def main():
    ap = argparse.ArgumentParser(description="小红书搜索工具")
    ap.add_argument("--keyword", default="")
    ap.add_argument("--limit", default="10")
    ap.add_argument("--content", default="0")
    ap.add_argument("--profile", default=None)
    ap.add_argument("--headless", default="1")
    ap.add_argument("--login", action="store_true")
    args = ap.parse_args()

    profile = args.profile or default_profile()

    if args.login:
        try:
            out = do_login(profile)
        except Exception as e:
            out = {"error": str(e)[:300]}
        print(json.dumps(out, ensure_ascii=False))
        return

    if not args.keyword or "{{" in args.keyword:
        print(json.dumps({"error": "缺少 keyword 参数"}, ensure_ascii=False))
        return

    limit = as_int(args.limit, 10)
    if limit <= 0:
        limit = 10
    ncontent = as_int(args.content, 0)
    if ncontent < 0:
        ncontent = 0
    headless = str(args.headless).strip() not in ("0", "false", "False")

    try:
        out = do_search(profile, args.keyword, limit, ncontent, headless)
    except ImportError as e:
        out = {"error": "playwright 未安装: %s" % e}
    except Exception as e:
        out = {"error": str(e)[:300]}
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    try:
        sys.stdout.reconfigure(encoding="utf-8")
    except Exception:
        pass
    main()
