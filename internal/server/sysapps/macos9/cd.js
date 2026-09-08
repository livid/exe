// Disc images are node-local. Always display the drive's reported state so
// ejecting in Finder or changing media from another viewer stays visible.
export default function initCD({token, modal}) {
 const $ = s => document.querySelector(s);
 let state = null, busy = false, loading = false, upload = null, generation = 0;
 let nextCheck = 0, macRunning = false, failure = '', library = '';
 async function request(method = 'GET', body) {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 12000);
  try {
   const r = await fetch('/v1/macos9/cd', {method, cache:'no-store', signal:controller.signal,
    headers:{...(token ? {Authorization:'Bearer '+token} : {}), ...(body ? {'Content-Type':'application/json'} : {})},
    body:body ? JSON.stringify(body) : undefined});
   const text = await r.text();
   let data; try { data = JSON.parse(text); } catch { throw new Error(r.status === 401 ? 'Set your API token in exe’s Special menu, then reopen this app.' : text.trim() || 'Cannot reach the CD drive.'); }
   if (!r.ok) throw Object.assign(new Error(data.message || 'Cannot read the CD drive. Try again.'), {locked:data.locked});
   return data;
  } finally { clearTimeout(timer); }
 }
 function render() {
  $('#cd').hidden = !macRunning;
  const current = failure ? 'CD unavailable' : state?.filename || (state ? 'No CD mounted' : 'Checking CD…');
  $('#cd-name').textContent = current; $('#cd-name').title = current;
  $('#cd').title = 'CD: '+current;
  $('#cd-current').textContent = failure || (state?.filename || (state ? 'No CD mounted' : 'Checking the CD drive…'));
  $('#cd-mount').disabled = busy || !state?.available || !$('#cd-images').value;
  $('#cd-eject').disabled = busy || !state?.filename;
  $('#cd-force').disabled = busy || !state?.filename;
  $('#cd-images').disabled = busy || !state?.images.length;
  $('#cd-upload').textContent = upload ? 'Cancel upload' : 'Upload image…';
  $('#cd-upload').disabled = busy && !upload;
  $('#cd-note').textContent = !macRunning ? 'Start the Mac to mount an image.' : 'Images stay on this computer for your next visit.';
 }
 function apply(data, selected) {
  state = data; failure = '';
  const value = selected || $('#cd-images').value || state.filename;
  const signature = JSON.stringify(state.images);
  if (library !== signature) {
   library = signature;
   $('#cd-images').replaceChildren(...state.images.map(img => new Option(`${img.filename} (${(img.size/1048576).toFixed(1)} MiB)`, img.filename)));
   if (!state.images.length) $('#cd-images').add(new Option('No images available', ''));
  }
  if (!state.filename && !$('#cd-force').hidden) { $('#cd-force').hidden = true; $('#cd-error').textContent = ''; }
  if (state.images.some(img => img.filename === value)) $('#cd-images').value = value;
  render();
 }
 async function refresh(force = false) {
  if (loading || busy || (!force && Date.now() < nextCheck)) return;
  loading = true; const attempt = generation; render();
  try { const data = await request(); if (attempt === generation) apply(data); }
  catch (e) { if (attempt === generation) { failure = e.name === 'AbortError' ? 'CD drive did not respond. Try again.' : e.message; state = null; render(); } }
  finally { loading = false; nextCheck = Date.now()+4000; render(); }
 }
 function showError(e) {
  $('#cd-error').textContent = e.name === 'AbortError' ? 'The CD request timed out. Check the drive and try again.' : e.message;
  $('#cd-force').hidden = !e.locked;
 }
 async function change(filename, force = false) {
  if (busy) return;
  busy = true; ++generation; $('#cd-error').textContent = ''; $('#cd-force').hidden = true; render();
  try { apply(await request('POST', {filename, force})); }
  catch (e) { showError(e); }
  finally { busy = false; render(); await refresh(true); }
 }
 function open(active, restore = true) {
  modal(active, restore); $('#cd').setAttribute('aria-expanded', String(active));
  if (active) refresh(true);
 }
 $('#cd').onclick = () => open(true);
 $('#cd-close').onclick = () => open(false);
 $('#cd-images').onchange = render;
 $('#cd-mount').onclick = () => change($('#cd-images').value);
 $('#cd-eject').onclick = () => change('');
 $('#cd-force').onclick = () => change('', true);
 $('#cd-upload').onclick = () => { if (upload) upload.abort(); else $('#cd-file').click(); };
 $('#cd-file').onchange = () => {
  const file = $('#cd-file').files[0]; $('#cd-file').value = '';
  if (!file || busy) return;
  $('#cd-error').textContent = ''; $('#cd-force').hidden = true;
  if (!/\.(iso|cdr|img|toast)$/i.test(file.name) || !file.size || file.size > 2*1024**3) {
   showError(new Error('Choose an ISO, CDR, IMG, or Toast image up to 2 GiB.')); return;
  }
  busy = true; ++generation;
  const xhr = new XMLHttpRequest(); upload = xhr;
  $('#cd-progress').hidden = false; $('#cd-upload-progress').value = 0; $('#cd-upload-status').textContent = 'Uploading '+file.name+'…'; render();
  xhr.open('POST', '/v1/macos9/cd/upload?filename='+encodeURIComponent(file.name));
  if (token) xhr.setRequestHeader('Authorization', 'Bearer '+token);
  xhr.setRequestHeader('Content-Type', 'application/octet-stream');
  xhr.upload.onprogress = e => { if (e.lengthComputable) { $('#cd-upload-progress').value = e.loaded/e.total; $('#cd-upload-status').textContent = `${file.name} — ${Math.round(e.loaded/e.total*100)}%`; } };
  xhr.onload = async () => {
   upload = null; busy = false; $('#cd-progress').hidden = true;
   if (xhr.status === 201) {
    try { const image = JSON.parse(xhr.responseText); apply(await request(), image.filename); $('#cd-error').textContent = 'Image uploaded. Click Mount to insert it.'; }
    catch (e) { showError(e); }
   } else showError(new Error(xhr.responseText.trim() || 'Upload failed. Try again.'));
   render();
  };
  xhr.onerror = xhr.onabort = () => { upload = null; busy = false; $('#cd-progress').hidden = true; showError(new Error(xhr.status ? 'Upload failed. Try again.' : 'Upload stopped. You can try again.')); render(); };
  xhr.send(file);
 };
 return {refresh, close:restore => open(false, restore), update(running) { macRunning = running; if (!running) { state = null; failure = ''; } render(); }};
}
