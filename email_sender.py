#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
email_sender.py
===============
一个零依赖（仅用 Python 标准库）的邮件发送工具。

功能:
  * 支持 QQ / 163 / 126 / Gmail / Outlook / 阿里云 等常见邮箱一键发送
  * 支持纯文本 / HTML 正文
  * 支持多收件人、抄送(cc)、密送(bcc)
  * 支持添加多个附件
  * 支持 SSL(465) 与 STARTTLS(587)
  * 密码优先从环境变量读取，避免明文写在命令里

用法示例:
  python email_sender.py init                      # 生成配置模板
  python email_sender.py check                     # 测试 SMTP 登录
  python email_sender.py send --to a@x.com --subject "Hello" --body "正文"
  python email_sender.py send --to a@x.com,b@y.com --cc c@z.com \
      --subject "周报" --html --body-file report.html --attach a.pdf b.xlsx
"""

import argparse
import json
import mimetypes
import os
import smtplib
import ssl
import sys
from email.message import EmailMessage
from email.utils import formataddr
from pathlib import Path

CONFIG_PATH = Path.home() / ".email_sender.json"

# 常见邮箱服务商预设
PROVIDERS = {
    "qq":      {"host": "smtp.qq.com",        "port": 465, "ssl": True},
    "163":     {"host": "smtp.163.com",       "port": 465, "ssl": True},
    "126":     {"host": "smtp.126.com",       "port": 465, "ssl": True},
    "gmail":   {"host": "smtp.gmail.com",     "port": 465, "ssl": True},
    "outlook": {"host": "smtp.office365.com", "port": 587, "ssl": False},
    "hotmail": {"host": "smtp.office365.com", "port": 587, "ssl": False},
    "sina":    {"host": "smtp.sina.com",      "port": 465, "ssl": True},
    "aliyun":  {"host": "smtp.aliyun.com",    "port": 465, "ssl": True},
    "yeah":    {"host": "smtp.yeah.net",      "port": 465, "ssl": True},
}

TEMPLATE = {
    "provider": "qq",
    "host": "",
    "port": 0,
    "ssl": True,
    "user": "your_account@qq.com",
    "password": "",
    "from_name": "自动邮件助手",
    "_comment": "host/port/ssl 留空则按 provider 预设自动填充；password 建议留空并改用环境变量 EMAIL_PASSWORD",
}


def load_config():
    if CONFIG_PATH.exists():
        with open(CONFIG_PATH, "r", encoding="utf-8") as f:
            return json.load(f)
    return {}


def save_config(cfg):
    with open(CONFIG_PATH, "w", encoding="utf-8") as f:
        json.dump(cfg, f, ensure_ascii=False, indent=2)


def resolve_settings(args):
    cfg = load_config()
    provider = args.provider or cfg.get("provider") or "qq"
    preset = PROVIDERS.get(provider.lower(), {})

    host = args.host or cfg.get("host") or preset.get("host")
    port = args.port or cfg.get("port") or preset.get("port", 465)
    use_ssl = preset.get("ssl", True) if cfg.get("ssl") is None else cfg.get("ssl")
    if args.no_ssl:
        use_ssl = False
    if args.starttls:
        use_ssl = False

    user = args.user or cfg.get("user") or os.environ.get("EMAIL_USER")
    password = args.password or os.environ.get("EMAIL_PASSWORD") or cfg.get("password")
    from_name = args.from_name or cfg.get("from_name") or ""

    if not host:
        sys.exit("[错误] 未识别 SMTP 服务器，请用 --provider 或 --host 指定")
    if not user or user.startswith("your_account"):
        sys.exit("[错误] 未配置发件邮箱账号，请设置 --user 或环境变量 EMAIL_USER")
    if not password:
        sys.exit("[错误] 未配置邮箱授权码/密码，请设置环境变量 EMAIL_PASSWORD 或配置文件")

    return {"host": host, "port": int(port), "ssl": bool(use_ssl),
            "user": user, "password": password, "from_name": from_name}


def build_message(settings, args):
    msg = EmailMessage()
    sender = formataddr((settings["from_name"], settings["user"])) if settings["from_name"] else settings["user"]
    msg["From"] = sender
    msg["To"] = ", ".join(args.to)
    if args.cc:
        msg["Cc"] = ", ".join(args.cc)
    msg["Subject"] = args.subject

    body = args.body
    if args.body_file:
        with open(args.body_file, "r", encoding=args.encoding) as f:
            body = f.read()
    body = body or ""

    if args.html:
        msg.set_content("这是一封 HTML 邮件，请使用支持 HTML 的客户端查看。")
        msg.add_alternative(body, subtype="html")
    else:
        msg.set_content(body)

    for path in (args.attach or []):
        p = Path(path)
        if not p.is_file():
            sys.exit("[错误] 附件不存在: %s" % path)
        ctype, encoding = mimetypes.guess_type(str(p))
        if ctype is None or encoding is not None:
            ctype = "application/octet-stream"
        maintype, subtype = ctype.split("/", 1)
        with open(p, "rb") as f:
            msg.add_attachment(f.read(), maintype=maintype, subtype=subtype, filename=p.name)
    return msg


def _connect(settings):
    context = ssl.create_default_context()
    if settings["ssl"]:
        return smtplib.SMTP_SSL(settings["host"], settings["port"], context=context, timeout=30)
    server = smtplib.SMTP(settings["host"], settings["port"], timeout=30)
    server.ehlo()
    server.starttls(context=context)
    return server


def send_mail(settings, msg, args):
    recipients = list(args.to) + list(args.cc or []) + list(args.bcc or [])
    print("[信息] 正在连接 %s:%s (%s) ..." % (
        settings["host"], settings["port"], "SSL" if settings["ssl"] else "STARTTLS"))
    server = _connect(settings)
    try:
        server.login(settings["user"], settings["password"])
        server.send_message(msg, from_addr=settings["user"], to_addrs=recipients)
    finally:
        server.quit()
    print("[成功] 邮件已发送给: %s" % ", ".join(recipients))


def cmd_init(args):
    if CONFIG_PATH.exists() and not args.force:
        print("[提示] 配置文件已存在: %s（加 --force 覆盖）" % CONFIG_PATH)
        return
    save_config(TEMPLATE)
    print("[成功] 已生成配置模板: %s" % CONFIG_PATH)
    print("请填写 user / password（或使用环境变量 EMAIL_PASSWORD）后即可发送。")


def cmd_check(args):
    settings = resolve_settings(args)
    print("[信息] 测试登录 %s:%s ..." % (settings["host"], settings["port"]))
    server = _connect(settings)
    try:
        server.login(settings["user"], settings["password"])
        print("[成功] SMTP 登录成功，账号可用。")
    finally:
        server.quit()


def cmd_send(args):
    settings = resolve_settings(args)
    msg = build_message(settings, args)
    send_mail(settings, msg, args)


def main():
    parser = argparse.ArgumentParser(description="邮件发送工具（仅用 Python 标准库）")
    parser.add_argument("--provider", help="邮箱服务商预设: %s" % "/".join(PROVIDERS))
    parser.add_argument("--host", help="SMTP 服务器地址")
    parser.add_argument("--port", type=int, help="SMTP 端口")
    parser.add_argument("--no-ssl", action="store_true", help="不使用 SSL")
    parser.add_argument("--starttls", action="store_true", help="使用 STARTTLS(587)")
    parser.add_argument("--user", help="发件邮箱账号")
    parser.add_argument("--password", help="授权码/密码（建议改用环境变量 EMAIL_PASSWORD）")
    parser.add_argument("--from-name", help="发件人显示名")

    sub = parser.add_subparsers(dest="command")

    p_init = sub.add_parser("init", help="生成配置文件模板")
    p_init.add_argument("--force", action="store_true")
    p_init.set_defaults(func=cmd_init)

    p_check = sub.add_parser("check", help="测试 SMTP 登录")
    p_check.set_defaults(func=cmd_check)

    p_send = sub.add_parser("send", help="发送邮件")
    p_send.add_argument("--to", required=True, action="append", help="收件人，可重复或逗号分隔")
    p_send.add_argument("--cc", action="append", help="抄送")
    p_send.add_argument("--bcc", action="append", help="密送")
    p_send.add_argument("--subject", required=True, help="主题")
    p_send.add_argument("--body", help="正文内容")
    p_send.add_argument("--body-file", help="从文件读取正文")
    p_send.add_argument("--html", action="store_true", help="正文按 HTML 解析")
    p_send.add_argument("--attach", nargs="*", help="附件路径列表")
    p_send.add_argument("--encoding", default="utf-8", help="读取正文文件的编码")
    p_send.set_defaults(func=cmd_send)

    args = parser.parse_args()

    for attr in ("to", "cc", "bcc"):
        val = getattr(args, attr, None)
        if val:
            flat = []
            for item in val:
                flat.extend([x.strip() for x in item.split(",") if x.strip()])
            setattr(args, attr, flat)

    if not getattr(args, "command", None):
        parser.print_help()
        return
    args.func(args)


if __name__ == "__main__":
    main()
