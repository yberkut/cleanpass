package gateway

import (
	"bytes"
	"encoding/xml"

	"github.com/tobischo/gokeepasslib/v3"
)

// timeElements are the names of all elements the library reads as a time.
var timeElements = map[string]bool{
	"SettingsChanged":            true,
	"DatabaseNameChanged":        true,
	"DatabaseDescriptionChanged": true,
	"DefaultUserNameChanged":     true,
	"MasterKeyChanged":           true,
	"RecycleBinChanged":          true,
	"EntryTemplatesGroupChanged": true,
	"CreationTime":               true,
	"LastModificationTime":       true,
	"LastAccessTime":             true,
	"ExpiryTime":                 true,
	"LocationChanged":            true,
	"DeletionTime":               true,
}

// hasEmptyTime tells whether the decrypted XML holds a time element without a value. The
// library fails on one with a bare EOF, but other damage gives that error too, so the
// tool looks at the document itself. It is for a Decode that has failed: the document
// may be broken, and that only means the answer is no.
func hasEmptyTime(db *gokeepasslib.Database) bool {
	document, err := xmlDocument(db)
	if err != nil {
		return false
	}
	decoder := xml.NewDecoder(bytes.NewReader(document))
	empty := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		switch t := token.(type) {
		case xml.StartElement:
			empty = timeElements[t.Name.Local]
		case xml.CharData:
			if len(bytes.Trim(t, xmlSpace)) > 0 {
				empty = false
			}
		case xml.EndElement:
			if empty {
				return true
			}
		}
	}
}
