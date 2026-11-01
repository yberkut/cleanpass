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

// NoPasswordGiven says that stdin ended before a password came.
func NoPasswordGiven(path string) string {
	return fmt.Sprintf("Пароль для %s не введено: ввід закінчився.", path)
}
