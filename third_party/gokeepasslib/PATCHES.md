# Local patches to gokeepasslib

This folder is a copy of `github.com/tobischo/gokeepasslib/v3` at v3.7.1 (MIT, see `LICENSE.md`).
The root `go.mod` connects it with `replace`. Only the KDBX Gateway (`internal/gateway`) may import it.

The copy differs from upstream by this file, by the one patch below, and by line endings:
`tests/schema/KDBX_XML_4.1.xsd` has CRLF upstream and LF here. Nothing else was changed.

## Argon2id

**Why.** Upstream knows only Argon2d. It treats any other KDF UUID as AES-KDF. A database with
Argon2id, the KeePassXC default, then fails with a false `Wrong password?` error.

**What changed.**

- `header.go`: new exported variable `KdfArgon2id`, the UUID `9E298B19-56DB-4773-B23D-FC3EC6F0A1E6`.
- `credentials.go`, `buildTransformedKey`: the Argon2 branch also accepts `KdfArgon2id`, and it calls
  `argon2.IDKey` for it instead of `argon2.DKey`.

```diff
--- a/credentials.go
+++ b/credentials.go
@@ func (c *DBCredentials) buildTransformedKey(db *Database) ([]byte, error) {
-		if reflect.DeepEqual(db.Header.FileHeaders.KdfParameters.UUID, KdfArgon2) {
+		isID := reflect.DeepEqual(db.Header.FileHeaders.KdfParameters.UUID, KdfArgon2id)
+		if isID || reflect.DeepEqual(db.Header.FileHeaders.KdfParameters.UUID, KdfArgon2) {
@@
-			transformedKey = argon2.DKey(
+			kdf := argon2.DKey
+			if isID {
+				kdf = argon2.IDKey
+			}
+			transformedKey = kdf(
--- a/header.go
+++ b/header.go
@@ var KdfArgon2 = []byte{
+// KdfArgon2id is the Argon2id key derivation function ID
+var KdfArgon2id = []byte{
+	0x9E, 0x29, 0x8B, 0x19,
+	0x56, 0xDB, 0x47, 0x73,
+	0xB2, 0x3D, 0xFC, 0x3E,
+	0xC6, 0xF0, 0xA1, 0xE6,
+}
```

**What it does not change.** New KDBX 4 headers still default to `KdfArgon2` (`NewKDBX4FileHeaders`).
Whoever writes a file sets the KDF explicitly.

## Updating this copy

1. Replace everything in this folder except this file with the new upstream version. Keep its LF line endings.
2. Apply the patch above again. Keep the `tests` folder: the upstream tests read it.
3. Run the upstream tests in this folder and the tests of the repository from its root:

       cd third_party/gokeepasslib && go test ./...
       cd ../.. && go vet ./... && go test ./...

The folder has its own `go.mod`, so `go test ./...` from the root does not include it. Run both.
