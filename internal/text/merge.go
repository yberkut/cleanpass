package text

import "fmt"

// Why merge gives no result.
const (
	// MergeNeedsOutput says that merge needs the path of the new file.
	MergeNeedsOutput = "Вкажіть шлях нового файла: --output <новий файл>."
	// MergeEngineFailed says that the copies could not be merged at all.
	MergeEngineFailed = "Не вдалося злити копії. Результат не створено, оригінали не змінено."
	// MergeLossesFound stands in front of the list of what the result would lose.
	MergeLossesFound = "Перевірка на втрати зупинила злиття: у результаті бракує даних. Результат не створено, оригінали не змінено. Чого бракує:"
)

// OptionTwice says that an option came more than once. It names only the option.
func OptionTwice(name string) string {
	return fmt.Sprintf("Опцію %s вказано кілька разів.", name)
}

// MergeTargetExists refuses a path where something already lies.
func MergeTargetExists(path string) string {
	return fmt.Sprintf("Файл %s вже існує. Тула ніколи його не перезаписує: вкажіть інший шлях.", path)
}

// MergeTargetIsInput refuses a path that leads to an input copy.
func MergeTargetIsInput(path string) string {
	return fmt.Sprintf("Шлях %s веде до вхідної копії. Оригінали не змінюються: вкажіть інший шлях.", path)
}

// MergeTargetDirectory refuses a path in a directory that is not there.
func MergeTargetDirectory(path string) string {
	return fmt.Sprintf("Директорії для файла %s немає. Створіть її або вкажіть інший шлях.", path)
}

// MergeInputChanged says that a copy is not what the tool read, so a result would be stale.
func MergeInputChanged(path string) string {
	return fmt.Sprintf("Копія %s змінилась під час роботи або більше не читається. Результат не створено, щоб не злити застарілі дані. Запустіть злиття ще раз.", path)
}

// MergeResultLeft says that a result could not be taken back after a copy changed.
func MergeResultLeft(path string) string {
	return fmt.Sprintf("Не вдалося прибрати файл %s. Він містить застарілі дані: не користуйтесь ним і видаліть його.", path)
}

// MergeStoppedByFields stops a merge at a copy with data a save would not keep.
func MergeStoppedByFields(path string) string {
	return fmt.Sprintf("Злиття зупинено через копію %s. Обійти цю зупинку не можна. Результат не створено, оригінали не змінено.", path)
}

// MergeReadBackFailed says that the written file does not hold what it should. The place
// names a part of a database and never a value.
func MergeReadBackFailed(place string) string {
	return fmt.Sprintf("Перевірка записаного файла не пройшла: він містить не те, що мав (місце: %s). Результат не створено, оригінали не змінено.", place)
}

// MergeWriteFailed says that the file could not be written.
func MergeWriteFailed(path string) string {
	return fmt.Sprintf("Не вдалося записати результат %s. Перевірте місце на диску і права на запис. Результат не створено, оригінали не змінено.", path)
}

// What merge says about a result.
const (
	// MergeInputsIntact says that the check of the originals has passed.
	MergeInputsIntact = "Оригінали не змінились: контрольні суми файлів збігаються."
)

// What the KeePassXC Judge says.
const (
	// JudgeWorking says that the outside check has begun. It can take a while: the program runs
	// the KDF of the file for each attachment.
	JudgeWorking = "Справжній KeePassXC перевіряє результат..."
	// JudgeAgrees says that the outside check has passed.
	JudgeAgrees = "Справжній KeePassXC перевірив результат: він бачить те, що результат має містити."
	// JudgeNotFound warns that no outside program has looked at the result.
	JudgeNotFound = "Попередження: справжній KeePassXC цей результат не перевіряв, бо програми keepassxc-cli не знайдено. Щоб перевірити результат, встановіть KeePassXC або додайте keepassxc-cli до PATH і запустіть злиття ще раз."
	// JudgeCannotRead stops a merge at a file the program cannot read.
	JudgeCannotRead = "Справжній KeePassXC не зміг прочитати результат. Результат не створено, оригінали не змінено."
)

// JudgeDoesNotRun warns that the program is there and does not run.
func JudgeDoesNotRun(path string) string {
	return fmt.Sprintf("Попередження: справжній KeePassXC цей результат не перевіряв, бо програма %s не запускається.", path)
}

// JudgeTooOld warns that the program is too old for the format of a result.
func JudgeTooOld(path, version string) string {
	return fmt.Sprintf("Попередження: справжній KeePassXC цей результат не перевіряв, бо програма %s має версію %s, а потрібна щонайменше 2.7.", path, version)
}

// JudgeDiffers stops a merge at a result the program sees in another way. The place names a part
// of a database and never a value.
func JudgeDiffers(place string) string {
	return fmt.Sprintf("Справжній KeePassXC бачить у результаті не те, що результат має містити (місце: %s). Результат не створено, оригінали не змінено.", place)
}

// MergeDone names the new file.
func MergeDone(path string) string {
	return fmt.Sprintf("Створено новий файл: %s", path)
}

// MergeKeysFrom says what opens the new file.
func MergeKeysFrom(path string) string {
	return fmt.Sprintf("Новий файл відкривають ті самі пароль і файл-ключ, що й копію %s.", path)
}

// MergeUnanswered says how many questions the merge settled by the rules without an answer.
func MergeUnanswered(count int) string {
	return fmt.Sprintf("Питань без відповіді: %d. Для них діють правила без відповіді, тож жодні дані не зникли.", count)
}

// Lines about what a result would lose. They name a thing and never hold a value.

// LossVersion names a lost version of an entry.
func LossVersion(entry, at string) string {
	return fmt.Sprintf("версія запису %s від %s", entry, at)
}

// LossAttachment names a lost attachment.
func LossAttachment(entry string) string {
	return fmt.Sprintf("вкладення запису %s", entry)
}

// LossDeletion names a lost record about a deleted object.
func LossDeletion(object string) string {
	return fmt.Sprintf("запис про видалення %s", object)
}

// LossGroup names a lost group.
func LossGroup(group string) string {
	return fmt.Sprintf("група %s", group)
}

// LossGroupFields names a group that lost the fields of its newest version.
func LossGroupFields(group string) string {
	return fmt.Sprintf("найновіші поля групи %s", group)
}

// LossCustomIcon names a lost custom icon.
func LossCustomIcon(icon string) string {
	return fmt.Sprintf("кастомна іконка %s", icon)
}

// LossCustomData names a lost item of the custom data of the database.
func LossCustomData(key string) string {
	return fmt.Sprintf("елемент власних даних бази з ключем %s", key)
}

// LossSetting names a setting that lost its newest value.
func LossSetting(name string) string {
	return fmt.Sprintf("налаштування бази %s", name)
}
