module github.com/yberkut/cleanpass

go 1.27

replace github.com/tobischo/gokeepasslib/v3 => ./third_party/gokeepasslib

require (
	github.com/tobischo/gokeepasslib/v3 v3.7.1
	golang.org/x/term v0.47.0
)

require (
	github.com/tobischo/argon2 v0.2.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)
