const { spawn } = require('node:child_process');
const { mkdir, access } = require('node:fs/promises');
const path = require('node:path');
const { setTimeout: delay } = require('node:timers/promises');

const address = 'http://127.0.0.1:8080';
async function running() {
  try {
    const res = await fetch(address + '/api/v1/node', { signal: AbortSignal.timeout(1000) });
    return res.ok && !!(await res.json()).node_id;
  } catch {
    return false;
  }
}
// Reuse a separately managed local node when one is already serving the journal.
module.exports = async function ensureLocalNode(app) {
  if (!app.isPackaged || (await running())) return;
  const bin = path.join(process.resourcesPath, 'node');
  const executable = path.join(bin, 'loshid-node');
  await access(executable);
  const dir = path.join(app.getPath('userData'), 'journal');
  const models = path.join(dir, 'models');
  await mkdir(dir, { recursive: true });
  const processNode = spawn(executable, ['web'], {
    cwd: dir,
    env: {
      ...process.env,
      DATA_DIR: dir,
      STORAGE_DRIVER: 'sqlite',
      NODE_DSN: path.join(dir, 'node.db'),
      LISTEN_ADDR: '127.0.0.1:8080',
      GRPC_LISTEN_ADDR: '127.0.0.1:0',
      MDNS_ENABLED: 'false',
      SYNC_ENABLED: 'false',
      INFER_ENGINE: 'yolo',
      INFER_MODEL_PATH: path.join(models, 'yolov8n.onnx'),
      INFER_REID_MODEL_DIR: models,
      INFER_RUNTIME_LIB: path.join(
        bin,
        process.platform === 'darwin' ? 'libonnxruntime.dylib' : 'libonnxruntime.so',
      ),
      MIGRATIONS_DIR: path.join(bin, 'migrations'),
    },
    stdio: 'ignore',
    windowsHide: true,
  });
  let failed;
  processNode.once('error', (error) => {
    failed = error;
  });
  app.once('will-quit', () => {
    if (processNode.exitCode === null) processNode.kill('SIGTERM');
  });
  for (let i = 0; i < 70; i++) {
    if (await running()) return;
    if (failed || processNode.exitCode !== null) break;
    await delay(100);
  }
  if (processNode.exitCode === null) processNode.kill('SIGTERM');
  throw failed || new Error('Не удалось запустить локальную ноду');
};
