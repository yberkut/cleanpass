package text

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The start of the dialog and the words that work at any question.

// DialogStart says how many questions wait, and how to answer them.
func DialogStart(count int) string {
	return fmt.Sprintf("Питань до вас: %d. Відповідайте номером. Enter лишає питання без відповіді: тоді діє правило без відповіді, і дані не зникають. Відповідь «с» завершує питання: для всіх наступних діють ті самі правила.", count)
}

// QuestionNumber stands in front of a question.
func QuestionNumber(number, total int) string {
	return fmt.Sprintf("Питання %d з %d.", number, total)
}

// AnswerPrompt asks for the answer. canShow says that the question has values to show.
func AnswerPrompt(choices int, canShow bool) string {
	if canShow {
		return fmt.Sprintf("Ваша відповідь (1-%d; Enter — без відповіді; п — показати одне значення; с — більше не питати): ", choices)
	}
	return fmt.Sprintf("Ваша відповідь (1-%d; Enter — без відповіді; с — більше не питати): ", choices)
}

// AnswerNotUnderstood says which answers there are. It never repeats what was typed: a person can
// type a password into the wrong prompt.
func AnswerNotUnderstood(choices int) string {
	return fmt.Sprintf("Такої відповіді немає. Введіть номер від 1 до %d.", choices)
}

// IsStopWord says the answer ends the dialog. The Latin c looks the same as the Cyrillic с.
func IsStopWord(answer string) bool {
	return slices.Contains([]string{"с", "c", "стоп", "stop"}, answer)
}

// IsShowWord says the answer asks to show a value.
func IsShowWord(answer string) bool {
	return slices.Contains([]string{"п", "p", "показати", "show"}, answer)
}

// orDefault marks the answer that also applies with no answer.
func orDefault(answer string) string {
	return answer + " — так буде і без відповіді"
}

func numbered(number int, answer string) string { return fmt.Sprintf("%d) %s", number, answer) }

// What a question about a deletion says.
const QuestionDeletion = "Запис видалено в одній копії, а в іншій він є:"

// AnswerKeepEntry and the two below are the answers to a question about a deletion.
func AnswerKeepEntry(number int) string {
	return numbered(number, orDefault("лишити запис"))
}

// AnswerDeleteEntry deletes an entry a copy has removed.
func AnswerDeleteEntry(number int) string { return numbered(number, "видалити запис") }

// AnswerDeleteToBin is for an entry no copy has removed: it stays in the recycle bin.
func AnswerDeleteToBin(number int) string {
	return numbered(number, "видалити запис: він лишиться в кошику з усіма версіями")
}

// What a question about the versions of an entry says.
const (
	QuestionConflict = "Версії запису сперечаються:"
	ConflictClose    = "Різниця менша за 24 години, і годиннику систем не можна вірити."
	ConflictFuture   = "Одна з версій має дату з майбутнього."
	ConflictClock    = "Найновіша за часом версія вже замінена в іншій копії: так може повернутись старий пароль."
	ConflictHint     = "Вибір іншої версії створить нову версію з її вмістом. Усі наявні версії лишаться в історії."
	// VersionInNoCopy says that a version is the main one in no copy.
	VersionInNoCopy = "не головна в жодній копії"
)

// AnswerVersion is the line about one version. The first answer is what happens with no answer.
func AnswerVersion(number int, modified string, paths []string) string {
	where := VersionInNoCopy
	if len(paths) > 0 {
		where = "головна в: " + strings.Join(paths, ", ")
	}
	answer := "змінено " + modified + "; " + where
	if number == 1 {
		answer = orDefault(answer)
	}
	return numbered(number, answer)
}

// DiffersFromFirstVersion names what differs from the first version in the list.
func DiffersFromFirstVersion(differences []string) string {
	return "відрізняється від версії 1: " + strings.Join(differences, ", ")
}

// SameAsFirstVersion says that a version differs from the first one only in its time.
const SameAsFirstVersion = "вміст як у версії 1"

// What a question about a similar pair says.
const QuestionPair = "Схожі записи з різними UUID:"

// AnswerKeepBoth and the one below are the answers to a question about a pair.
func AnswerKeepBoth(number int) string {
	return numbered(number, orDefault("лишити обидва записи"))
}

// AnswerMergePair merges the two entries.
func AnswerMergePair(number int) string {
	return numbered(number, "злити в один запис: версії обох лишаться в його історії")
}

// QuestionIdenticalPairs counts the pairs one answer can settle.
func QuestionIdenticalPairs(count int) string {
	return fmt.Sprintf("Пар схожих записів з однаковим вмістом: %d. Їх можна вирішити одним рішенням.", count)
}

// AnswerAskEach and the one below are the answers to the question about all such pairs.
func AnswerAskEach(number int) string {
	return numbered(number, orDefault("вирішувати кожну пару окремо"))
}

// AnswerMergeAll merges all pairs with the same content.
func AnswerMergeAll(number int) string {
	return numbered(number, "злити всі такі пари: версії обох записів лишаться в історії")
}

// Showing one value.
const (
	// ShowWarning stands in front of the list of what can be shown.
	ShowWarning = "Увага: показане значення лишиться в прокрутці термінала, і його можна буде прочитати там і після завершення роботи. Коли закінчите, очистьте прокрутку або закрийте термінал."
	// ShowPrompt asks which value to show.
	ShowPrompt = "Номер значення (Enter — нічого не показувати): "
	// ShowNothing says that nothing was shown.
	ShowNothing = "Нічого не показано."
	// PairFirstEntry and PairSecondEntry name the entries of a pair in the list.
	PairFirstEntry  = "перший запис"
	PairSecondEntry = "другий запис"
	// OnlyEntry names the entry of a question about a deletion in the list.
	OnlyEntry = "запис"
)

// VersionName names a version in the list of what can be shown.
func VersionName(number int) string { return fmt.Sprintf("версія %d", number) }

// ShowableField is one line of the list of what can be shown. It holds a name, and no value.
func ShowableField(number int, holder, name string) string {
	return numbered(number, holder+": "+FieldName(name))
}

// standardFields gives each standard field of an entry its name for a person, and the words that
// say it differs. A value can be shown in full, so the name says so for the URL: the report
// shows only its host.
var standardFields = map[string]struct{ name, differs string }{
	snapshot.FieldTitle:    {"назва", "назва різна"},
	snapshot.FieldUserName: {"логін", "логін різний"},
	snapshot.FieldPassword: {"пароль", "пароль різний"},
	snapshot.FieldURL:      {"повний URL", "URL різний"},
	snapshot.FieldNotes:    {"нотатки", "нотатки різні"},
}

// FieldName gives the name of a string field for a person.
func FieldName(name string) string {
	if field, standard := standardFields[name]; standard {
		return field.name
	}
	return "поле «" + name + "»"
}

// ShownValue is the value the person asked to see.
func ShownValue(holder, name, value string) string {
	return holder + ", " + FieldName(name) + ": " + value
}

// What opens the result.
const (
	// QuestionKeys asks what will open the new file.
	QuestionKeys = "Чим відкриватиметься новий файл? Шифр і KDF теж беруться з копії, яку ви виберете."
	// NewKeysPasswordPrompt asks for the password of the result.
	NewKeysPasswordPrompt = "Новий пароль (Enter — без пароля): "
	// NewKeysRepeatPrompt asks for the same password again.
	NewKeysRepeatPrompt = "Повторіть новий пароль: "
	// NewKeysDifferent says that the two passwords differ.
	NewKeysDifferent = "Паролі різні. Спробуйте ще раз."
	// NewKeysKeyFilePrompt asks for the key file of the result.
	NewKeysKeyFilePrompt = "Новий файл-ключ (Enter — без файла-ключа): "
	// NewKeysSlotPrompt asks for the slot of a YubiKey for the result.
	NewKeysSlotPrompt = "Новий слот YubiKey, 1 або 2 (Enter — без YubiKey): "
	// NewKeysEmpty refuses a result that nothing protects.
	NewKeysEmpty = "Новому файлу потрібен пароль, файл-ключ або YubiKey. Спробуйте ще раз."
	// NewKeysNotGiven stops a merge when the person asked for new keys and input ended.
	NewKeysNotGiven = "Нові ключі не введено до кінця: ввід закінчився. Результат не створено, оригінали не змінено."
)

// KeysFromCopy is the answer that takes the keys of a copy.
func KeysFromCopy(number int, path string, newest bool) string {
	answer := "як копію " + path
	if newest {
		answer = orDefault(answer + " (найновіша)")
	}
	return numbered(number, answer)
}

// KeysNew is the answer that sets new keys. The cipher and the KDF come from the newest copy.
func KeysNew(number int) string {
	return numbered(number, "задати нові пароль, файл-ключ і YubiKey; шифр і KDF будуть від найновішої копії")
}
