package snapshot_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

func TestSameDataHasNoDifference(t *testing.T) {
	if place := snapshot.Difference(testkit.FullSnapshot(), testkit.FullSnapshot()); place != "" {
		t.Errorf("difference at %s", place)
	}
}

func TestNilAndEmptyAreTheSame(t *testing.T) {
	a, b := testkit.FullSnapshot(), testkit.FullSnapshot()
	a.Root.Groups[0].Entries, b.Root.Groups[0].Entries = nil, []snapshot.Entry{}
	a.Deletions, b.Deletions = nil, []snapshot.Deletion{}
	if place := snapshot.Difference(a, b); place != "" {
		t.Errorf("difference at %s", place)
	}
}

func TestTheSameInstantInAnotherZoneIsTheSame(t *testing.T) {
	a, b := testkit.FullSnapshot(), testkit.FullSnapshot()
	b.Meta.SettingsChanged = b.Meta.SettingsChanged.In(time.FixedZone("elsewhere", 3600))
	if place := snapshot.Difference(a, b); place != "" {
		t.Errorf("difference at %s", place)
	}
}

// The test changes each value in the data, one at a time, and the change must show. It knows
// no names, so a new part of a Snapshot is in it at once.
func TestEveryChangeOfTheDataShows(t *testing.T) {
	count := 0
	forEachSettable(reflect.ValueOf(testkit.FullSnapshot()).Elem(), "", func(_ reflect.Value, leafPlace string) {
		count++
		changed := testkit.FullSnapshot()
		changeLeaf(t, reflect.ValueOf(changed).Elem(), "", leafPlace)
		if place := snapshot.Difference(testkit.FullSnapshot(), changed); place == "" {
			t.Errorf("a change at %s does not show", leafPlace)
		}
	})
	if count < 50 {
		t.Errorf("the test reached only %d values", count)
	}
}

func TestADifferentOrderShows(t *testing.T) {
	a, b := testkit.FullSnapshot(), testkit.FullSnapshot()
	fields := b.Root.Entries[0].Fields
	slices.Reverse(fields)
	if place := snapshot.Difference(a, b); !strings.Contains(place, "Fields[0]") {
		t.Errorf("difference at %q, want the first field", place)
	}
}

func TestThePlaceHoldsNoValue(t *testing.T) {
	a, b := testkit.FullSnapshot(), testkit.FullSnapshot()
	b.Root.Entries[0].Fields[1].Value = "SECRET-MARKER-changed"
	place := snapshot.Difference(a, b)
	if strings.Contains(place, "SECRET-MARKER") {
		t.Errorf("the place names a value: %s", place)
	}
	if !strings.Contains(place, "Fields[1].Value") {
		t.Errorf("the place is %q, want the changed field", place)
	}
}

// outsideTheData are the parts that say something about a file and not what it holds.
var outsideTheData = []string{".Format", ".FieldLosses", ".FileDigest"}

func isOutsideTheData(place string) bool {
	return slices.ContainsFunc(outsideTheData, func(part string) bool { return strings.HasPrefix(place, part) })
}

func changeLeaf(t *testing.T, value reflect.Value, place, target string) {
	t.Helper()
	forEachSettable(value, place, func(leaf reflect.Value, leafPlace string) {
		if leafPlace != target {
			return
		}
		switch leaf.Kind() {
		case reflect.String:
			leaf.SetString(leaf.String() + "-changed")
		case reflect.Bool:
			leaf.SetBool(!leaf.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			leaf.SetInt(leaf.Int() + 1)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			leaf.SetUint(leaf.Uint() + 1)
		case reflect.Array:
			leaf.Index(0).SetUint(leaf.Index(0).Uint() + 1)
		case reflect.Map:
			for _, key := range leaf.MapKeys() {
				old := leaf.MapIndex(key).Interface().(snapshot.Attachment)
				old.Protected = !old.Protected
				leaf.SetMapIndex(key, reflect.ValueOf(old))
			}
		case reflect.Struct:
			leaf.Set(reflect.ValueOf(leaf.Interface().(time.Time).Add(time.Second)))
		default:
			t.Fatalf("cannot change %s of kind %s", leafPlace, leaf.Kind())
		}
	})
}

// forEachSettable calls visit with each value that can change on its own. An empty slice and
// a nil pointer hold nothing to change, and TestFullSnapshotFillsEveryPart holds those few.
func forEachSettable(value reflect.Value, place string, visit func(reflect.Value, string)) {
	if isOutsideTheData(place) {
		return
	}
	if value.Type() == reflect.TypeFor[time.Time]() {
		visit(value, place)
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := range value.NumField() {
			forEachSettable(value.Field(i), place+"."+value.Type().Field(i).Name, visit)
		}
	case reflect.Slice:
		for i := range value.Len() {
			forEachSettable(value.Index(i), fmt.Sprintf("%s[%d]", place, i), visit)
		}
	case reflect.Pointer:
		if !value.IsNil() {
			forEachSettable(value.Elem(), place, visit)
		}
	default:
		visit(value, place)
	}
}

// Every part of every kind of struct in a Snapshot holds a value somewhere in the full
// snapshot. So a new part cannot stay out of the tests that write that snapshot to a file.
func TestFullSnapshotFillsEveryPart(t *testing.T) {
	filled := map[string]bool{}
	var walk func(value reflect.Value)
	walk = func(value reflect.Value) {
		switch value.Kind() {
		case reflect.Struct:
			if value.Type() == reflect.TypeFor[time.Time]() {
				return
			}
			for i := range value.NumField() {
				part := value.Type().Name() + "." + value.Type().Field(i).Name
				if isOutsideTheData(strings.TrimPrefix(part, "Snapshot")) {
					continue
				}
				filled[part] = filled[part] || !value.Field(i).IsZero()
				walk(value.Field(i))
			}
		case reflect.Slice:
			for i := range value.Len() {
				walk(value.Index(i))
			}
		case reflect.Pointer:
			if !value.IsNil() {
				walk(value.Elem())
			}
		case reflect.Map:
			for iterator := value.MapRange(); iterator.Next(); {
				walk(iterator.Value())
			}
		}
	}
	walk(reflect.ValueOf(testkit.FullSnapshot()).Elem())

	var parts []string
	for part := range filled {
		parts = append(parts, part)
	}
	slices.Sort(parts)
	if len(parts) < 50 {
		t.Errorf("the test saw only %d parts", len(parts))
	}
	for _, part := range parts {
		if !filled[part] {
			t.Errorf("no value for %s", part)
		}
	}
}
