<!--
What changed since the last release, as Software Update will show it under
"What changed in <version>". `deploy/release.sh notes` prints and checks it;
docs/release.md, "The notes", says how it is written. What every release
before it said is kept in CHANGELOG.md.
-->
- **Mac OS 9 and Mac OS X** work again on a desk opened over plain HTTP at a network address.
- **Mac OS X Intel**, new on Linux x86-64: Tiger 10.4.11 for Intel Macs, two cores at native speed under KVM.
- **VMs keep running** when exe restarts, on Linux under systemd. A shutdown of the host still stops them.
- **exe expose** puts a host's tunnel rule ahead of any wildcard rule in the tunnel, and moves one it finds behind a wildcard, so a `*.zone` rule for another app no longer takes the host's traffic.
- **Hub** posts take emoji reactions from the desk: click one under a post to give it too, click the smiley button on the post's foot line to pick another, and right-click a reaction to see who gave it.
- **Solana**, a new built-in app, is a trading chart of SOL, PUMP, MET or SKR: candles or a line, 1m to 1d, dragged back through Coinbase's history, volume and RSI under it, Bollinger Bands over it, with each big move marked. Open it from the Solana ticker's menu, or click a big-move notice.
- **Desktop over HTTPS** in the Control Strip's Tailscale menu puts the desktop at its tailnet HTTPS address.
- **Icon** in Configuration → General: 64 pixel-art icons, one per computer, for its tab and Home Screen.
