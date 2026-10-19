(() => {
  'use strict';

  const root = document.querySelector('[data-whip]');
  if (!root) return;

  const whipURL = root.dataset.whipUrl;
  const preview = root.querySelector('[data-whip-preview]');
  const startBtn = root.querySelector('[data-whip-start]');
  const stopBtn = root.querySelector('[data-whip-stop]');
  const statusEl = root.querySelector('[data-whip-status]');

  let pc = null;
  let stream = null;
  let resourceURL = null;
  let stopped = true;

  const setStatus = (msg) => { statusEl.textContent = msg; };
  const setBusy = (live) => {
    startBtn.disabled = live;
    stopBtn.disabled = !live;
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

  const stop = async () => {
    if (stopped) return;
    stopped = true;
    if (resourceURL) {
      try { await fetch(resourceURL, { method: 'DELETE' }); } catch (e) { /* best effort */ }
    }
    cleanup();
    document.getElementById('stream-stop-form').requestSubmit();
  };

  const start = async () => {
    if (!stopped) return;
    stopped = false;
    setBusy(true);
    setStatus('Requesting media...');
    try {
      stream = await getMedia();
      preview.srcObject = stream;
      pc = new RTCPeerConnection();
      stream.getTracks().forEach((track) => {
        pc.addTransceiver(track, { direction: 'sendonly' });
        track.addEventListener('ended', stop);
      });
      await pc.setLocalDescription(await pc.createOffer());
      await iceComplete(pc);
      setStatus('Connecting...');
      const resp = await fetch(whipURL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/sdp' },
        body: pc.localDescription.sdp,
      });
      if (resp.status !== 201 && resp.status !== 200) throw new Error(`WHIP server replied ${resp.status}`);
      // Location is readable cross-origin only if exposed via CORS; otherwise Stop just closes the PC.
      const loc = resp.headers.get('Location');
      if (loc) resourceURL = new URL(loc, whipURL).href;
      await pc.setRemoteDescription({ type: 'answer', sdp: await resp.text() });
      setStatus('You are live.');
    } catch (err) {
      cleanup();
      stopped = true;
      setStatus(`Could not go live: ${err.message || err}`);
    }
  };

  startBtn.addEventListener('click', start);
  stopBtn.addEventListener('click', stop);
})();
