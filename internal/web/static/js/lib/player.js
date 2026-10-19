// Video player in the app's style. player(src, {autoplay, poster, focusKeys, onError}) → {el, video, pause, keys(), dispose}.
// Its keys are a scope of their own: from keys() until dispose (a modal's player), or while it has the focus
// (focusKeys, one in the text). Space/k play, j l 10s (← → stay the carousel's), Home End, m mute, f full screen, p picture-in-picture (where the browser has it), , . a frame, < > speed.
// It is 16:9 until the video says otherwise; --player-max-h caps its height. With a poster it fetches nothing
// before it plays.
import { h } from './dom.js';
import { icon } from './icons.js';
import { keys as km } from './keys.js';
import { T } from './i18n.js';

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const FRAME = 1 / 30;
const clock = s => { s = Math.max(0, Math.floor(s || 0)); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };

export function player(src, { autoplay = false, poster, focusKeys = false, onError } = {}) {
  const video = h('video', { src, autoplay, poster, playsInline: true, preload: poster && !autoplay ? 'none' : 'metadata', onerror: () => onError && onError() });
  const btn = (name, title, fn) => h('button.btn.ghost.sm.player-btn', { type: 'button', title, 'aria-label': title, onclick: e => { e.stopPropagation(); fn(); } }, icon(name));
  const play = btn('play', T('Play (space)'), () => toggle());
  const mute = btn('volume-2', T('Mute (m)'), () => { video.muted = !video.muted; });
  const full = btn('maximize-2', T('Full screen (f)'), () => fullscreen());
  const canPip = !!document.pictureInPictureEnabled && !video.disablePictureInPicture;
  const pip = canPip && btn('picture-in-picture-2', T('Picture-in-picture (p)'), () => pictureInPicture());
  const speed = h('button.btn.ghost.sm.player-btn.player-speed', { type: 'button', title: T('Speed (< >)'), onclick: e => { e.stopPropagation(); rate(1); } }, '1×');
  const time = h('span.player-time.mono', '0:00 / 0:00');
  const buf = h('div.player-buf'), fill = h('div.player-fill');
  const track = h('div.player-track', { role: 'slider', 'aria-label': T('Position'), 'aria-valuemin': 0 }, buf, fill);
  const big = h('div.player-big', icon('play'));
  const bar = h('div.player-bar', { onclick: e => e.stopPropagation() }, play, time, track, speed, mute, pip, full);
  const el = h('div.player.paused', { tabIndex: 0, onclick: () => toggle(), ondblclick: () => fullscreen() }, video, big, bar);

  const swap = (b, name) => b.replaceChildren(icon(name));
  const toggle = () => (video.paused || video.ended ? video.play().catch(() => {}) : video.pause());
  const seek = t => { if (isFinite(video.duration)) video.currentTime = Math.min(video.duration, Math.max(0, t)); wake(); };
  const rate = d => { const i = SPEEDS.indexOf(video.playbackRate); video.playbackRate = SPEEDS[Math.min(SPEEDS.length - 1, Math.max(0, (i < 0 ? 2 : i) + d))] || 1; wake(); };
  const pictureInPicture = () => (document.pictureInPictureElement === video ? document.exitPictureInPicture() : video.requestPictureInPicture()).catch(() => {});
  const fullscreen = () => (document.fullscreenElement ? document.exitFullscreen() : el.requestFullscreen()).catch(() => {});
  const draw = () => {
    const d = video.duration, t = video.currentTime;
    fill.style.width = (d ? (t / d) * 100 : 0) + '%';
    time.textContent = clock(t) + ' / ' + clock(d);
    track.setAttribute('aria-valuemax', Math.floor(d || 0)); track.setAttribute('aria-valuenow', Math.floor(t));
    const b = video.buffered;
    for (let i = 0; i < b.length; i++) if (b.start(i) <= t && t <= b.end(i)) buf.style.width = (d ? (b.end(i) / d) * 100 : 0) + '%';
  };
  const state = () => {
    el.classList.toggle('paused', video.paused);
    swap(play, video.paused ? 'play' : 'pause'); play.title = video.paused ? T('Play (space)') : T('Pause (space)');
    swap(mute, video.muted ? 'volume-x' : 'volume-2');
    speed.textContent = video.playbackRate + '×';
  };
  for (const ev of ['play', 'pause', 'ended', 'volumechange', 'ratechange']) video.addEventListener(ev, state);
  for (const ev of ['timeupdate', 'durationchange', 'progress', 'seeked']) video.addEventListener(ev, draw);
  el.addEventListener('fullscreenchange', () => swap(full, document.fullscreenElement === el ? 'shrink' : 'maximize-2'));
  video.addEventListener('loadedmetadata', () => { if (video.videoWidth && video.videoHeight) el.style.setProperty('--ar', video.videoWidth / video.videoHeight); });

  // Dragging the track seeks.
  const at = e => { const r = track.getBoundingClientRect(); return ((e.clientX - r.left) / r.width) * video.duration; };
  track.addEventListener('pointerdown', e => { track.setPointerCapture(e.pointerId); seek(at(e)); });
  track.addEventListener('pointermove', e => { if (track.hasPointerCapture(e.pointerId)) seek(at(e)); });

  // The bar fades while playing and the pointer rests.
  let idle = 0;
  function wake() { el.classList.remove('idle'); clearTimeout(idle); idle = setTimeout(() => { if (!video.paused) el.classList.add('idle'); }, 2000); }
  el.addEventListener('pointermove', wake);
  video.addEventListener('play', wake);

  let scope = null;
  function bindKeys() {
    const s = km.scope('video', { layer: 3 });
    const o = { group: 'Video', when: () => el.isConnected };
    s.bind(['Space', 'k'], () => { toggle(); wake(); }, T('play / pause'), o);
    s.bind('j', () => seek(video.currentTime - 10), T('back 10s'), o);
    s.bind('l', () => seek(video.currentTime + 10), T('forward 10s'), o);
    s.bind('Home', () => seek(0), T('to the start'), o);
    s.bind('End', () => seek(video.duration), T('to the end'), o);
    s.bind(',', () => { video.pause(); seek(video.currentTime - FRAME); }, T('frame back'), o);
    s.bind('.', () => { video.pause(); seek(video.currentTime + FRAME); }, T('frame forward'), o);
    s.bind('<', () => rate(-1), T('slower'), o);
    s.bind('>', () => rate(1), T('faster'), o);
    s.bind('m', () => { video.muted = !video.muted; wake(); }, T('mute'), o);
    s.bind('f', fullscreen, T('full screen'), o);
    if (canPip) s.bind('p', pictureInPicture, T('picture-in-picture'), o);
    return s;
  }
  const keys = () => { if (!scope) scope = bindKeys(); };
  const unkeys = () => { if (scope) scope.dispose(); scope = null; };
  if (focusKeys) {
    el.addEventListener('focusin', keys);
    el.addEventListener('focusout', e => { if (!el.contains(e.relatedTarget)) unkeys(); });
  }
  state();
  return {
    el, video, keys,
    pause: () => video.pause(),
    // One in picture-in-picture plays on there.
    dispose() { if (document.pictureInPictureElement !== video) video.pause(); clearTimeout(idle); unkeys(); },
  };
}
