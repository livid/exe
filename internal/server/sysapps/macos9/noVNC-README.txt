noVNC 1.6.0, with local integration changes in core/:
- Preserve fractional mouse coordinates using Pointer Events, with mouse
  fallback and the existing touch gestures; clamp at guest-pixel edges.
- Negotiate QEMU's audio extension and dispatch bounded PCM packets to the
  app's opt-in browser audio player through the existing console connection.
The vendor/ directory is unmodified.
Source: https://github.com/novnc/noVNC/releases/tag/v1.6.0
Copyright and license notices are retained in every source and vendor directory.
