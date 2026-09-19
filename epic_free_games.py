import json
import sys
import io
import urllib.request

API = "https://store-site-backend-static.ak.epicgames.com/freeGamesPromotions"
QUERY = "?locale=zh-CN&country=CN&allowCountries=CN"

try:
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
except Exception:
    pass


def fetch():
    req = urllib.request.Request(
        API + QUERY,
        headers={
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
            "Accept": "application/json",
        },
    )
    with urllib.request.urlopen(req, timeout=25) as resp:
        return json.loads(resp.read().decode("utf-8"))


def pick_image(images):
    if not images:
        return None
    order = ["OfferImageWide", "DieselStoreFrontWide", "Thumbnail", "OfferImageTall", "DieselStoreFrontTall"]
    by_type = {}
    for img in images:
        t = img.get("type")
        if t and t not in by_type:
            by_type[t] = img.get("url")
    for t in order:
        if by_type.get(t):
            return by_type[t]
    return images[0].get("url")


def slug_of(elem):
    if elem.get("productSlug"):
        return elem["productSlug"]
    if elem.get("urlSlug"):
        return elem["urlSlug"]
    mappings = (elem.get("catalogNs") or {}).get("mappings") or []
    if mappings:
        return mappings[0].get("pageSlug")
    return None


def first_offer(promo_groups):
    if not promo_groups:
        return None
    offers = promo_groups[0].get("promotionalOffers") or []
    if not offers:
        return None
    off = offers[0]
    return off.get("startDate"), off.get("endDate")


def build(elem, window):
    slug = slug_of(elem)
    price = ((elem.get("price") or {}).get("totalPrice") or {})
    fmt = price.get("fmtPrice") or {}
    return {
        "title": elem.get("title"),
        "url": ("https://store.epicgames.com/zh-CN/p/" + slug) if slug else None,
        "original_price": fmt.get("originalPrice"),
        "image": pick_image(elem.get("keyImages")),
        "start": window[0],
        "end": window[1],
        "description": elem.get("description"),
    }


def main():
    data = fetch()
    elements = data["data"]["Catalog"]["searchStore"]["elements"]
    current = []
    upcoming = []
    for elem in elements:
        promo = elem.get("promotions") or {}
        now = first_offer(promo.get("promotionalOffers"))
        if now:
            current.append(build(elem, now))
        soon = first_offer(promo.get("upcomingPromotionalOffers"))
        if soon:
            upcoming.append(build(elem, soon))
    result = {
        "region": "CN",
        "locale": "zh-CN",
        "current_free": current,
        "upcoming_free": upcoming,
    }
    if not current and not upcoming:
        result["note"] = "当前接口未返回免费游戏信息"
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False))
        sys.exit(1)