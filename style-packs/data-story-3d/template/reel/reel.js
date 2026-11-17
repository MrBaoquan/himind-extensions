import * as THREE from '../node_modules/three/build/three.module.js';

const W = 1080, H = 1920, FPS = 30; let DURATION = 10;
const webglCanvas = document.getElementById('webgl');
const overlay = document.getElementById('overlay');
const ctx = overlay.getContext('2d');
const DEFAULT = {
  title: '数据在三维空间里生长',
  subtitle: '3D DATA STORY / 2026',
  unit: '单位：指数',
  items: [
    {name: '华东', value: 92}, {name: '华南', value: 81}, {name: '华北', value: 74}, {name: '西南', value: 63}, {name: '东北', value: 52},
  ],
  source: 'SOURCE / HIMIND SAMPLE DATA',
};
let DATA = DEFAULT;
async function loadData() {
  try {
    const response = await fetch('/input/data.json', {cache: 'no-store'});
    if (response.ok) {
      const value = await response.json();
      const items = Array.isArray(value.items) ? value.items.slice(0, 6).filter((x) => x && x.name != null).map((x) => ({name:String(x.name), value:Number(x.value || 0)})) : [];
      if (items.length) {
        const duration = Number(value.duration);
        if (Number.isFinite(duration) && duration > 0 && duration <= 30) DURATION = duration;
        DATA = {title:String(value.title || DEFAULT.title), subtitle:String(value.subtitle || DEFAULT.subtitle), unit:String(value.unit || DEFAULT.unit), items, source:String(value.source || DEFAULT.source)};
      }
    }
  } catch { DATA = DEFAULT; }
}
const clamp = (v, a, b) => Math.max(a, Math.min(b, v));
const ease = (v) => 1 - Math.pow(1 - clamp(v, 0, 1), 3);
const hash = (n) => { const x = Math.sin(n * 127.1 + 311.7) * 43758.5453; return x - Math.floor(x); };
const noiseCanvas = document.createElement('canvas'); noiseCanvas.width = 128; noiseCanvas.height = 128;
const noiseContext = noiseCanvas.getContext('2d'); const noiseImage = noiseContext.createImageData(128, 128);
for (let i = 0; i < noiseImage.data.length; i += 4) {
  const value = Math.round(hash(i + 1) * 255);
  noiseImage.data[i] = value; noiseImage.data[i + 1] = value; noiseImage.data[i + 2] = value; noiseImage.data[i + 3] = 255;
}
noiseContext.putImageData(noiseImage, 0, 0);
const noisePattern = ctx.createPattern(noiseCanvas, 'repeat');
const C = {ink:'#f1fbff',muted:'rgba(241,251,255,.58)',dim:'rgba(241,251,255,.28)',cyan:'#5ce1ff',green:'#5dffb0',gold:'#f4c96b',pink:'#ff6a9f'};

const renderer = new THREE.WebGLRenderer({canvas: webglCanvas, antialias: true, preserveDrawingBuffer: true, powerPreference: 'high-performance'});
renderer.setPixelRatio(1); renderer.setSize(W, H, false); renderer.outputColorSpace = THREE.SRGBColorSpace; renderer.toneMapping = THREE.ACESFilmicToneMapping; renderer.toneMappingExposure = 1.1;
const scene = new THREE.Scene(); scene.background = new THREE.Color(0x060a12); scene.fog = new THREE.Fog(0x060a12, 12, 30);
const camera = new THREE.PerspectiveCamera(38, W / H, 0.1, 100); camera.position.set(8.2, 6.0, 14.2); camera.lookAt(0, 1.8, 0);
scene.add(new THREE.AmbientLight(0x2d4b5f, 2.0));
const key = new THREE.DirectionalLight(0xffffff, 3.0); key.position.set(6, 9, 8); scene.add(key);
const cyan = new THREE.PointLight(0x5ce1ff, 28, 20, 2); cyan.position.set(-5, 2, 4); scene.add(cyan);
const pink = new THREE.PointLight(0xff6a9f, 20, 20, 2); pink.position.set(5, 1, 3); scene.add(pink);
const grid = new THREE.GridHelper(16, 24, 0x1a4050, 0x0e202b); grid.position.y = 0; scene.add(grid);
const floor = new THREE.Mesh(new THREE.PlaneGeometry(18, 18), new THREE.MeshBasicMaterial({color:0x071018, transparent:true, opacity:0.7})); floor.rotation.x = -Math.PI / 2; floor.position.y = -0.02; scene.add(floor);

const bars = [];
function buildBars() {
  const maxValue = Math.max(...DATA.items.map((x) => x.value), 1);
  DATA.items.forEach((item, i) => {
    const h = 0.7 + (item.value / maxValue) * 3.6;
    const color = i === 0 ? 0xf4c96b : i === 1 ? 0x5dffb0 : 0x5ce1ff;
    const mat = new THREE.MeshPhysicalMaterial({color, metalness:0.35, roughness:0.28, emissive:new THREE.Color(color), emissiveIntensity:0.12});
    const mesh = new THREE.Mesh(new THREE.BoxGeometry(0.82, 1, 0.82), mat);
    const x = (i - (DATA.items.length - 1) / 2) * 1.65; mesh.position.set(x, 0, 0);
    const edge = new THREE.LineSegments(new THREE.EdgesGeometry(mesh.geometry), new THREE.LineBasicMaterial({color, transparent:true, opacity:0.7}));
    const group = new THREE.Group(); group.add(mesh); group.add(edge); group.position.x = x; scene.add(group);
    bars.push({group, mesh, edge, height: Math.min(h, 5.4), value:item.value, name:item.name});
  });
}
const particles = new Float32Array(180 * 3);
for (let i = 0; i < 180; i++) { particles[i*3] = (hash(i+1)-.5)*18; particles[i*3+1] = hash(i+101)*8; particles[i*3+2] = (hash(i+201)-.5)*18; }
const particleGeo = new THREE.BufferGeometry(); particleGeo.setAttribute('position', new THREE.BufferAttribute(particles, 3));
const points = new THREE.Points(particleGeo, new THREE.PointsMaterial({color:0x5ce1ff, size:0.035, transparent:true, opacity:0.35})); scene.add(points);

function drawOverlay(t) {
  ctx.clearRect(0, 0, W, H);
  const p = ease(t / 1.2); ctx.save(); ctx.globalAlpha = p; ctx.fillStyle = C.ink; ctx.font = '700 66px "Microsoft YaHei", sans-serif'; ctx.fillText(DATA.title, 80, 230); ctx.fillStyle = C.cyan; ctx.font = '600 21px "Courier New",monospace'; ctx.fillText(DATA.subtitle, 84, 280); ctx.fillStyle = C.muted; ctx.font = '500 18px "Microsoft YaHei", sans-serif'; ctx.fillText(DATA.unit, 84, 316); ctx.strokeStyle = 'rgba(92,225,255,.5)'; ctx.lineWidth = 3; ctx.beginPath(); ctx.moveTo(84, 350); ctx.lineTo(84 + 400*p, 350); ctx.stroke(); ctx.restore();
  ctx.save(); ctx.fillStyle = C.muted; ctx.font = '500 17px "Courier New",monospace'; ctx.fillText('HIMIND / 3D DATA STORY', 80, 104); ctx.textAlign = 'right'; ctx.fillText('VIEW 03', W - 80, 104); ctx.textAlign = 'left'; ctx.restore();
  const top = bars[0];
  if (top) { const cp = ease((t - 7.0) / 0.7); if (cp > 0) { ctx.save(); ctx.globalAlpha = cp; ctx.fillStyle = C.gold; ctx.font = '700 20px "Courier New",monospace'; ctx.fillText('TOP SIGNAL', 84, 1110); ctx.fillStyle = C.ink; ctx.font = '700 46px "Microsoft YaHei", sans-serif'; ctx.fillText(`${top.name} / ${top.value}`, 84, 1168); ctx.strokeStyle = C.gold; ctx.lineWidth = 3; ctx.beginPath(); ctx.moveTo(84, 1194); ctx.lineTo(480, 1194); ctx.stroke(); ctx.restore(); } }
  const baseY = 1630; const colW = 920 / Math.max(bars.length, 1);
  bars.forEach((bar, i) => { const lp = ease((t - (1.8 + i * 0.45)) / 0.7); if (lp <= 0) return; const x = 80 + i * colW; ctx.save(); ctx.globalAlpha = lp; ctx.fillStyle = i === 0 ? C.gold : C.dim; ctx.fillRect(x, baseY - 6, Math.max(4, colW - 22), 6); ctx.fillStyle = C.muted; ctx.font = '500 17px "Microsoft YaHei", sans-serif'; ctx.fillText(bar.name, x, baseY + 34); ctx.fillStyle = i === 0 ? C.gold : C.ink; ctx.font = '700 27px "Courier New",monospace'; ctx.fillText(String(bar.value), x, baseY + 72); ctx.restore(); });
  ctx.save(); ctx.fillStyle = C.muted; ctx.font = '500 17px "Courier New",monospace'; ctx.fillText(DATA.source, 80, 1815); ctx.fillStyle = C.cyan; ctx.fillRect(80, 1780, 180, 4); ctx.restore();
  ctx.save(); ctx.fillStyle = noisePattern; ctx.globalAlpha = 0.045; ctx.fillRect(0, 0, W, H); ctx.restore();
}
function rebuildBars() {
  for (const bar of bars) { scene.remove(bar.group); bar.group.traverse((child) => { if (child.geometry) child.geometry.dispose(); if (child.material) child.material.dispose(); }); }
  bars.length = 0; buildBars();
}
function renderAt(t) {
  const time = clamp(t, 0, DURATION);
  const orbit = Math.sin(time * 0.22) * 0.18;
  camera.position.x = 8.2 + orbit * 2.0; camera.position.z = 14.2 - orbit * 1.2; camera.lookAt(0, 1.8, 0);
  bars.forEach((bar, i) => { const p = ease((time - (1.2 + i * 0.32)) / 1.15); const h = Math.max(0.02, bar.height * p); bar.group.scale.y = h; bar.group.position.y = 0; bar.mesh.position.y = 0.5; bar.edge.position.y = 0.5; });
  points.rotation.y = time * 0.015;
  renderer.render(scene, camera);
  drawOverlay(time);
}
window.renderAt = renderAt; window.__FPS__ = FPS;
loadData().then(() => { rebuildBars(); window.__DURATION__ = DURATION; document.fonts.ready.then(() => { renderAt(0); window.__READY__ = true; }); });
