# todo.py —— 简洁版命令行待办清单

tasks = []

while True:
    print("\n1.查看  2.添加  3.删除  4.退出")
    for i, t in enumerate(tasks, 1):
        print(f"  {i}. {t}")
    c = input("选择(1-4): ").strip()

    if c == "2" and (t := input("新任务: ").strip()):
        tasks.append(t)
    elif c == "3" and tasks:
        n = input("删除编号: ").strip()
        if n.isdigit() and 1 <= int(n) <= len(tasks):
            print("已删除：", tasks.pop(int(n) - 1))
    elif c == "4":
        break