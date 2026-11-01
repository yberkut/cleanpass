package text

import "fmt"

// The verdicts of the status command and what stands around them.
const (
	// StatusNeedsTwoCopies says status has nothing to compare one file with.
	StatusNeedsTwoCopies = "Команда status порівнює щонайменше дві копії."
	// StatusSameBytes says all files hold the same bytes.
	StatusSameBytes = "Копії однакові: файли збігаються побайтно. Пароль не потрібен."
	// StatusSameContent says the files differ, but only in what a plain save renews.
	StatusSameContent = "Копії однакові: записи, групи й налаштування збігаються. Файли різняться лише службовими даними або шифруванням."
	// StatusNoMergeNeeded ends the verdict about copies that lag.
	StatusNoMergeNeeded = "Злиття не потрібне: копія, що випереджає, вже містить усе, що є в інших. Файл копіюєте ви, тула нічого не змінює."
	// StatusDiverged says no copy holds all that the others hold.
	StatusDiverged = "Копії розійшлись: жодна з них не містить усього, що є в інших."
	// StatusNextCommand stands in front of the command that merges the copies.
	StatusNextCommand = "Наступна команда:"
	// StatusVerdictIsBlind follows the warning of the field guard.
	StatusVerdictIsBlind = "Вердикт не враховує цих даних: у копіях вони можуть бути різними."
	// StatusFailed says the tool could not get to a verdict.
	StatusFailed = "Не вдалося порівняти копії."
)

// StatusAhead names a copy that holds all that the others hold, and more than some of them.
func StatusAhead(path string) string {
	return fmt.Sprintf("Випереджає: %s", path)
}

// StatusBehind names a copy that another copy holds in full.
func StatusBehind(path string) string {
	return fmt.Sprintf("Відстає: %s", path)
}

// StatusDirection says which file should go where.
func StatusDirection(from, to string) string {
	return fmt.Sprintf("Напрямок копіювання: %s -> %s", from, to)
}

// StatusCopyHeader names the copy a warning is about.
func StatusCopyHeader(path string) string {
	return fmt.Sprintf("Копія %s:", path)
}
