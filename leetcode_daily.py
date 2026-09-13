import json, urllib.request

HOST = "https://leetcode.com"
QUERY = (
    "query questionOfToday { activeDailyCodingChallengeQuestion {"
    " date link"
    " question { frontendQuestionId: questionFrontendId title titleSlug"
    " difficulty acRate paidOnly: isPaidOnly topicTags { name slug } } } }"
)

def fetch_daily(host=HOST):
    payload = json.dumps({
        "operationName": "questionOfToday",
        "variables": {},
        "query": QUERY,
    }).encode("utf-8")
    req = urllib.request.Request(
        host + "/graphql/",
        data=payload,
        headers={
            "Content-Type": "application/json",
            "Referer": host + "/",
            "Origin": host,
            "User-Agent": "Mozilla/5.0",
        },
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    return data["data"]["activeDailyCodingChallengeQuestion"]

def main():
    q = fetch_daily()
    info = q["question"]
    result = {
        "date": q["date"],
        "id": info["frontendQuestionId"],
        "title": info["title"],
        "title_slug": info["titleSlug"],
        "difficulty": info["difficulty"],
        "ac_rate": round(info["acRate"], 2),
        "paid_only": info["paidOnly"],
        "tags": [t["name"] for t in info["topicTags"]],
        "url": HOST + q["link"],
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
