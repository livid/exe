# Price alerts for the ticker

*Design, 2026-09-16. Not built yet. The numbers come from
`docs/price-alerts-calibrate.py`, which fetches a month of five-minute
candles from Coinbase (Aug 17 to Sep 16, 2026, UTC) and replays the rule
below; rerun it when a token's temperament changes.*

The Control Strip's ticker shows SOL, PUMP, MET and SKR when you look. This
is the other half: a notification when one of them moves in a way worth
looking at, and never more than four per token in any 24 hours.

## What counts as a move

A move earns a notification when it is rare for that token: about the top
half-percent of its hours, or the top five percent of its days, measured
over the trailing month. In sigma terms that is roughly four hourly sigmas
or two daily sigmas. Rounded to numbers a person can hold in their head:

| token | in one hour | in 24 hours | daily volatility, 30 days | the month replayed |
|---|---|---|---|---|
| SOL  | ±3%  | ±8%  | 3.7%  | 10 alerts on 6 days, busiest day 3 |
| PUMP | ±6%  | ±15% | 8.4%  | 10 alerts on 8 days, busiest day 2 |
| MET  | ±8%  | ±20% | 12.4% | 5 alerts on 4 days, busiest day 2 |
| SKR  | ±10% | ±30% | 17.3% (11.3% over the last 14 days) | 9 alerts on 5 days, the cap hit on the listing days |

Read it as: SOL moving 3% within an hour, or 8% within a day, is news; for
PUMP it takes twice that; MET and SKR are noisier still. A move a token
makes several times a week is not news, whatever the number.

- **Why two windows.** The hour catches a shock (SOL fell 8.4% in the hour
  to 05:40 UTC on Aug 22). The day catches a grind no single hour shows (SOL
  rose 13% over Aug 27 without an hour past the hourly threshold).
- **Why per token.** The same 3% is a four-sigma hour for SOL and an
  ordinary afternoon for SKR. One number for all four would either spam on
  the small caps or never speak about SOL.
- **The SKR caveat.** Its month contains its listing: 0.007 to 0.029 in four
  days, a 24-hour move of +195%. Its thresholds are set for the calmer
  regime since (daily volatility 11%, hours rarely past 9%), which is why
  they sit below what its 30-day distribution alone would say. Re-check
  next month.

## The rule

Everything runs in the daemon, whether or not a desktop is open.

1. **Sample.** Every minute, the spot price of the four USD pairs and the
   three SOL cross rates (for the text), from the same Coinbase call the
   ticker uses; the sampler fills the `/v1/prices` cache, so the tile's own
   polls become free. Keep 25 hours of samples per pair; write them and the
   alert state to `~/.exe/alerts-state.json` each minute so a restart loses
   nothing.
2. **Measure.** For each token, the change against the sample nearest to 60
   minutes ago and to 24 hours ago, within ±3 minutes; a gap means that
   window is skipped this minute. A window is not evaluated until its
   reference exists, so a cold start sends no 24-hour alerts in its first
   day.
3. **Hit.** |1h change| ≥ m·J or |24h change| ≥ m·D, where J and D are the
   token's two thresholds above and m is the escalation multiplier of
   step 7.
4. **Persist.** A hit must hold on two consecutive minutes. That costs a
   minute of latency and buys immunity to a bad tick.
5. **Cool down.** At least 30 minutes since the token's previous alert or
   held-back move.
6. **Re-arm.** The price must have moved at least J, the token's one-hour
   threshold, from the price at the previous alert, in either direction.
   Without this the rolling windows would fire every minute for as long as
   a trend lasts; with it, the next alert says something new: a further
   leg, or a reversal.
7. **Escalate.** Within a sliding 24-hour window, the k-th alert needs a
   bigger move: m = 1, 1.5, 2, 2 for k = 1 to 4. The first alert of the day
   is cheap; a wild day spends its budget on progressively bigger news
   instead of on its first three hours. SOL on Aug 22: a +8.4% day at
   03:20, a +12.8% day at 04:35 (m = 1.5 asked for 12), then the −8.4% hour
   at 05:40, which under m = 2 needed a 6% hour and had it.
8. **Cap.** At most 4 alerts per token in any trailing 24 hours. Sliding,
   not per calendar day, so a burst around midnight cannot double it. A
   move that qualifies while the budget is spent is held back and counted;
   the next alert that is allowed says "3 more moves went unreported since
   18:40".

The cap has a price and the replay shows it. On SKR's listing days the four
alerts were spent by 18:40 UTC on Aug 30, and the biggest hour of the whole
month anywhere, SKR up 35% in the hour to 02:10 UTC on Aug 31, went
unreported until a slot freed. Eight moves were held back over those two
days. Four a day is the ceiling asked for; this is what it costs on the one
day in the month when it binds.

## The notification

Title and body, in the ticker's own number formats:

    SOL −8.4% in the last hour
    $93.58, from $102.20 · 24h +3.9% · 3rd of 4 today

    PUMP +10.3% in the last hour
    $0.004688 = 0.0000493 SOL (+8.1% in SOL) · 24h +21.1% · 1st of 4 today

Ecosystem tokens carry their SOL price and their change in SOL, so a move of
their own can be told from a move of SOL's. Alerts fire on dollar moves
only; a token flat in dollars while SOL moves is not news about that token.

**Channel: Web Push to the installed desktop.** exe is already a PWA with a
service worker (`internal/server/ui/sw.js`); Home Screen web apps receive
push on iOS since 16.4, and Android and desktop browsers always have.

- The daemon makes a VAPID key pair once (`~/.exe/vapid.json`) and serves
  the public key at `GET /v1/push/key`.
- "Notify me of big moves" in the ticker's menu asks the browser for
  permission (it must come from a click), subscribes through the worker's
  push manager, and `POST /v1/push/subscribe` stores the subscription in
  `~/.exe/push.json`, one per browser, so the phone and the laptop each get
  theirs. The same item, checked, turns it off.
- The sampler sends each alert to every subscription: an ES256 VAPID token
  (standard-library ECDSA) and an aes128gcm payload per RFC 8291 (HKDF from
  `golang.org/x/crypto`, already a dependency). A 404 or 410 from the push
  service drops that subscription.
- The worker's `push` handler shows the notification with the token as its
  tag, so a newer alert for the same token replaces the older one instead
  of stacking; `notificationclick` focuses or opens the desktop.
- An open desktop also gets the toast, and the tile shows the move for a
  minute.
- Every alert, delivered or held back, is appended to `~/.exe/alerts.jsonl`;
  a "Recent Moves…" line in the ticker's menu lists the last ten. That file
  is the record if a push never arrives.

Not a channel: the hub (other people's feed) and the Newsfeed (node events,
by rule).

## Calibration

`python3 docs/price-alerts-calibrate.py` fetches 30 days of 5-minute candles
for the four pairs from Coinbase Exchange's public API, prints the move
distributions for the month and for the last 14 days, and replays the whole
rule with the thresholds at the top of the file, listing every alert it
would have sent and every move the cap held back. Change `THRESHOLDS` to
try others; `--cache file.json` reuses a fetch. The replay steps every five
minutes where the daemon samples every minute, so it errs quiet: "two
consecutive samples" is ten minutes there.

Rerun it monthly, or when a token's daily volatility drifts by a third, and
update the table above. Targets when picking: about two alerts a week per
token in a normal month, none on most days, the cap binding only on days
that make the news.

## Open

- Quiet hours (no push between, say, 01:00 and 08:00 local, with held-back
  moves folded into the morning's first alert). Easy to add; not asked for.
- Per-token opt-out in the menu.
- Whether SKR's 24-hour threshold should come down to 25% once its calmer
  regime is a month old.
