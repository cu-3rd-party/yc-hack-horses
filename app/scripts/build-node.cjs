// electron-builder calls this for each target before copying extraResources.
const { execFileSync } = require('node:child_process');
const path = require('node:path');
module.exports = async ({ electronPlatformName, arch }) => {
  const cpu = require('builder-util').Arch[arch];
  execFileSync('bash', [path.join(__dirname, 'build-node.sh'), electronPlatformName, cpu], {
    stdio: 'inherit',
  });
};
