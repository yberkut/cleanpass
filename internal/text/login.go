package text

import "fmt"

// PasswordPrompt asks for the password of a copy. canRepeat says that Enter repeats a
// password that was already typed in this run.
func PasswordPrompt(path string, canRepeat bool) string {
	if canRepeat {
		return fmt.Sprintf("Пароль для %s (Enter — повторити вже введений): ", path)
	}
	return fmt.Sprintf("Пароль для %s: ", path)
}

// KeyFilePrompt asks for the key file of a copy. fromOption is the key file the command
// line gave, or empty.
func KeyFilePrompt(path, fromOption string) string {
	if fromOption != "" {
		return fmt.Sprintf("Файл-ключ для %s (Enter — лишити %s): ", path, fromOption)
	}
	return fmt.Sprintf("Файл-ключ для %s (Enter — без файла-ключа): ", path)
}

// WrongCredentials says that the keys do not fit and that the person can try again.
func WrongCredentials(path string) string {
	return fmt.Sprintf("Неправильний пароль або файл-ключ для %s. Спробуйте ще раз.", path)
}

// KeyFileUnreadable says that the key file cannot be used.
func KeyFileUnreadable(path string) string {
	return fmt.Sprintf("Не вдалося прочитати файл-ключ %s.", path)
}

// TerminalShowsPasswords refuses a terminal of Git Bash, where a typed password would show on the
// screen. It tells what works instead. A plain pipe from a script is not refused.
const TerminalShowsPasswords = "Цей термінал показував би пароль під час введення: mintty у Git Bash передає програмам введення через канал, а не через консоль, і сховати його неможливо. Запустіть команду через winpty (winpty cleanpass ...) або у Windows Terminal. Скрипт, який передає паролі через канал, працює без змін."

// Interrupted says that Ctrl+C has ended the work.
const Interrupted = "Перервано. Оригінали не змінено."

// InterruptedTempLeft says that a temporary file could not be removed after Ctrl+C.
func InterruptedTempLeft(path string) string {
	return fmt.Sprintf("Не вдалося прибрати тимчасовий файл %s. Він зашифрований, його можна видалити.", path)
}

// InterruptedAfterPublish says that Ctrl+C came when the result was already in its place.
func InterruptedAfterPublish(path string) string {
	return fmt.Sprintf("Файл %s уже створено, але перевірку незмінності оригіналів наприкінці не завершено. Щоб отримати цю перевірку, видаліть його і запустіть злиття ще раз.", path)
}

// NoPasswordGiven says that stdin ended before a password came.
func NoPasswordGiven(path string) string {
	return fmt.Sprintf("Пароль для %s не введено: ввід закінчився.", path)
}
