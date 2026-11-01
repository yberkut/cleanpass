package lossaudit_test

import (
	"testing"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// versionData gives the version the custom data, written as key, value, key, value.
func versionData(v snapshot.EntryVersion, pairs ...string) snapshot.EntryVersion {
	v.CustomData = nil
	for i := 0; i < len(pairs); i += 2 {
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: pairs[i], Value: pairs[i+1]})
	}
	return v
}

// KeePassXC leaves the custom data of an entry out of the history version an edit makes, so a
// version with the same time and the same content and less custom data says nothing new.
func TestAVersionThatLargerCustomDataHoldsIsNoLoss(t *testing.T) {
	main := version(hours(10), "main")
	larger := versionData(version(hours(1), "secret"), "browser", "settings", "plugin", "1")
	for name, smaller := range map[string]snapshot.EntryVersion{
		"no custom data":   version(hours(1), "secret"),
		"part of the data": versionData(version(hours(1), "secret"), "plugin", "1"),
		"only service data": versionData(version(hours(1), "secret"),
			snapshot.CustomDataLastModified, "Mon", snapshot.CustomDataRandomSlug, "abc"),
	} {
		t.Run(name, func(t *testing.T) {
			wantLosses(t, audit(database(entry(mailID, main, larger)), database(entry(mailID, main, smaller))))
			wantLosses(t, audit(database(entry(mailID, larger)), database(entry(mailID, smaller))))
			wantLosses(t, audit(database(entry(mailID, main, larger)), database(entry(mailID, smaller))))
			wantLosses(t, audit(database(entry(mailID, larger)), database(entry(mailID, main, smaller)), database(entry(mailID, main))),
				versionLost(mailID, hours(10)))
		})
	}
}

// Each of these has a result that lacks the version of the copy.
func TestOtherCustomDataInTheResultDoesNotCoverAVersion(t *testing.T) {
	for name, tc := range map[string]struct{ kept, held []string }{
		"the result has less":               {[]string{"browser", "settings"}, []string{"browser", "settings", "plugin", "1"}},
		"the result has nothing":            {nil, []string{"browser", "settings"}},
		"neither holds the other":           {[]string{"browser", "settings"}, []string{"plugin", "1"}},
		"they overlap":                      {[]string{"common", "x", "browser", "settings"}, []string{"common", "x", "plugin", "1"}},
		"another value of the same key":     {[]string{"browser", "new", "plugin", "1"}, []string{"browser", "old"}},
		"another key of the same value":     {[]string{"plugin", "settings", "more", "1"}, []string{"browser", "settings"}},
		"the same items, one of them twice": {[]string{"browser", "settings"}, []string{"browser", "settings", "browser", "settings"}},
		"only service data is more":         {[]string{snapshot.CustomDataLastModified, "Mon"}, []string{"browser", "settings"}},
	} {
		t.Run(name, func(t *testing.T) {
			kept := versionData(version(hours(1), "secret"), tc.kept...)
			held := versionData(version(hours(1), "secret"), tc.held...)

			wantLosses(t, audit(database(entry(mailID, kept)), database(entry(mailID, held))), versionLost(mailID, hours(1)))
		})
	}
}

// More custom data covers a version only when all the rest is the same. Whatever else differs
// is a change of the content, so the larger custom data must not hide it.
func TestLargerCustomDataDoesNotCoverAnyOtherChange(t *testing.T) {
	main := version(hours(10), "main")
	for _, c := range lostChanges {
		if c.touches == "CustomData" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			changed := fullVersion()
			c.apply(&changed)
			changed.CustomData = append(changed.CustomData, snapshot.CustomDataItem{Key: "more", Value: "data"})

			got := audit(database(entry(mailID, main, changed)), database(entry(mailID, main, fullVersion())))

			wantLosses(t, got, versionLost(mailID, hours(1)))
		})
	}
}

func TestLargerCustomDataUnderAnotherEntryDoesNotCount(t *testing.T) {
	smaller := version(hours(1), "secret")
	larger := versionData(smaller, "browser", "settings")

	got := audit(database(entry(bankID, larger)), database(entry(mailID, smaller), entry(bankID, smaller)))

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

// The audit does not take the word of a merged pair for more than it says.
func TestLargerCustomDataCountsUnderTheEntryThatTookTheVersions(t *testing.T) {
	smaller := version(hours(1), "secret")
	larger := versionData(smaller, "browser", "settings")
	result, input := database(entry(bankID, larger)), database(entry(mailID, smaller))

	got := lossaudit.Audit(lossaudit.Input{
		Copies:  []*snapshot.Snapshot{input},
		Result:  result,
		Allowed: lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID}},
	})

	wantLosses(t, got)
}

// A version the result holds exactly stays covered next to larger ones, and two versions where
// neither holds the other both have to stay.
func TestEachVersionNeedsItsOwnPlaceWhenNeitherHoldsTheOther(t *testing.T) {
	first := versionData(version(hours(1), "secret"), "browser", "settings")
	second := versionData(version(hours(1), "secret"), "plugin", "1")
	input := database(entry(mailID, first, second))

	wantLosses(t, audit(database(entry(mailID, second, first)), input))
	wantLosses(t, audit(database(entry(mailID, first)), input), versionLost(mailID, hours(1)))
	wantLosses(t, audit(database(entry(mailID, second)), input), versionLost(mailID, hours(1)))
}
