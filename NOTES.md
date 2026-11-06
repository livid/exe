<!--
What changed since the last release, as Software Update will show it under
"What changed in <version>". `deploy/release.sh notes` prints and checks it;
docs/release.md, "The notes", says how it is written. What every release
before it said is kept in CHANGELOG.md.
-->
- **Mac OS X Intel**, new on Linux x86-64: Tiger 10.4.11 for Intel Macs, at native speed under KVM.
- **VMs keep running** when exe restarts, on Linux under systemd. A shutdown of the host still stops them.
- **exe expose** puts a host's tunnel rule ahead of any wildcard rule in the tunnel, and moves one it finds behind a wildcard, so a `*.zone` rule for another app no longer takes the host's traffic.
- **Hub** posts take emoji reactions from the desk: click one under a post to give it too, click the smiley button on the post's foot line to pick another, and right-click a reaction to see who gave it.
- **A link** can open an app: add `#show=app:Ping` to the desktop's address and Ping's window comes forward.
- **Big price moves** of the Solana ticker stay on the desktop as a notice, with the time of the move, until you close it, instead of a toast that fades.
- **Solana**, a new built-in app, is a trading chart of SOL, PUMP, MET or SKR: candles or a line, 1m to 1d, dragged back through Coinbase's history, volume and RSI under it, Bollinger Bands over it, with each big move marked. Open it from the Solana ticker's menu, or click a big-move notice.
- **Install tmux…** on Welcome's Claude Code and Codex card: an agent's session outlives its window.
