package mergetest

import (
	"slices"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The moves of the settings of the database.
func init() {
	register("rename the database", renameDatabase)
	register("describe the database", describeDatabase)
	register("set the default user", setDefaultUser)
	register("change the settings", changeSettings)
	register("set a custom icon", setCustomIcon)
	register("set custom data", setCustomData)
	register("make a bin of its own", makeOwnBin)
}

func renameDatabase(t *rapid.T, _ *world, f *fork) {
	f.snapshot.Meta.DatabaseName = rapid.SampledFrom([]string{"base", "work", "home"}).Draw(t, "database name")
	f.snapshot.Meta.DatabaseNameChanged = f.tick(t)
}

func describeDatabase(t *rapid.T, _ *world, f *fork) {
	f.snapshot.Meta.DatabaseDescription = drawText(t)
	f.snapshot.Meta.DatabaseDescriptionChanged = f.tick(t)
}

func setDefaultUser(t *rapid.T, _ *world, f *fork) {
	f.snapshot.Meta.DefaultUserName = rapid.SampledFrom(userNames).Draw(t, "default user")
	f.snapshot.Meta.DefaultUserNameChanged = f.tick(t)
}

// changeSettings changes what has no time of its own. KeePassXC writes no limit as -1.
func changeSettings(t *rapid.T, _ *world, f *fork) {
	meta := &f.snapshot.Meta
	meta.SettingsChanged = f.tick(t)
	meta.Color = rapid.SampledFrom(colors).Draw(t, "database color")
	meta.MaintenanceHistoryDays = int64(rapid.IntRange(0, 365).Draw(t, "maintenance days"))
	meta.HistoryMaxItems = rapid.SampledFrom([]int64{-1, 0, 2, 10}).Draw(t, "history items")
	meta.HistoryMaxSize = rapid.SampledFrom([]int64{-1, 0, 100, 6291456}).Draw(t, "history size")
}

// iconIDs holds few UUIDs, so two copies can set the same icon.
var iconIDs = []snapshot.UUID{{0: 0xC1}, {0: 0xC2}, {0: 0xC3}}

func setCustomIcon(t *rapid.T, _ *world, f *fork) {
	icon := snapshot.CustomIcon{
		UUID:             rapid.SampledFrom(iconIDs).Draw(t, "icon"),
		Data:             drawText(t),
		Name:             drawText(t),
		LastModification: f.tick(t),
	}
	icons := f.snapshot.Meta.CustomIcons
	if i := slices.IndexFunc(icons, func(known snapshot.CustomIcon) bool { return known.UUID == icon.UUID }); i >= 0 {
		icons[i] = icon
	} else {
		f.snapshot.Meta.CustomIcons = append(icons, icon)
	}
}

// customDataKeys holds few keys, so two copies can set the same item.
var customDataKeys = []string{"plugin.first", "plugin.second", "plugin.third"}

func setCustomData(t *rapid.T, _ *world, f *fork) {
	item := snapshot.CustomDataItem{
		Key:              rapid.SampledFrom(customDataKeys).Draw(t, "custom data key"),
		Value:            drawText(t),
		LastModification: f.tick(t),
	}
	items := f.snapshot.Meta.CustomData
	if i := slices.IndexFunc(items, func(known snapshot.CustomDataItem) bool { return known.Key == item.Key }); i >= 0 {
		items[i] = item
	} else {
		f.snapshot.Meta.CustomData = append(items, item)
	}
}

// makeOwnBin makes a recycle bin with a UUID only this copy knows, as two systems do when each
// has its first deletion after the fork.
func makeOwnBin(t *rapid.T, w *world, f *fork) {
	if !f.snapshot.Meta.RecycleBin.IsZero() {
		return
	}
	stamp := f.tick(t)
	id := w.newUUID()
	f.snapshot.Meta.RecycleBinEnabled, f.snapshot.Meta.RecycleBin, f.snapshot.Meta.RecycleBinChanged = true, id, stamp
	f.snapshot.Root.Groups = append(f.snapshot.Root.Groups, snapshot.Group{
		UUID:  id,
		Name:  rapid.SampledFrom([]string{"Recycle Bin", "Trash"}).Draw(t, "name of the bin"),
		Times: snapshot.Times{Creation: stamp, LastModification: stamp, LocationChanged: stamp},
	})
}
