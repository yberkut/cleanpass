package snapshot

import (
	"bytes"
	"fmt"
	"reflect"
	"time"
)

// Difference names the first place where the data of two snapshots differs, or gives an
// empty string when it is the same data. The place holds no value, so it can reach a report.
//
// It looks at what a file holds as data: Meta, Root, Deletions and Attachments. Format and
// FieldLosses say something about the file, and FileDigest about its bytes. Order counts,
// because a file keeps its order. A nil list and an empty one are the same, because a file
// cannot tell them apart. Times count as instants.
func Difference(a, b *Snapshot) string {
	for _, part := range []struct {
		name string
		a, b any
	}{
		{"Meta", a.Meta, b.Meta},
		{"Root", a.Root, b.Root},
		{"Deletions", a.Deletions, b.Deletions},
		{"Attachments", a.Attachments, b.Attachments},
	} {
		if place := differ(part.name, reflect.ValueOf(part.a), reflect.ValueOf(part.b)); place != "" {
			return place
		}
	}
	return ""
}

var (
	timeType  = reflect.TypeFor[time.Time]()
	bytesType = reflect.TypeFor[[]byte]()
)

func differ(place string, a, b reflect.Value) string {
	switch {
	case a.Type() == timeType:
		if t := a.Interface().(time.Time); !t.Equal(b.Interface().(time.Time)) {
			return place
		}
		return ""
	case a.Type() == bytesType:
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			return place
		}
		return ""
	}
	switch a.Kind() {
	case reflect.Struct:
		for i := range a.NumField() {
			if found := differ(place+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i)); found != "" {
				return found
			}
		}
	case reflect.Slice:
		if a.Len() != b.Len() {
			return place
		}
		for i := range a.Len() {
			if found := differ(fmt.Sprintf("%s[%d]", place, i), a.Index(i), b.Index(i)); found != "" {
				return found
			}
		}
	case reflect.Pointer:
		if a.IsNil() != b.IsNil() {
			return place
		}
		if !a.IsNil() {
			return differ(place, a.Elem(), b.Elem())
		}
	case reflect.Map:
		// The keys of Attachments are the hashes of bytes, so the place does not name them.
		if a.Len() != b.Len() {
			return place
		}
		for iterator := a.MapRange(); iterator.Next(); {
			other := b.MapIndex(iterator.Key())
			if !other.IsValid() || differ(place, iterator.Value(), other) != "" {
				return place
			}
		}
	default:
		if !a.Equal(b) {
			return place
		}
	}
	return ""
}
