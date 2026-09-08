// QEMU's VNC audio extension. The negotiated stream is signed 16-bit,
// little-endian stereo PCM at 44.1 kHz. Message headers use network byte order.
export function requestAudio(sock, enabled) {
    if (enabled) {
        sock.sQpush8(255); sock.sQpush8(1); sock.sQpush16(2);
        sock.sQpush8(3); sock.sQpush8(2); sock.sQpush32(44100);
    }
    sock.sQpush8(255); sock.sQpush8(1); sock.sQpush16(enabled ? 0 : 1);
    sock.flush();
}

// Called after the outer message byte (255) has been consumed. On a partial
// packet, rewind that byte too so noVNC can retry when more bytes arrive.
export function readAudio(sock) {
    if (sock.rQwait('QEMU audio header', 3, 1)) return null;
    const extension = sock.rQshift8(), type = sock.rQshift16();
    if (extension !== 1 || type > 2) throw new Error('Unsupported QEMU audio message');
    if (type < 2) return {type: type === 1 ? 'start' : 'stop'};
    if (sock.rQwait('QEMU audio length', 4, 4)) return null;
    const length = sock.rQshift32();
    if (length > 1048576 || length % 4 !== 0) throw new Error('Invalid QEMU audio packet length');
    if (sock.rQwait('QEMU audio data', length, 8)) return null;
    return {type: 'data', data: sock.rQshiftBytes(length)};
}
