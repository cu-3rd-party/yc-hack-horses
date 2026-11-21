const { app, BrowserWindow, ipcMain, net, protocol, Menu } = require('electron');
const { mkdtemp, readFile, rm, writeFile } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const run = promisify(execFile);
const startURL = 'journal://app/';
protocol.registerSchemesAsPrivileged([
  {
    scheme: 'journal',
    privileges: {
      standard: true,
      secure: true,
      supportFetchAPI: true,
      corsEnabled: true,
      stream: true,
    },
  },
]);

function openJournal() {
  const win = new BrowserWindow({
    title: 'ЛошID · Полевой журнал',
    width: 1340,
    height: 880,
    minWidth: 900,
    minHeight: 600,
    backgroundColor: '#ffffff',
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  });
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  win.webContents.on('will-navigate', (event, url) => {
    if (url !== startURL) event.preventDefault();
  });
  win.loadURL(startURL);
}

app.whenReady().then(async () => {
  try { await require('./local-node.cjs')(app); } catch (error) { console.error(error.message); }
  const root = path.resolve(__dirname, '../dist');
  protocol.handle('journal', async (request) => {
    const url = new URL(request.url);
    if (url.host === 'app' && url.pathname.startsWith('/api/v1/'))
      return require('./node-proxy.cjs')(request, url);
    if (url.host !== 'app' || request.method !== 'GET') return new Response(null, { status: 403 });
    const name = decodeURIComponent(url.pathname);
    const file = path.resolve(root, '.' + (name === '/' ? '/index.html' : name));
    if (!file.startsWith(root + path.sep)) return new Response(null, { status: 403 });
    try {
      const response = await net.fetch(pathToFileURL(file).href);
      const headers = new Headers(response.headers);
      headers.set(
        'Content-Security-Policy',
        "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; font-src 'self'; connect-src 'self' blob:; object-src 'none'; frame-src 'none'; base-uri 'none'",
      );
      return new Response(response.body, { status: response.status, headers });
    } catch {
      return new Response(null, { status: 404 });
    }
  });
  ipcMain.handle('image:preview', async (event, bytes) => {
    if (
      event.senderFrame?.url !== startURL ||
      process.platform !== 'darwin' ||
      !(bytes instanceof Uint8Array)
    ) {
      throw new Error('Формат изображения не поддерживается');
    }
    const directory = await mkdtemp(path.join(tmpdir(), 'takhi-photo-'));
    try {
      const source = path.join(directory, 'original');
      const preview = path.join(directory, 'preview.png');
      await writeFile(source, bytes);
      await run('/usr/bin/sips', ['-s', 'format', 'png', source, '--out', preview], {
        timeout: 30000,
      });
      return await readFile(preview);
    } finally {
      await rm(directory, { force: true, recursive: true });
    }
  });
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      ...(process.platform === 'darwin' ? [{ role: 'appMenu' }] : []),
      { role: 'editMenu' },
      { role: 'windowMenu' },
    ]),
  );
  openJournal();
  app.on('activate', () => {
    if (!BrowserWindow.getAllWindows().length) openJournal();
  });
});
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
