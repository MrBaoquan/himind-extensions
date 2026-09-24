const fs = require('fs');
const path = require('path');

async function main() {
  const payloadPath = process.env.HIMIND_WECHAT_CI_PAYLOAD;
  if (!payloadPath) throw new Error('HIMIND_WECHAT_CI_PAYLOAD is required');
  const payload = JSON.parse(fs.readFileSync(payloadPath, 'utf8'));
  const projectRoot = payload.project_root;
  const resolved = require.resolve('miniprogram-ci', { paths: [projectRoot, process.cwd()] });
  const ci = require(resolved);
  const project = new ci.Project({
    appid: payload.app_id,
    type: 'miniProgram',
    projectPath: projectRoot,
    privateKeyPath: payload.private_key,
    ignores: ['node_modules/**/*', '.git/**/*', '.himind/**/*'],
  });
  const setting = {
    es6: true,
    minify: true,
    codeProtect: false,
    autoPrefixWXSS: true,
  };
  if (payload.operation === 'preview') {
    fs.mkdirSync(path.dirname(payload.qrcode_output), { recursive: true });
    await ci.preview({
      project,
      desc: payload.description || 'HiMind preview',
      setting,
      qrcodeFormat: 'image',
      qrcodeOutputDest: payload.qrcode_output,
      onProgressUpdate: () => {},
    });
    process.stdout.write(JSON.stringify({
      ok: true,
      operation: 'preview',
      qrcode_output: payload.qrcode_output,
    }));
    return;
  }
  if (payload.operation === 'upload') {
    await ci.upload({
      project,
      version: payload.version,
      desc: payload.description || 'HiMind upload',
      setting,
      onProgressUpdate: () => {},
    });
    process.stdout.write(JSON.stringify({
      ok: true,
      operation: 'upload',
      version: payload.version,
    }));
    return;
  }
  throw new Error(`unsupported operation: ${payload.operation}`);
}

main().catch(error => {
  process.stderr.write(String(error && error.stack ? error.stack : error));
  process.exit(1);
});
