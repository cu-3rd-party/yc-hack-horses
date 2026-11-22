// tsc emits enroll.js (CommonJS content); Electron main.cjs requires it as
// build/enroll.cjs, so rename after tsc -p tsconfig.electron.json.
const fs = require('node:fs');
const path = require('node:path');
const src = path.join(__dirname, '..', 'electron', 'build', 'enroll.js');
const dst = path.join(__dirname, '..', 'electron', 'build', 'enroll.cjs');
if (fs.existsSync(src)) fs.renameSync(src, dst);
