// Schedule short PCM buffers instead of requiring AudioWorklet, which is
// unavailable on the plain HTTP origins used by local exe nodes.
export default class MacAudio {
  constructor() {
    this.context = null;
    this.enabled = false;
    this.generation = 0;
    this.sources = new Set();
    this.nextTime = 0;
  }

  async start() {
    const generation = ++this.generation;
    if (!this.context) {
      const Audio = window.AudioContext || window.webkitAudioContext;
      if (!Audio) throw new Error('Audio playback is unavailable in this browser.');
      this.context = new Audio({latencyHint: 'interactive'});
    }
    // Invoke from a user gesture. A later reconnect can reuse this context.
    await this.context.resume();
    if (generation !== this.generation || this.context.state !== 'running') return false;
    this.enabled = true;
    return true;
  }

  reset() {
    for (const source of this.sources) {
      source.onended = null;
      source.stop(); source.disconnect();
    }
    this.sources.clear(); this.nextTime = 0;
  }

  stop() {
    ++this.generation; this.enabled = false; this.reset();
    this.context?.suspend().catch(() => {});
  }

  push(bytes) {
    if (!this.enabled || this.context?.state !== 'running' || !bytes.length) return;
    const frames = bytes.length / 4;
    // Bound latency and memory even after background-tab throttling or a
    // delayed connection. Old sound must not play when the user returns.
    if (!Number.isInteger(frames) || frames > 22050) { this.reset(); return; }
    const now = this.context.currentTime;
    if (this.nextTime > now + .25) this.reset();
    const buffer = this.context.createBuffer(2, frames, 44100);
    const data = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    for (let channel = 0; channel < 2; channel++) {
      const samples = buffer.getChannelData(channel);
      for (let i = 0; i < frames; i++) samples[i] = data.getInt16(i * 4 + channel * 2, true) / 32768;
    }
    const source = this.context.createBufferSource();
    source.buffer = buffer; source.connect(this.context.destination);
    source.onended = () => { this.sources.delete(source); source.disconnect(); };
    this.sources.add(source);
    const start = Math.max(now + .04, this.nextTime);
    source.start(start); this.nextTime = start + frames / 44100;
  }
}
