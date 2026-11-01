package text

import "fmt"

// FileNotFound says that a copy does not exist.
func FileNotFound(path string) string {
	return fmt.Sprintf("Файл не знайдено: %s.", path)
}

// FileUnreadable says that the system does not let the tool read a file.
func FileUnreadable(path string) string {
	return fmt.Sprintf("Не вдалося прочитати файл %s.", path)
}

// NotADatabase says that a file is not a database the tool knows.
func NotADatabase(path string) string {
	return fmt.Sprintf("Файл %s не схожий на базу KeePass (KDBX 3 або KDBX 4).", path)
}

// DatabaseDamaged says that a file looks like a database but cannot be read.
func DatabaseDamaged(path string) string {
	return fmt.Sprintf("Не вдалося прочитати базу %s: файл пошкоджений або має непідтримуваний формат.", path)
}

// UnknownCommand says that the command does not exist.
func UnknownCommand(name string) string {
	return fmt.Sprintf("Невідома команда: %s.", name)
}

// UnknownOption says that the option does not exist. It names only the option,
// never its value.
func UnknownOption(name string) string {
	return fmt.Sprintf("Невідома опція: %s.", name)
}

// OptionNeedsValue says that an option came without its value.
func OptionNeedsValue(name string) string {
	return fmt.Sprintf("Опція %s потребує значення.", name)
}

// OptionNeedsFile says that a per-copy option came before any file.
func OptionNeedsFile(name string) string {
	return fmt.Sprintf("Опція %s стосується файла перед нею, але файла перед нею немає.", name)
}

// NoFiles says that the command got no files.
const NoFiles = "Не вказано жодного файла бази."
