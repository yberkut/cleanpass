package gateway

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"slices"
	"strings"

	"github.com/tobischo/gokeepasslib/v3"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// guardFields is the field guard. The library keeps only the fields on its list, so an
// unknown field would vanish without a word on the next save. The guard compares the
// decrypted XML with the XML the library would build from what it understood.
//
// It looks at names and counts, never at values. An element or attribute without data
// does not count: the library leaves out many empty known elements, so counting them
// would raise false alarms on files of KeePassXC. The cost is that an empty unknown
// element goes unnoticed, and it holds nothing to lose.
//
// Call it while the values are still locked, so that the rebuilt XML in memory holds no
// clear text.
func guardFields(db *gokeepasslib.Database) ([]snapshot.FieldLoss, error) {
	original, err := xmlDocument(db)
	if err != nil {
		return nil, err
	}
	rebuilt, err := xml.MarshalIndent(db.Content, "", "\t")
	if err != nil {
		return nil, err
	}
	// This runs on every open and most of its time goes to reading XML, so the two
	// documents get read side by side.
	type counted struct {
		paths map[string]int
		err   error
	}
	rebuiltCounted := make(chan counted, 1)
	go func() {
		paths, err := countDataPaths(rebuilt)
		rebuiltCounted <- counted{paths, err}
	}()
	before, err := countDataPaths(original)
	if err != nil {
		return nil, err
	}
	result := <-rebuiltCounted
	if result.err != nil {
		return nil, result.err
	}
	after := result.paths

	var losses []snapshot.FieldLoss
	for path, inFile := range before {
		if kept := after[path]; kept < inFile {
			losses = append(losses, snapshot.FieldLoss{Path: path, InFile: inFile, Kept: kept})
		}
	}
	if loss, ok := unusedAttachments(db); ok {
		losses = append(losses, loss)
	}
	losses = append(losses, headerLosses(db)...)
	slices.SortFunc(losses, func(a, b snapshot.FieldLoss) int { return strings.Compare(a.Path, b.Path) })
	return losses, nil
}

// The places of the attachments in a file. KDBX 4 keeps them outside the XML.
const (
	innerHeaderAttachmentPath = "InnerHeader/Binary"
	metaAttachmentPath        = "KeePassFile/Meta/Binaries/Binary"
	headerCommentPath         = "OuterHeader/Comment"
	headerCustomDataPath      = "OuterHeader/PublicCustomData"
)

// headerLosses finds what the rebuilt XML cannot show and a Snapshot has no place for: a file
// is written from a Snapshot with a new header. KeePassXC itself does not fill these places,
// so they can only hold data another program needs. KDBX 4 keeps its attachments in the inner
// header, so the same goes for attachments it holds in the XML, which nothing can refer to.
func headerLosses(db *gokeepasslib.Database) []snapshot.FieldLoss {
	var losses []snapshot.FieldLoss
	headers := db.Header.FileHeaders
	if len(headers.Comment) > 0 {
		losses = append(losses, snapshot.FieldLoss{Path: headerCommentPath, InFile: 1})
	}
	if headers.PublicCustomData != nil && len(headers.PublicCustomData.Items) > 0 {
		losses = append(losses, snapshot.FieldLoss{Path: headerCustomDataPath, InFile: len(headers.PublicCustomData.Items)})
	}
	if db.Header.IsKdbx4() && len(db.Content.Meta.Binaries) > 0 {
		losses = append(losses, snapshot.FieldLoss{Path: metaAttachmentPath, InFile: len(db.Content.Meta.Binaries)})
	}
	return losses
}

// unusedAttachments finds what the rebuilt XML cannot show: on every save the library
// drops an attachment that no version of any entry refers to.
func unusedAttachments(db *gokeepasslib.Database) (snapshot.FieldLoss, bool) {
	path, attachments := metaAttachmentPath, db.Content.Meta.Binaries
	if db.Header.IsKdbx4() {
		path, attachments = innerHeaderAttachmentPath, db.Content.InnerHeader.Binaries
	}
	used := map[int]bool{}
	addGroupReferences(used, db.Content.Root.Groups)
	kept := 0
	for i := range attachments {
		if used[attachments[i].ID] {
			kept++
		}
	}
	return snapshot.FieldLoss{Path: path, InFile: len(attachments), Kept: kept}, kept < len(attachments)
}

func addGroupReferences(used map[int]bool, groups []gokeepasslib.Group) {
	for i := range groups {
		addEntryReferences(used, groups[i].Entries)
		addGroupReferences(used, groups[i].Groups)
	}
}

func addEntryReferences(used map[int]bool, entries []gokeepasslib.Entry) {
	for i := range entries {
		for j := range entries[i].Binaries {
			used[entries[i].Binaries[j].Value.ID] = true
		}
		for j := range entries[i].Histories {
			addEntryReferences(used, entries[i].Histories[j].Entries)
		}
	}
}

const innerHeaderItemSize = 5 // one byte of kind and four of length

// xmlDocument finds the XML in the decrypted data. In KDBX 4 the inner header comes
// first, with the bytes of all attachments. It has to be walked: an attachment may
// itself hold the text of a KeePass XML export.
func xmlDocument(db *gokeepasslib.Database) ([]byte, error) {
	data := db.Content.RawData
	if !db.Header.IsKdbx4() {
		return data, nil
	}
	for {
		if len(data) < innerHeaderItemSize {
			return nil, errors.New("inner header has no end")
		}
		kind, size := data[0], uint64(binary.LittleEndian.Uint32(data[1:]))
		data = data[innerHeaderItemSize:]
		if size > uint64(len(data)) {
			return nil, errors.New("inner header item is longer than the data")
		}
		data = data[size:]
		if kind == gokeepasslib.InnerHeaderTerminator {
			return data, nil
		}
	}
}

// xmlSpace is what the XML standard calls white space. Pretty printing adds it between
// elements, so on its own it is not data.
const xmlSpace = " \t\r\n"

// openElement is an element whose end tag is still to come.
type openElement struct {
	parentPathSize int
	hasData        bool
}

// countDataPaths counts the elements with text and the attributes with a value, by their
// full path. It reads only up to the end of the root element, as the library does:
// without compression the padding of the cipher follows the XML.
//
// The library has already read the document, so RawToken is enough, and it is faster: the
// tags are known to match and namespaces play no part in the paths.
func countDataPaths(document []byte) (map[string]int, error) {
	counts := map[string]int{}
	decoder := xml.NewDecoder(bytes.NewReader(document))
	var path []byte
	var open []openElement
	for {
		token, err := decoder.RawToken()
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			open = append(open, openElement{parentPathSize: len(path)})
			if len(path) > 0 {
				path = append(path, '/')
			}
			path = append(path, t.Name.Local...)
			for _, attribute := range t.Attr {
				if attribute.Value != "" && !isNamespaceDeclaration(attribute.Name) {
					counts[string(path)+"@"+attribute.Name.Local]++
				}
			}
		case xml.CharData:
			if len(open) > 0 && len(bytes.Trim(t, xmlSpace)) > 0 {
				open[len(open)-1].hasData = true
			}
		case xml.EndElement:
			if len(open) == 0 {
				return nil, errors.New("end tag without a start tag")
			}
			current := open[len(open)-1]
			if current.hasData {
				counts[string(path)]++
			}
			path = path[:current.parentPathSize]
			open = open[:len(open)-1]
			if len(open) == 0 {
				return counts, nil
			}
		}
	}
}

func isNamespaceDeclaration(name xml.Name) bool {
	return name.Space == "xmlns" || name.Local == "xmlns"
}
