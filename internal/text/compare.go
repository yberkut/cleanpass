package text

import (
	"fmt"
	"strings"
)

// What ends the report of compare, beside the verdicts it shares with status.
const (
	// CompareOneCopy says one file gives nothing to compare.
	CompareOneCopy = "Порівнювати нема з чим: вказано одну копію. Наступна команда не потрібна."
	// CompareNoCommandNeeded follows the verdict that the copies are the same.
	CompareNoCommandNeeded = "Наступна команда не потрібна."
	// CompareMergeAnyway stands between the direction of copying and the merge command.
	CompareMergeAnyway = "Якщо замість копіювання вам потрібен новий файл, злийте копії."
	// CompareMergeWouldStop stands in place of a command that could not work.
	CompareMergeWouldStop = "Наступної команди немає: злиття цих копій зупиниться на даних, яких тула не вміє зберегти, і обходу цієї зупинки немає."
	// CompareOtherDifferences explains a verdict no section of the report accounts for.
	CompareOtherDifferences = "Головні версії записів збігаються. Копії різняться історією записів, групами або налаштуваннями бази."
	// CompareOnlyIfOneDatabase stands in front of a command that takes off the stop on different databases.
	CompareOnlyIfOneDatabase = "Різні бази зливати не треба. Лише якщо це копії однієї бази, злийте їх із опцією, яка знімає цей захист."

	// UnrelatedWarning stands in front of the groups of copies that look like different databases.
	UnrelatedWarning = "Попередження: копії схожі на різні бази: вони не мають спільного UUID кореневої групи чи запису і мають мало схожих записів. Злиття таких копій зупиниться. Копії, схожі між собою:"
)

// UnrelatedGroup names the copies that look like one database.
func UnrelatedGroup(number int, paths []string) string {
	return fmt.Sprintf("Група %d: %s", number, strings.Join(paths, ", "))
}

// FutureStampWarning says that a copy holds a time stamp after the current time.
func FutureStampWarning(stamp string) string {
	return fmt.Sprintf("Попередження: у цій копії є часова мітка з майбутнього: %s. Годинник системи, де змінювали цю копію, міг бути збитий, тому її позначка найсвіжішої теж може бути хибною.", stamp)
}

// The headers of the sections about differences in the report of compare.
const (
	SectionOnlyInSome = "Записи лише в частині копій. Запис, якого в копії просто немає, вважається новим:"
	SectionLagging    = "Копія відстає. Це не конфлікт, питання не буде:"
	SectionDisputes   = "Версії сперечаються:"
	SectionDeletions  = "Видалення:"
	SectionPairs      = "Схожі записи з різними UUID. Під час злиття про кожну пару буде питання, а без відповіді лишаться обидва:"
	SectionGroups     = "Групи. Тула зробить це сама, без питання:"

	SectionRememberedPairs = "Записи, які ви вже злили раніше. Питання не буде:"
)

// What became of the question about a deletion.
const (
	DeletionWillAsk       = "Під час злиття буде питання, а без відповіді запис лишиться."
	DeletionDecidedKeep   = "Питання не буде: ви вже вирішили лишити цей запис."
	DeletionDecidedDelete = "Питання не буде: ви вже вирішили видалити цей запис."
)

// The reasons of a dispute between the versions of an entry.
const (
	trueConflict = "Справжній конфлікт: жодна версія не є в історії іншої. "

	// DisputeFarApart says the rule settles the conflict with no question.
	DisputeFarApart = trueConflict + "Різниця 24 години або більше, тому новіша стане головною, а старша піде в історію."
	// DisputeClose says the clocks cannot settle the conflict.
	DisputeClose = trueConflict + "Різниця менша за 24 години, і годиннику систем не можна вірити. Під час злиття буде питання, а без відповіді новіша стане головною."
	// DisputeFuture says a version in the conflict comes from the future.
	DisputeFuture = trueConflict + "Одна з версій має дату з майбутнього. Під час злиття буде питання, а без відповіді новіша стане головною."
	// DisputeClock says the clock puts a version on top that a copy has already replaced.
	DisputeClock = "Увага: збитий годинник. Найновіша за часом версія вже замінена в іншій копії, але без відповіді стане головною, і так може повернутись старий пароль. Під час злиття буде питання."
	// DisputeClockDecided says the human has already let such a version stay on top.
	DisputeClockDecided = "Збитий годинник: найновіша за часом версія вже замінена в іншій копії, але ви вже вирішили лишити її головною. Питання не буде."
)

// The parts of the line about an entry in one copy.
const (
	InHistoryOfAnother   = "ця версія є в історії іншої копії"
	NotInHistoryOfOthers = "цієї версії немає в історії інших копій"
	SameAsMainVersion    = "вміст як у версії, що стане головною"
)

// The attributes of a version that can differ. Each one says only that it differs.
const (
	DiffersIcon         = "значок різний"
	DiffersColors       = "кольори різні"
	DiffersOverrideURL  = "перевизначення URL різне"
	DiffersQualityCheck = "перевірка якості різна"
	DiffersTags         = "теги різні"
	DiffersCreation     = "дата створення різна"
	DiffersExpiry       = "термін дії різний"
	DiffersAttachments  = "вкладення різні"
	DiffersAutoType     = "Auto-Type різний"
	DiffersCustomData   = "дані розширень різні"
)

// EntryLabel tells a person which entry this is. The UUID stands in only for a missing title.
func EntryLabel(title, userName, host, uuid string) string {
	var details []string
	if userName != "" {
		details = append(details, "логін: "+userName)
	}
	if host != "" {
		details = append(details, "сайт: "+host)
	}
	if title == "" {
		title = "(без назви, UUID " + uuid + ")"
	}
	if len(details) == 0 {
		return title
	}
	return title + " (" + strings.Join(details, ", ") + ")"
}

// EntryInCopy is the line about an entry in one copy.
func EntryInCopy(path, modified string, details ...string) string {
	return path + ": " + strings.Join(append([]string{"змінено " + modified}, details...), "; ")
}

// EntryNotInCopy is the line about a copy without the entry.
func EntryNotInCopy(path string) string {
	return path + ": запису немає"
}

// DiffersFromMainVersion names what differs from the version that becomes the main one.
func DiffersFromMainVersion(differences []string) string {
	return "відрізняється від версії, що стане головною: " + strings.Join(differences, ", ")
}

// FieldDiffers says that a string field differs. This is all the report says about a secret.
func FieldDiffers(name string) string {
	switch name {
	case "Title":
		return "назва різна"
	case "UserName":
		return "логін різний"
	case "Password":
		return "пароль різний"
	case "URL":
		return "URL різний"
	case "Notes":
		return "нотатки різні"
	}
	return fmt.Sprintf("поле «%s» різне", name)
}

// DeletionDates gives the two dates a person weighs against each other.
func DeletionDates(deleted, modified string) string {
	return "Видалено " + deleted + "; остання зміна вмісту " + modified
}

// DeletionRecordedIn names the copies with a record about the deletion.
func DeletionRecordedIn(paths []string) string {
	return "Запис про видалення: " + strings.Join(paths, ", ")
}

// DeletionInBin names the copies that hold the entry in their recycle bin.
func DeletionInBin(paths []string) string {
	return "У кошику: " + strings.Join(paths, ", ")
}

// DeletionOutsideBin names the copies that hold the entry outside their recycle bin.
func DeletionOutsideBin(paths []string) string {
	return "Поза кошиком: " + strings.Join(paths, ", ")
}

// PairHeader names the two entries of a pair.
func PairHeader(first, second string) string {
	return first + " і " + second
}

// PairFirst tells about the first entry of a pair.
func PairFirst(modified string, paths []string) string {
	return "Перший запис: змінено " + modified + "; є в " + strings.Join(paths, ", ")
}

// PairSecond tells about the second entry of a pair.
func PairSecond(modified string, paths []string) string {
	return "Другий запис: змінено " + modified + "; є в " + strings.Join(paths, ", ")
}

// PairCommon says what the entries of a pair have in common and whether their passwords are the
// same. This is all a person learns about the passwords.
func PairCommon(titleAndUser, userAndHost, samePassword, sameContent bool) string {
	var common []string
	if titleAndUser {
		common = append(common, "назва і логін")
	}
	if userAndHost {
		common = append(common, "логін і сайт")
	}
	line := "Спільне: " + strings.Join(common, ", ")
	if samePassword {
		line += "; пароль однаковий"
	} else {
		line += "; пароль різний"
	}
	if sameContent {
		line += "; вміст однаковий"
	}
	return line
}

// PairDiffers names what differs between the entries of a pair.
func PairDiffers(differences []string) string {
	return "Відрізняється: " + strings.Join(differences, ", ")
}

// PairsIdentical counts the pairs one decision can merge.
func PairsIdentical(count int) string {
	return fmt.Sprintf("Пар з однаковим вмістом: %d. Під час злиття їх можна злити одним рішенням.", count)
}

// PairRemembered tells about an entry that took other entries in during an earlier merge, while
// copies still hold those entries under their old UUIDs.
func PairRemembered(count int, paths []string) string {
	return fmt.Sprintf("Раніше сюди злито записів: %d. Під старим UUID вони ще є в: %s", count, strings.Join(paths, ", "))
}

// GroupsJoin names a group that takes other groups in.
func GroupsJoin(name string, others int) string {
	return fmt.Sprintf("%s: в одну групу з нею об'єднається ще груп: %d (однаковий шлях або кошик)", name, others)
}

// GroupLeavesCycle names a group that leaves its parent to break a cycle.
func GroupLeavesCycle(name string) string {
	return name + ": рухи в різних копіях зациклили групи, тому ця група стане під кореневу"
}

// UnknownFacts says that the report cannot show part of what the merge said.
func UnknownFacts(count int) string {
	return fmt.Sprintf("Ще є відомості про злиття, яких цей звіт не вміє показати: %d.", count)
}
