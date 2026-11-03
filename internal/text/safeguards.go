package text

import "fmt"

// FieldLossWarning stands in front of the paths of data a save would not keep.
const FieldLossWarning = "Попередження: у цій копії є дані, яких тула не вміє зберегти. Злиття такої копії зупиниться. Шляхи:"

// FieldLossLine names one path with the amount of data there now and after a save.
func FieldLossLine(path string, inFile, kept int) string {
	return fmt.Sprintf("%s (у файлі: %d, лишиться: %d)", path, inFile, kept)
}

// EmptyTime says that a copy holds an empty time element, and what helps.
func EmptyTime(path string) string {
	return fmt.Sprintf("База %s має порожнє поле часу, і тула не може її прочитати. Відкрийте копію в KeePassXC і збережіть її: після цього тула її прочитає.", path)
}

// kdfAdvice says what the person can do about a KDF the tool cannot use.
const kdfAdvice = "Якщо KeePassXC відкриває цю копію, змініть у ньому KDF у налаштуваннях бази і збережіть її."

// KDFUnknown says that a copy names a KDF the tool does not know.
func KDFUnknown(path string) string {
	return fmt.Sprintf("База %s використовує незнайомий KDF. Тула знає AES-KDF і Argon2. %s", path, kdfAdvice)
}

// KDFVersion says that a copy uses a version of Argon2 the tool cannot run.
func KDFVersion(path string) string {
	return fmt.Sprintf("База %s використовує версію Argon2, відмінну від 0x13. Тула вміє лише цю версію. %s", path, kdfAdvice)
}

// KDFKeyed says that the KDF of a copy uses a secret key or extra data.
func KDFKeyed(path string) string {
	return fmt.Sprintf("У параметрах KDF бази %s заповнено K або A. Тула не вміє їх враховувати. %s", path, kdfAdvice)
}

// KDFLimits says that the KDF parameters of a copy are not ones the tool can use.
func KDFLimits(path string) string {
	return fmt.Sprintf("Параметри KDF бази %s неповні, незнайомі або виходять за межі, які тула вміє обробити. %s", path, kdfAdvice)
}

// TestMode warns that the variable of the faults for tests is set.
func TestMode(variable string) string {
	return fmt.Sprintf("!!! УВАГА: ТЕСТОВИЙ РЕЖИМ. Змінна %s вмикає збої для тестів: обрив запису, паузу, інший спосіб публікації і пошук KeePassXC без стандартних місць встановлення. Приберіть цю змінну, якщо ви не запускаєте тести. !!!", variable)
}
