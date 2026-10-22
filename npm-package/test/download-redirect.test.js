const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { EventEmitter } = require('node:events');
const { PassThrough } = require('node:stream');
const { execFileSync } = require('node:child_process');
const test = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../scripts/postinstall.js'), 'utf8');

// Keep real file descriptors and tar extraction; control only transport and the
// unlink/open interleaving that caused #7780, without downloading a release.
function downloader(archive, redirects, status = 200) {
  let pendingUnlink;
  let cleanup = Promise.resolve();
  let opened = 0;
  let requests = 0;
  const fileSystem = Object.create(fs);
  fileSystem.unlink = (dest, callback) => {
    pendingUnlink = () => {
      cleanup = new Promise(resolve => fs.unlink(dest, err => { callback(err); resolve(); }));
    };
  };
  fileSystem.createWriteStream = dest => {
    const stream = fs.createWriteStream(dest);
    opened++;
    if (pendingUnlink) {
      const unlink = pendingUnlink;
      pendingUnlink = undefined;
      stream.once('open', unlink);
    }
    return stream;
  };
  const transport = {
    get(url, callback) {
      const request = new EventEmitter();
      request.destroy = () => {};
      const current = requests++;
      setImmediate(() => {
        const response = new PassThrough();
        response.statusCode = current < redirects ? 302 : status;
        response.headers = { location: `https://release.invalid/${current + 1}` };
        callback(response);
        if (response.statusCode === 200) response.end(archive);
      });
      return request;
    },
  };
  const load = name => name === 'https' ? transport : name === 'fs' ? fileSystem
    : name === '../package.json' ? { version: '1.3.1' } : require(name);
  const download = vm.runInNewContext(`${source}\n;downloadFile`, {
    require: load, process: { env: { CI: 'true' } }, console: { log() {} },
    setTimeout, __dirname: path.join(__dirname, '../scripts'),
  });
  return { download, streams: () => opened, cleanup: () => cleanup };
}

for (const redirects of [0, 1, 2]) {
  test(`download retains an extractable archive after ${redirects} redirects`, async t => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-download-'));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    fs.writeFileSync(path.join(dir, 'bd'), 'fixture binary\n');
    const archive = execFileSync('tar', ['-czf', '-', '-C', dir, 'bd']);
    const dest = path.join(dir, 'download.tar.gz');
    const { download, streams, cleanup } = downloader(archive, redirects);
    await download('https://release.invalid/start', dest);
    await cleanup();
    assert.equal(streams(), redirects + 1);
    assert.deepEqual(fs.readFileSync(dest), archive);
    const output = path.join(dir, 'extracted');
    fs.mkdirSync(output);
    execFileSync('tar', ['-xzf', dest, '-C', output]);
    assert.equal(fs.readFileSync(path.join(output, 'bd'), 'utf8'), 'fixture binary\n');
  });
}

test('a rejected HTTP response does not resolve as a downloaded archive', async t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-download-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  await assert.rejects(downloader(Buffer.alloc(0), 0, 503).download(
    'https://release.invalid/unavailable', path.join(dir, 'archive.tar.gz')),
  /Failed to download: HTTP 503/);
});
