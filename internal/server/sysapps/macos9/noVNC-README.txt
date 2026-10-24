noVNC 1.6.0, with local integration changes in core/:
- Preserve fractional mouse coordinates using Pointer Events, with mouse
  fallback and the existing touch gestures; clamp at guest-pixel edges.
- Negotiate QEMU's audio extension and dispatch bounded PCM packets to the
  app's opt-in browser audio player through the existing console connection.
- pointerTrim (rfb.js, _sendMouse and pointerTarget): send a guest pixel as
  its place inside the part of an absolute pointer's range the guest uses
  (Mac OS X 10.4.11 leaves 7.5% unused at each edge).
- framesInFlight (rfb.js, after the first update request): send that many update
  requests at the start; a server that answers each one (exe's QEMU, from
  /www/exe-qegl's vnc-pacing.patch) keeps that many frames in flight.
The vendor/ directory is unmodified.
Source: https://github.com/novnc/noVNC/releases/tag/v1.6.0
Copyright and license notices are retained in every source and vendor directory.
