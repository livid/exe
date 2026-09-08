import test from 'node:test';
import assert from 'node:assert/strict';
import {readAudio, requestAudio} from '../../server/sysapps/macos9/core/qemu-audio.js';
import MacAudio from '../../server/sysapps/macos9/audio.js';
globalThis.window = {console};
const {default: Websock} = await import('../../server/sysapps/macos9/core/websock.js');

function reader(bytes) {
  const s = new Websock(); s._rQ = Uint8Array.from(bytes); s._rQlen = bytes.length; s._rQi = 1; return s;
}
const packet = [255, 1, 0, 2, 0, 0, 0, 8, 0, 128, 255, 127, 0, 64, 0, 192];
test('PCM framing survives a split at every byte, followed by another message', () => {
  for (let split = 1; split < packet.length; split++) {
    const s = reader(packet.slice(0, split));
    assert.equal(readAudio(s), null); assert.equal(s._rQi, 0);
    s._rQ = Uint8Array.from([...packet, 255, 1, 0, 0]); s._rQlen = s._rQ.length;
    assert.equal(s.rQshift8(), 255);
    assert.deepEqual([...readAudio(s).data], packet.slice(8));
    assert.equal(s.rQshift8(), 255); assert.deepEqual(readAudio(s), {type:'stop'});
    assert.equal(s._rQi, s._rQlen);
  }
  assert.deepEqual(readAudio(reader([255,1,0,1])), {type:'start'});
});
test('malformed audio messages are rejected before payload allocation', () => {
  for (const bytes of [[255,2,0,1],[255,1,0,3],[255,1,0,2,0,16,0,4],[255,1,0,2,0,0,0,3]]) {
    assert.throws(() => readAudio(reader(bytes)));
  }
});
test('audio is explicitly negotiated and can be disabled', () => {
  const s = new Websock(); s._sQ = new Uint8Array(100); s.flush = () => {};
  requestAudio(s,true);
  assert.deepEqual([...s._sQ.slice(0,s._sQlen)], [255,1,0,2,3,2,0,0,172,68,255,1,0,0]);
  s._sQlen = 0; requestAudio(s,false);
  assert.deepEqual([...s._sQ.slice(0,s._sQlen)], [255,1,0,1]);
});
class Context {
  constructor(){this.state='suspended';this.currentTime=0;this.destination={};this.created=[];this.buffers=[];}
  async resume(){this.state='running';}
  async suspend(){this.state='suspended';}
  createBuffer(channels,frames,rate){const data=Array.from({length:channels},()=>new Float32Array(frames));const b={data,rate,getChannelData:c=>data[c]};this.buffers.push(b);return b;}
  createBufferSource(){const s={connect(){},disconnect(){},stop(){this.stopped=true;},start(at){this.at=at;}};this.created.push(s);return s;}
}
globalThis.window={AudioContext:Context};
test('PCM sign, channel order, start, mute, and bounded buffering', async () => {
  const a=new MacAudio(),bytes=Uint8Array.from(packet.slice(8));
  a.push(bytes);assert.equal(a.context,null);
  assert.equal(await a.start(),true);a.push(bytes);
  assert.deepEqual([...a.context.buffers[0].data[0]],[-1,.5]);
  assert.deepEqual([...a.context.buffers[0].data[1]],[32767/32768,-.5]);
  assert.equal(a.context.buffers[0].rate,44100);
  assert.equal(a.context.created[0].at,.04);
  a.nextTime=2;a.push(bytes);assert.equal(a.context.created[0].stopped,true);
  assert(a.nextTime<.25);a.stop();assert.equal(a.sources.size,0);assert.equal(a.enabled,false);
  const count=a.context.created.length;a.push(bytes);assert.equal(a.context.created.length,count);
  await a.start();a.push(new Uint8Array(100000));assert.equal(a.sources.size,0);
});
test('muting during a pending resume cannot restart playback later', async () => {
  const a=new MacAudio();a.context=new Context();let resume;
  a.context.resume=()=>new Promise(r=>{resume=()=>{a.context.state='running';r();};});
  const started=a.start();a.stop();resume();assert.equal(await started,false);assert.equal(a.enabled,false);
});
