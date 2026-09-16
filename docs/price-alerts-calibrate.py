#!/usr/bin/env python3
"""Calibrate the ticker's price alerts (docs/price-alerts.md).

Pulls 5-minute candles for the ticker's tokens from Coinbase Exchange's
public API, prints how big their hourly and daily moves are, and replays the
alert rule over the month so a threshold can be judged by what it would have
sent: how many alerts, on how many days, how often the 4-per-24h cap bound.

    python3 docs/price-alerts-calibrate.py            # fetch 30 days, replay
    python3 docs/price-alerts-calibrate.py --cache c.json   # reuse a fetch

Change THRESHOLDS to try other numbers. The replay steps every 5 minutes
where the daemon samples every minute, so "two consecutive samples" here is
ten minutes rather than two; it errs on the quiet side.
"""
import bisect, datetime as dt, json, os, statistics, sys, time, urllib.request
from collections import defaultdict

# token: (1h move %, 24h move %) that earns a notification — what alerts.go uses
THRESHOLDS = {"SOL-USD": (2.5, 6), "PUMP-USD": (5, 12), "MET-USD": (6, 15), "SKR-USD": (8, 25)}
CAP, COOLDOWN, LADDER_MULTS = 4, 30 * 60, (1, 1.5, 2, 2)
DAYS, GRANULARITY = 30, 300


def fetch(pairs, days):
    end = dt.datetime.now(dt.timezone.utc).replace(second=0, microsecond=0)
    out = {}
    for p in pairs:
        rows, t = {}, end - dt.timedelta(days=days)
        while t < end:
            t2 = min(t + dt.timedelta(seconds=GRANULARITY * 300), end)
            url = (f"https://api.exchange.coinbase.com/products/{p}/candles"
                   f"?granularity={GRANULARITY}&start={t.isoformat()}&end={t2.isoformat()}")
            req = urllib.request.Request(url, headers={"User-Agent": "exe-price-alerts"})
            for _ in range(3):
                try:
                    with urllib.request.urlopen(req, timeout=20) as r:
                        for c in json.load(r):
                            rows[c[0]] = c  # time, low, high, open, close, volume
                    break
                except Exception:
                    time.sleep(1.5)
            t = t2
            time.sleep(0.15)
        out[p] = sorted(rows.values())
        print(f"{p}: {len(out[p])} candles", file=sys.stderr)
    return out


def price_at(ts, cl, t, tol=600):
    i = bisect.bisect_right(ts, t) - 1
    return cl[i] if i >= 0 and t - ts[i] <= tol else None


def pct(xs, q):
    xs = sorted(xs)
    k = (len(xs) - 1) * q / 100
    f, c = int(k), min(int(k) + 1, len(xs) - 1)
    return xs[f] + (xs[c] - xs[f]) * (k - f)


def moves(rows, since=None):
    ts, cl = [r[0] for r in rows], [r[4] for r in rows]
    r1, r24, r5 = [], [], []
    for i, t in enumerate(ts):
        if since and t < since:
            continue
        a, b = price_at(ts, cl, t - 3600), price_at(ts, cl, t - 86400)
        if a: r1.append(abs(cl[i] / a - 1) * 100)
        if b: r24.append(abs(cl[i] / b - 1) * 100)
        if i and ts[i] - ts[i - 1] == GRANULARITY: r5.append(cl[i] / cl[i - 1] - 1)
    vol = statistics.pstdev(r5) * (86400 / GRANULARITY) ** .5 * 100 if len(r5) > 1 else 0
    return r1, r24, vol


def replay(rows, J, D):
    """The rule of docs/price-alerts.md: two windows, persistence, cooldown,
    ladder re-arm, escalating multipliers, a sliding 24h cap of CAP."""
    ts, cl = [r[0] for r in rows], [r[4] for r in rows]
    alerts, capped, prev_hit = [], [], False
    for i, t in enumerate(ts):
        a, b = price_at(ts, cl, t - 3600), price_at(ts, cl, t - 86400)
        r1 = (cl[i] / a - 1) * 100 if a else 0
        r24 = (cl[i] / b - 1) * 100 if b else 0
        recent = [x for x in alerts if t - x["t"] < 86400]
        m = LADDER_MULTS[min(len(recent), len(LADDER_MULTS) - 1)]
        hit = abs(r1) >= m * J or abs(r24) >= m * D
        ok, prev_hit = hit and prev_hit, hit
        if not ok:
            continue
        last = max(alerts + capped, key=lambda x: x["t"], default=None)  # delivered or held back
        if last and t - last["t"] < COOLDOWN:
            continue
        if last and t - last["t"] < 86400 and abs(cl[i] / last["p"] - 1) * 100 < J:
            continue
        rec = {"t": t, "p": cl[i], "r1": r1, "r24": r24, "k": len(recent) + 1,
               "win": "1h" if abs(r1) >= m * J else "24h"}
        (capped if len(recent) >= CAP else alerts).append(rec)
    return alerts, capped


def main():
    cache = sys.argv[sys.argv.index("--cache") + 1] if "--cache" in sys.argv else None
    if cache and os.path.exists(cache):
        data = json.load(open(cache))
    else:
        data = fetch(list(THRESHOLDS), DAYS)
        if cache:
            json.dump(data, open(cache, "w"))
    when = lambda t: dt.datetime.fromtimestamp(t, dt.timezone.utc).strftime("%m-%d %H:%M")
    print("How the tokens move (absolute % change, close to close)")
    print(f"{'':9s} {'daily vol':>9s} | |1h| p99   p99.5   max | |24h| p90   p95    max")
    for p, rows in data.items():
        r1, r24, vol = moves(rows)
        r1b, r24b, volb = moves(rows, rows[-1][0] - 14 * 86400)
        print(f"{p:9s} {vol:8.1f}% | {pct(r1, 99):6.2f} {pct(r1, 99.5):6.2f} {max(r1):6.2f} |"
              f" {pct(r24, 90):7.2f} {pct(r24, 95):6.2f} {max(r24):6.2f}   (30 days)")
        print(f"{'':9s} {volb:8.1f}% | {pct(r1b, 99):6.2f} {pct(r1b, 99.5):6.2f} {max(r1b):6.2f} |"
              f" {pct(r24b, 90):7.2f} {pct(r24b, 95):6.2f} {max(r24b):6.2f}   (last 14)")
    print("\nReplay of the rule with THRESHOLDS")
    for p, rows in data.items():
        J, D = THRESHOLDS[p]
        alerts, capped = replay(rows, J, D)
        days = (rows[-1][0] - rows[0][0]) / 86400
        per_day = defaultdict(int)
        for x in alerts:
            per_day[when(x["t"])[:5]] += 1
        print(f"\n{p}: 1h >= {J}%, 24h >= {D}%  ->  {len(alerts)} alerts in {days:.0f} days "
              f"({len(alerts) / days:.2f}/day), on {len(per_day)} days, busiest {max(per_day.values(), default=0)}, "
              f"{len(capped)} held back by the cap")
        for x in alerts:
            print(f"   {when(x['t'])} UTC  #{x['k']}  {x['win']:3s}  1h {x['r1']:+6.2f}%  24h {x['r24']:+7.2f}%  at {x['p']:.6g}")
        for x in capped:
            print(f"   {when(x['t'])} UTC  capped   1h {x['r1']:+6.2f}%  24h {x['r24']:+7.2f}%  at {x['p']:.6g}")


if __name__ == "__main__":
    main()
