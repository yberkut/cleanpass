package lossaudit

import (
	"bytes"
	"encoding/hex"
	"strings"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The memory of decisions is a contract with the Merge Engine, written down in the custom data of
// a copy. The audit reads it from the copies, not from the result and not from the engine, so a
// bug in the engine cannot excuse itself. Three kinds of key count here:
//
//	cleanpass/accepted/<e>/<v>  the human deleted entry e and saw a version with the time v;
//	                            a version with any other time is news to the human
//	cleanpass/entries/<a>/<b>   the human merged the entries a and b into one entry
//	cleanpass/groups/<a>/<b>    the groups a and b are one group: they stood on one path, or both
//	                            were a recycle bin
const (
	memoryAccepted = "cleanpass/accepted/"
	memoryEntries  = "cleanpass/entries/"
	memoryGroups   = "cleanpass/groups/"
)

// memory is what the copies remember.
type memory struct {
	// accepted holds the keys of the deletions the copies remember.
	accepted map[string]bool
	// entries and groups tell which entries and which groups are one.
	entries, groups families
}

func readMemory(copies []*snapshot.Snapshot) memory {
	m := memory{accepted: map[string]bool{}, entries: families{}, groups: families{}}
	for _, c := range copies {
		for i := range c.Meta.CustomData {
			key := c.Meta.CustomData[i].Key
			if strings.HasPrefix(key, memoryAccepted) {
				m.accepted[key] = true
			}
			if rest, ok := strings.CutPrefix(key, memoryEntries); ok {
				m.entries.joinNamed(rest)
			}
			if rest, ok := strings.CutPrefix(key, memoryGroups); ok {
				m.groups.joinNamed(rest)
			}
		}
	}
	return m
}

// families tells which UUIDs are one thing to the audit. It holds the way from each member to
// the first member of its family.
type families map[snapshot.UUID]snapshot.UUID

func (f families) head(id snapshot.UUID) snapshot.UUID {
	for {
		next, ok := f[id]
		if !ok {
			return id
		}
		id = next
	}
}

func (f families) join(a, b snapshot.UUID) {
	a, b = f.head(a), f.head(b)
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	if a != b {
		f[b] = a
	}
}

// joinNamed joins the two UUIDs a key names as <a>/<b>. A key of another form joins nothing.
func (f families) joinNamed(names string) {
	first, second, ok := strings.Cut(names, "/")
	a, okA := parseUUID(first)
	b, okB := parseUUID(second)
	if ok && okA && okB {
		f.join(a, b)
	}
}

func parseUUID(text string) (snapshot.UUID, bool) {
	var id snapshot.UUID
	if len(text) != hex.EncodedLen(len(id)) {
		return id, false
	}
	_, err := hex.Decode(id[:], []byte(text))
	return id, err == nil
}

// members gives for each UUID that has a family all members of that family.
func (f families) members() map[snapshot.UUID][]snapshot.UUID {
	byHead := map[snapshot.UUID][]snapshot.UUID{}
	for member := range f {
		head := f.head(member)
		if byHead[head] == nil {
			byHead[head] = []snapshot.UUID{head}
		}
		byHead[head] = append(byHead[head], member)
	}
	result := map[snapshot.UUID][]snapshot.UUID{}
	for _, all := range byHead {
		for _, member := range all {
			result[member] = all
		}
	}
	return result
}

// placesOf gives the members of the family of id, or id alone.
func placesOf(members map[snapshot.UUID][]snapshot.UUID, id snapshot.UUID) []snapshot.UUID {
	if all, ok := members[id]; ok {
		return all
	}
	return []snapshot.UUID{id}
}
