import puppeteer from 'puppeteer-core';
import http from 'node:http';
import {spawn} from 'node:child_process';
import {createReadStream, existsSync, statSync} from 'node:fs';
import {mkdir, readFile, rm, writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const ROOT = path.dirname(fileURLToPath(import.meta.url));
const FPS = 30;
const WIDTH = 1080;
const HEIGHT = 1920;

const args = {};
for (const raw of process.argv.slice(2)) {
  if (!raw.startsWith('--')) continue;
  const [key, ...rest] = raw.slice(2).split('=');
  args[key] = rest.length ? rest.join('=') : true;
}

let extraChromeArgs = [];
try {
  extraChromeArgs = JSON.parse(process.env.HIMIND_CHROME_ARGS_JSON || '[]');
} catch { extraChromeArgs = []; }

function findChrome() {
  if (args.chrome) return String(args.chrome);
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const candidates = [];
  if (process.platform === 'win32') {
    for (const root of [process.env.ProgramFiles, process.env['ProgramFiles(x86)'], process.env.LOCALAPPDATA]) {
      if (!root) continue;
      candidates.push(path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe'));
      candidates.push(path.join(root, 'Microsoft', 'Edge', 'Application', 'msedge.exe'));
    }
  } else if (process.platform === 'darwin') {
    candidates.push('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome');
    candidates.push('/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge');
  } else {
    candidates.push('/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/microsoft-edge');
  }
  return candidates.find((candidate) => existsSync(candidate)) || '';
}

function parseTimes(value, fallback) {
  const source = String(value || fallback);
  return source.split(',').map((item) => Number.parseFloat(item.trim())).filter((value) => Number.isFinite(value));
}

function parseRange(value, fallback) {
  const source = String(value || fallback);
  const [start, end] = source.split(':').map((item) => Number.parseFloat(item.trim()));
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
    throw new Error(`invalid range: ${source}`);
  }
  return [start, end];
}

function mimeFor(file) {
  const ext = path.extname(file).toLowerCase();
  return {
    '.html': 'text/html; charset=utf-8',
    '.js': 'text/javascript; charset=utf-8',
    '.css': 'text/css; charset=utf-8',
    '.json': 'application/json; charset=utf-8',
    '.png': 'image/png',
    '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg',
    '.webp': 'image/webp',
    '.m4a': 'audio/mp4',
    '.mp3': 'audio/mpeg',
    '.wav': 'audio/wav',
  }[ext] || 'application/octet-stream';
}

function startServer() {
  const server = http.createServer((req, res) => {
    const requestPath = decodeURIComponent((req.url || '/').split('?')[0]);
    const resolved = path.resolve(ROOT, '.' + requestPath);
    if (!resolved.startsWith(ROOT)) {
      res.writeHead(403).end('forbidden');
      return;
    }
    let file = resolved;
    if (existsSync(file) && statSync(file).isDirectory()) file = path.join(file, 'index.html');
    if (!existsSync(file)) {
      res.writeHead(404).end('not found');
      return;
    }
    res.writeHead(200, {'content-type': mimeFor(file), 'cache-control': 'no-store'});
    createReadStream(file).pipe(res);
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      resolve({server, port: address.port});
    });
  });
}

function run(command, commandArgs, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, commandArgs, {stdio: ['ignore', 'pipe', 'pipe'], ...options});
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk.toString(); });
    child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
    child.on('error', reject);
    child.on('close', (code) => {
      if (code === 0) resolve({stdout, stderr});
      else reject(new Error(`${command} exited ${code}\n${stderr || stdout}`));
    });
  });
}

async function openPage(browser, port) {
  const page = await browser.newPage();
  await page.setViewport({width: WIDTH, height: HEIGHT, deviceScaleFactor: 1});
  await page.goto(`http://127.0.0.1:${port}/reel/index.html`, {waitUntil: 'networkidle0'});
  await page.waitForFunction('window.__READY__ === true', {timeout: 30000});
  return page;
}

async function shot(page, time, type = 'jpeg', quality = 0.94) {
  // renderAt() draws synchronously; do not wait for requestAnimationFrame.
  // In headless Chrome, background pages can throttle RAF and hang workers.
  await page.evaluate((t) => window.renderAt(t), time);
  return page.screenshot({type, ...(type === 'jpeg' ? {quality} : {})});
}

async function tileImages(inputPattern, output, cols, rows) {
  const ffmpeg = process.env.FFMPEG_PATH || 'ffmpeg';
  await mkdir(path.dirname(output), {recursive: true});
  await run(ffmpeg, [
    '-y', '-hide_banner', '-loglevel', 'error',
    '-framerate', '1', '-start_number', '0', '-i', inputPattern,
    '-vf', `tile=${cols}x${rows}:padding=8:margin=8`,
    '-frames:v', '1', output,
  ]);
}

async function main() {
  const chrome = findChrome();
  if (!chrome) throw new Error('Chrome not found: pass --chrome=<path> or set CHROME_PATH');
  if (args.encode) {
    const framesDir = path.resolve(ROOT, args['frames-dir'] || 'out/frames');
    const requestedFormat = String(args['frame-format'] || '').toLowerCase();
    const hasPng = existsSync(path.join(framesDir, 'f00000.png'));
    const frameExt = (requestedFormat === 'jpg' || requestedFormat === 'jpeg') ? 'jpg' : (requestedFormat === 'png' ? 'png' : (hasPng ? 'png' : 'jpg'));
    const frames = existsSync(framesDir)
      ? (await import('node:fs')).readdirSync(framesDir).filter((name) => name.startsWith('f') && name.endsWith('.' + frameExt)).sort()
      : [];
    if (!frames.length) throw new Error(`no frames in ${framesDir}; run --frames first`);
    const output = path.resolve(ROOT, args.out || 'out/video.mp4');
    await mkdir(path.dirname(output), {recursive: true});
    const ffmpeg = process.env.FFMPEG_PATH || 'ffmpeg';
    const commandArgs = [
      '-y', '-hide_banner', '-loglevel', 'error',
      '-framerate', String(FPS), '-start_number', '0',
      '-i', path.join(framesDir, 'f%05d.' + frameExt),
    ];
    if (args.audio) commandArgs.push('-i', path.resolve(ROOT, args.audio));
    commandArgs.push('-c:v', 'libx264', '-preset', 'medium', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart');
    if (args.audio) commandArgs.push('-c:a', 'aac', '-b:a', '192k', '-shortest');
    commandArgs.push(output);
    await run(ffmpeg, commandArgs);
    console.log(output);
    return;
  }

  const {server, port} = await startServer();
  const browser = await puppeteer.launch({
    executablePath: chrome,
    headless: 'new', protocolTimeout: 180000,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--hide-scrollbars', '--force-device-scale-factor=1', ...extraChromeArgs],
  });
  try {
    if (args.frames) {
      const frameDir = path.resolve(ROOT, args['frames-dir'] || 'out/frames');
      // A shortened duration or changed resolution must not reuse stale frame files.
      await rm(frameDir, {recursive: true, force: true});
      await mkdir(frameDir, {recursive: true});
      const duration = Number(args.duration || 10);
      const workers = Math.max(1, Number(args.workers || 1));
      const total = Math.round(duration * FPS);
      const frameFormat = String(args['frame-format'] || 'png').toLowerCase() === 'jpg' ? 'jpg' : 'png';
      const pages = await Promise.all(Array.from({length: workers}, () => openPage(browser, port)));
      let next = 0;
      let done = 0;
      const started = Date.now();
      await Promise.all(pages.map(async (page) => {
        while (next < total) {
          const index = next++;
          const buffer = await shot(page, index / FPS, frameFormat, 0.94);
          await writeFile(path.join(frameDir, 'f' + String(index).padStart(5, '0') + '.' + frameFormat), buffer);
          done += 1;
          if (done % 30 === 0 || done === total) {
            console.log(`${done}/${total} frames (${Math.round((Date.now() - started) / done)} ms/frame)`);
          }
        }
      }));
      return;
    }

    const page = await openPage(browser, port);
    if (args.sheet) {
      const times = parseTimes(args.sheet, '1,3,5,7,9');
      const cols = Math.max(1, Number(args.cols || 3));
      const rows = Math.ceil(times.length / cols);
      const temp = path.resolve(ROOT, `.tmp-sheet-${process.pid}`);
      await mkdir(temp, {recursive: true});
      for (let i = 0; i < times.length; i += 1) {
        await writeFile(path.join(temp, `f${String(i).padStart(3, '0')}.jpg`), await shot(page, times[i], 'jpeg', 0.95));
      }
      await tileImages(path.join(temp, 'f%03d.jpg'), path.resolve(ROOT, args.out || 'out/check/sheet.jpg'), cols, rows);
      await rm(temp, {recursive: true, force: true});
      console.log(path.resolve(ROOT, args.out || 'out/check/sheet.jpg'));
      return;
    }
    if (args.strip) {
      const [start, end] = parseRange(args.strip, '2:3');
      const cols = Math.max(1, Number(args.cols || 6));
      const times = [];
      for (let t = start; t < end + 1e-6; t += 1 / FPS) times.push(Number(t.toFixed(4)));
      const capped = times.slice(0, 120);
      const rows = Math.ceil(capped.length / cols);
      const temp = path.resolve(ROOT, `.tmp-strip-${process.pid}`);
      await mkdir(temp, {recursive: true});
      for (let i = 0; i < capped.length; i += 1) {
        await writeFile(path.join(temp, `f${String(i).padStart(3, '0')}.jpg`), await shot(page, capped[i], 'jpeg', 0.93));
      }
      await tileImages(path.join(temp, 'f%03d.jpg'), path.resolve(ROOT, args.out || 'out/check/strip.jpg'), cols, rows);
      await rm(temp, {recursive: true, force: true});
      console.log(path.resolve(ROOT, args.out || 'out/check/strip.jpg'));
      return;
    }
    if (args.stills) {
      const times = parseTimes(args.stills, '2,5,8');
      const outputDir = path.resolve(ROOT, args.out || 'out/stills');
      await mkdir(outputDir, {recursive: true});
      for (let i = 0; i < times.length; i += 1) {
        const buffer = await shot(page, times[i], 'png');
        await writeFile(path.join(outputDir, `still-${String(i + 1).padStart(4, '0')}.png`), buffer);
      }
      console.log(outputDir);
      return;
    }
    throw new Error('one of --sheet, --strip, --stills, --frames or --encode is required');
  } finally {
    await browser.close();
    server.close();
  }
}

main().catch((error) => {
  console.error(error?.stack || String(error));
  process.exit(1);
});
