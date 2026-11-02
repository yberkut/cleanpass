package merge

import "github.com/yberkut/cleanpass/internal/snapshot"

// VersionsAsked gives the versions a question is about, in the order the question names them: the
// answers of a Conflict, the two sides of a SimilarPair, the one entry of a Deletion. They hold
// secret values, so a Question does not carry them. Only the Session takes them, at the moment
// a person asks to see one value.
func (r *Result) VersionsAsked(id QuestionID) []snapshot.EntryVersion { return r.asked[id] }

// noteAsked keeps the versions a question is about.
func (m *merger) noteAsked(id QuestionID, versions ...*version) {
	if m.asked == nil {
		m.asked = map[QuestionID][]snapshot.EntryVersion{}
	}
	kept := make([]snapshot.EntryVersion, len(versions))
	for i, v := range versions {
		kept[i] = v.data
	}
	m.asked[id] = kept
}
