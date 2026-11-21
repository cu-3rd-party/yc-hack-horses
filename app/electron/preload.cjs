const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('journalImages', {
  decode: (bytes) => ipcRenderer.invoke('image:preview', bytes),
});
