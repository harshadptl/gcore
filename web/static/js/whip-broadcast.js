(() => {
  'use strict';

  const root = document.querySelector('[data-whip]');
  if (!root) return;

  const whipURL = root.dataset.whipUrl;
  const preview = root.querySelector('[data-whip-preview]');
  const startBtn = root.querySelector('[data-whip-start]');
  const stopBtn = root.querySelector('[data-whip-stop]');
  const statusEl = root.querySelector('[data-whip-status]');
  // Submitting the title form navigates away and would drop the broadcast.
  const titleBtn = document.querySelector('form[action="/stream/start"] button[type="submit"]');

  let pc = null;
  let stream = null;
  let resourceURL = null;
  let stopped = true;

  const setStatus = (msg) => { statusEl.textContent = msg; };
  const setBusy = (live) => {
    startBtn.disabled = live;
    stopBtn.disabled = !live;
    if (titleBtn) titleBtn.disabled = live;
    root.querySelectorAll('input[name="whip-source"]').forEach((r) => { r.disabled = live; });
  };

  const getMedia = () => {
    const source = root.querySelector('input[name="whip-source"]:checked').value;
    const media = navigator.mediaDevices;
    return source === 'screen'
      ? media.getDisplayMedia({ video: true, audio: true })
      : media.getUserMedia({ video: true, audio: true });
  };

  // Non-trickle WHIP: the offer must already carry the ICE candidates.
  const iceComplete = (conn) => new Promise((resolve) => {
    if (conn.iceGatheringState === 'complete') return resolve();
    const done = () => {
      clearTimeout(timer);
      conn.removeEventListener('icegatheringstatechange', onChange);
      resolve();
    };
    const onChange = () => { if (conn.iceGatheringState === 'complete') done(); };
    const timer = setTimeout(done, 2000);
    conn.addEventListener('icegatheringstatechange', onChange);
  });

  const cleanup = () => {
    if (pc) pc.close();
    if (stream) stream.getTracks().forEach((t) => t.stop());
    pc = stream = resourceURL = null;
    preview.srcObject = null;
    setBusy(false);
  };

  const del = (url) => fetch(url, { method: 'DELETE' }).catch(() => {});

  const stop = async () => {
    if (stopped) return;
    stopped = true;
    if (resourceURL) await del(resourceURL);
    cleanup();
    document.getElementById('stream-stop-form').requestSubmit();
  };

  const start = async () => {
    if (!stopped) return;
    stopped = false;
    setBusy(true);
    setStatus('Requesting media...');
    // Locals so a Stop during any await still lets us release what we acquired afterwards.
    let s, conn, res;
    const abort = () => {
      if (conn) conn.close();
      if (s) s.getTracks().forEach((t) => t.stop());
      if (res) del(res);
    };
    try {
      s = await getMedia();
      stream = s;
      if (stopped) return abort();
      preview.srcObject = s;
      conn = pc = new RTCPeerConnection();
      s.getTracks().forEach((track) => {
        conn.addTransceiver(track, { direction: 'sendonly' });
        track.addEventListener('ended', stop);
      });
      await conn.setLocalDescription(await conn.createOffer());
      if (stopped) return abort();
      await iceComplete(conn);
      if (stopped) return abort();
      setStatus('Connecting...');
      const resp = await fetch(whipURL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/sdp' },
        body: conn.localDescription.sdp,
      });
      if (resp.status !== 201 && resp.status !== 200) throw new Error(`WHIP server replied ${resp.status}`);
      // Location is readable cross-origin only if exposed via CORS; otherwise Stop just closes the PC.
      const loc = resp.headers.get('Location');
      if (loc) res = resourceURL = new URL(loc, whipURL).href;
      const sdp = await resp.text();
      if (stopped) return abort();
      await conn.setRemoteDescription({ type: 'answer', sdp });
      if (stopped) return abort();
      setStatus('You are live.');
    } catch (err) {
      abort();
      if (stopped) return;
      cleanup();
      stopped = true;
      setStatus(`Could not go live: ${err.message || err}`);
    }
  };

  startBtn.addEventListener('click', start);
  stopBtn.addEventListener('click', stop);
})();
