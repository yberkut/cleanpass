package text

import "fmt"

// Labels of the passport of a copy.
const (
	LabelFileSize    = "Розмір файлу (байт)"
	LabelFileDate    = "Дата файлу"
	LabelLastChange  = "Остання зміна в базі"
	LabelEntries     = "Записів"
	LabelGroups      = "Груп"
	LabelAttachments = "Вкладень"
	LabelDeletions   = "Записів про видалення"
	LabelFormat      = "Формат"
	LabelCipher      = "Шифр"
	LabelKDF         = "KDF"

	// NoDate stands where a database has no date at all.
	NoDate = "немає"

	CipherAES256   = "AES-256"
	CipherChaCha20 = "ChaCha20"
	CipherTwofish  = "Twofish"

	KDFNameArgon2d  = "Argon2d"
	KDFNameArgon2id = "Argon2id"
)

// CopyHeader is the first line of the passport of a copy.
func CopyHeader(number int, path string, freshest bool) string {
	if freshest {
		return fmt.Sprintf("Копія %d: %s  [найсвіжіша]", number, path)
	}
	return fmt.Sprintf("Копія %d: %s", number, path)
}

// FormatName names the file format.
func FormatName(major, minor uint16) string {
	return fmt.Sprintf("KDBX %d.%d", major, minor)
}

// KDFAES describes AES-KDF with its cost.
func KDFAES(rounds uint64) string {
	return fmt.Sprintf("AES-KDF (раундів: %d)", rounds)
}

// KDFArgon2 describes an Argon2 kind with its cost.
func KDFArgon2(name string, memoryMiB, iterations uint64, parallelism uint32) string {
	return fmt.Sprintf("%s (пам'ять, МіБ: %d; ітерацій: %d; потоків: %d)", name, memoryMiB, iterations, parallelism)
}
