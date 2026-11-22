const { spawn, execFile } = require('node:child_process');
const { mkdir, access, readFile, writeFile } = require('node:fs/promises');
const path = require('node:path');
const { setTimeout: delay } = require('node:timers/promises');
const { BrowserWindow } = require('electron');
// Чистая логика enrollment/first-run (ADR-0014) — электрон-агностичный TS,
// компилируется в build/enroll.cjs (npm run build).
const enroll = require('./build/enroll.cjs');

const address = 'http://127.0.0.1:8080';
const HOST_STATE_FILE = 'node-host.json';
// central по умолчанию; YC_CENTRAL_URL — только для smoke-тестов, в релизе не задаётся.
const centralUrl = process.env.YC_CENTRAL_URL || 'https://yc-hack-horses.cu3rd.ru';

async function running() {
  try {
    const res = await fetch(address + '/api/v1/node', { signal: AbortSignal.timeout(1000) });
    return res.ok && !!(await res.json()).node_id;
  } catch {
    return false;
  }
}

function execFileP(file, args, opts) {
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout: 120_000, ...opts }, (err, stdout, stderr) => {
      if (err) {
        err.stdout = stdout;
        err.stderr = stderr;
        reject(err);
      } else {
        resolve({ stdout, stderr });
      }
    });
  });
}

/** Диалог ввода enrollment-токена (модальное окно с формой). */
function askToken() {
  return new Promise((resolve) => {
    const win = new BrowserWindow({
      width: 520,
      height: 240,
      show: false,
      title: 'Первый запуск · регистрация ноды',
      autoHideMenuBar: true,
      webPreferences: { contextIsolation: true, sandbox: true, nodeIntegration: false },
    });
    const page = `<!doctype html><meta charset="utf-8"><style>
      body{font-family:system-ui,sans-serif;margin:20px;color:#111}
      p{margin:0 0 12px;line-height:1.4}
      input{width:100%;box-sizing:border-box;padding:8px;font-size:14px;margin-bottom:14px}
      .row{display:flex;gap:8px;justify-content:flex-end}
      button{padding:8px 16px;font-size:14px;cursor:pointer}
      code{background:#f2f2f2;padding:0 4px}
    </style>
    <p>Для подключения к серверу введите <b>enrollment-токен</b>, выданный
    оператором (<code>ENROLLMENT_TOKEN</code> центральной ноды).</p>
    <input id="token" placeholder="токен" autofocus>
    <div class="row">
      <button id="skip">Пропустить</button>
      <button id="ok">Подключить</button>
    </div>
    <script>
      ok.addEventListener('click', () => {
        const value = document.getElementById('token').value.trim();
        if (value) { location.hash = encodeURIComponent(value); window.close(); }
      });
      skip.addEventListener('click', () => { location.hash = '#skip'; window.close(); });
      document.getElementById('token').addEventListener('keydown', (e) => {
        if (e.key === 'Enter') ok.click();
      });
    </script>`;
    win.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent(page));
    win.once('ready-to-show', () => win.show());
    let token = null;
    win.on('close', () => {
      try {
        const hash = new URL(win.webContents.getURL()).hash;
        if (hash && hash.length > 1 && hash !== '#skip') token = decodeURIComponent(hash.slice(1));
      } catch {
        // окно закрывается без загруженного контента — токена нет
      }
    });
    win.on('closed', () => resolve(token));
  });
}

async function readState(userData) {
  try {
    return JSON.parse(await readFile(path.join(userData, HOST_STATE_FILE), 'utf8'));
  } catch {
    return {};
  }
}

/**
 * Enrollment против central (ADR-0014): статус серта → токен (сохранённый
 * или диалог) → `loshid-node enroll`. Возвращает пути сертов при успехе,
 * null — если нода остаётся plain (офлайн-деградация, журнал не блокируется).
 */
async function ensureEnrolled(executable, bin, dir, userData, warn) {
  const certPaths = enroll.certPathsIn(dir);
  const baseEnv = {
    ...process.env,
    ...enroll.buildNodeEnv({
      dataDir: dir,
      nodeResourceDir: bin,
      migrationsDir: path.join(bin, 'migrations'),
      platform: process.platform,
      certPaths,
      syncEnabled: false,
      centralUrl,
    }),
  };
  try {
    const { stdout } = await execFileP(executable, ['enroll', '--status'], { env: baseEnv });
    const status = enroll.parseStatus(stdout);
    if (!status.needs_reenroll) return certPaths;
  } catch (error) {
    warn?.('[node-host] enroll --status failed:', error.message);
  }
  const state = await readState(userData);
  let token = typeof state.enrollmentToken === 'string' ? state.enrollmentToken : null;
  if (!token) token = await askToken();
  if (!token) {
    warn?.('[node-host] enrollment-токен не введён — нода без синка с central');
    return null;
  }
  try {
    const { stdout } = await execFileP(executable, ['enroll', '--token', token], { env: baseEnv });
    const status = enroll.parseStatus(stdout);
    if (status.enrolled && !status.needs_reenroll) {
      // Токен запоминаем: пере-enroll за 7 дней до конца срока пройдёт молча.
      await mkdir(userData, { recursive: true });
      await writeFile(
        path.join(userData, HOST_STATE_FILE),
        JSON.stringify({ ...state, enrollmentToken: token }, null, 2),
        'utf8',
      );
      return certPaths;
    }
    warn?.('[node-host] enrollment не выполнен:', status.reason ?? 'неизвестно');
    return null;
  } catch (error) {
    warn?.('[node-host] ошибка enrollment:', error.stderr ?? error.message);
    return null;
  }
}

// Reuse a separately managed local node when one is already serving the journal.
// In the packaged app the bundled node is spawned (ADR-0014); enrollment runs
// against central on first launch, afterwards only when the cert is expiring.
module.exports = async function ensureLocalNode(app) {
  if (!app.isPackaged || (await running())) return;
  const bin = path.join(process.resourcesPath, 'node');
  const executable = path.join(bin, 'loshid-node');
  await access(executable);
  const dir = path.join(app.getPath('userData'), 'journal');
  await mkdir(dir, { recursive: true });
  const certPaths = await ensureEnrolled(executable, bin, dir, app.getPath('userData'), console.warn);
  const processNode = spawn(executable, ['web'], {
    cwd: dir,
    env: {
      // An explicit INFER_MODEL_PATH passes through. The node resolves its default in DATA_DIR.
      ...process.env,
      ...enroll.buildNodeEnv({
        dataDir: dir,
        nodeResourceDir: bin,
        migrationsDir: path.join(bin, 'migrations'),
        platform: process.platform,
        certPaths,
        // Синк с central — только с enrollment-сертификатом (ADR-0014).
        syncEnabled: Boolean(certPaths),
        centralUrl,
      }),
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
