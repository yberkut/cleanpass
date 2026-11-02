// kdbxweb opens a database and prints the titles of its entries as JSON. A software HMAC-SHA1 stands
// in for the YubiKey. Usage: read.cjs <file> <password> <secret in hex> [<key file>]
const fs = require('fs');
const crypto = require('crypto');
const kdbxweb = require('kdbxweb/dist/kdbxweb.min.js');

const [file, password, secret, keyFile] = process.argv.slice(2);

const respond = async (challenge) => {
  const salt = Buffer.from(challenge);
  const pad = 64 - salt.length;
  const padded = Buffer.concat([salt, Buffer.alloc(pad, pad)]);
  return new Uint8Array(crypto.createHmac('sha1', Buffer.from(secret, 'hex')).update(padded).digest());
};

kdbxweb.CryptoEngine.setArgon2Impl((key, salt, memory, iterations, length, parallelism, type) =>
  new Promise((resolve, reject) =>
    crypto.argon2(
      ['argon2d', 'argon2i', 'argon2id'][type],
      { message: Buffer.from(key), nonce: Buffer.from(salt), memory, passes: iterations, tagLength: length, parallelism },
      (error, output) => (error ? reject(error) : resolve(output.buffer.slice(output.byteOffset, output.byteOffset + output.length))),
    ),
  ),
);

const toArrayBuffer = (bytes) => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.length);

(async () => {
  const key = keyFile ? toArrayBuffer(fs.readFileSync(keyFile)) : null;
  const credentials = new kdbxweb.KdbxCredentials(kdbxweb.ProtectedValue.fromString(password), key, respond);
  await credentials.ready;
  const db = await kdbxweb.Kdbx.load(toArrayBuffer(fs.readFileSync(file)), credentials);
  const titles = [];
  for (const group of db.groups) {
    for (const entry of group.allEntries()) {
      const title = entry.fields.get('Title');
      titles.push(title && title.getText ? title.getText() : title);
    }
  }
  console.log(JSON.stringify(titles));
})().catch((error) => {
  console.error('kdbxweb failed:', error.message || error);
  process.exit(1);
});
