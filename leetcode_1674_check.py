import random

def solve(nums, limit):
    """差分数组解法，O(n + limit)"""
    n = len(nums)
    diff = [0] * (2 * limit + 2)
    for i in range(n // 2):
        a, b = nums[i], nums[n - 1 - i]
        if a > b:
            a, b = b, a
        # 基础代价：所有 T 都要 2 次
        diff[2] += 2
        diff[2 * limit + 1] -= 2
        # 降到 1 次的区间：T in [a+1, b+limit]
        diff[a + 1] -= 1
        diff[b + limit + 1] += 1
        # 降到 0 次的点：T = a + b
        diff[a + b] -= 1
        diff[a + b + 1] += 1
    best = float('inf')
    cur = 0
    for T in range(2, 2 * limit + 1):
        cur += diff[T]
        best = min(best, cur)
    return best


def brute(nums, limit):
    """暴力：枚举每个目标 T，逐对算代价"""
    n = len(nums)
    best = float('inf')
    for T in range(2, 2 * limit + 1):
        cost = 0
        for i in range(n // 2):
            a, b = nums[i], nums[n - 1 - i]
            if a + b == T:
                c = 0
            elif 1 <= T - a <= limit or 1 <= T - b <= limit:
                c = 1
            else:
                c = 2
            cost += c
        best = min(best, cost)
    return best


# 官方示例
assert solve([1, 2, 4, 3], 4) == 1
assert solve([1, 2, 2, 1], 2) == 2
assert solve([1, 2, 1, 2], 2) == 0
print("examples ok")

# 随机对拍
random.seed(1)
for _ in range(5000):
    limit = random.randint(1, 12)
    n = random.randint(1, 6) * 2
    nums = [random.randint(1, limit) for _ in range(n)]
    s, b = solve(nums, limit), brute(nums, limit)
    if s != b:
        print("MISMATCH", nums, limit, s, b)
        break
else:
    print("random tests all ok")
