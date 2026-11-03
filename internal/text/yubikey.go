package text

import "fmt"

// What the tool says about a YubiKey.
const (
	// YkmanMissing says that the program is not there and what to do. The tool does not look for
	// it outside PATH, because Yubico does not name the place its installers use.
	YkmanMissing = "Для бази з YubiKey потрібна програма ykman від Yubico, але її не знайдено. Встановіть YubiKey Manager (https://docs.yubico.com/software/yubikey/tools/ykman/Install_ykman.html), перевірте, що команда ykman доступна через PATH, і запустіть цю команду ще раз."
	// YubiKeySlotAnswerInvalid answers a bad slot in the dialog. It never repeats the answer.
	YubiKeySlotAnswerInvalid = "Слот YubiKey — це 1 або 2."
	// JudgeMaybeNoTouch stands before the stop when the check of KeePassXC fails with a key: each
	// call of the program waits for its touch only for a limited time.
	JudgeMaybeNoTouch = "Можливо, YubiKey не отримав дотику вчасно: KeePassXC чекає на кожен дотик обмежений час. Якщо так, запустіть злиття ще раз."
	// MergeYubiKeyNoResponse says that the key gave no response for the new file.
	MergeYubiKeyNoResponse = "YubiKey не дав відповіді для нового файла. Новий файл має нову сіль, тому йому потрібен окремий дотик. Результат не створено, оригінали не змінено. Запустіть злиття ще раз."
)

// YubiKeySlotInvalid refuses the value of the option.
func YubiKeySlotInvalid(option string) string {
	return fmt.Sprintf("Опція %s приймає слот YubiKey: 1 або 2.", option)
}

// YubiKeySlotPrompt asks for the slot of a copy. current is the slot from the command line, or 0.
func YubiKeySlotPrompt(path string, current int) string {
	if current != 0 {
		return fmt.Sprintf("Слот YubiKey для %s, 1 або 2 (Enter — лишити слот %d): ", path, current)
	}
	return fmt.Sprintf("Слот YubiKey для %s, 1 або 2 (Enter — без YubiKey): ", path)
}

// TouchYubiKey stands just before each call of the key that can wait for a touch.
func TouchYubiKey(slot int) string {
	return fmt.Sprintf("Торкніться YubiKey, якщо він блимає (слот %d). Чекаю на відповідь ключа...", slot)
}

// WrongCredentialsWithYubiKey says that the keys do not fit, and a key was among them.
func WrongCredentialsWithYubiKey(path string, slot int) string {
	return fmt.Sprintf("Неправильний пароль, файл-ключ або слот YubiKey (%d) для %s. Спробуйте ще раз.", slot, path)
}

// YubiKeyNoResponse says that the key gave no response while a copy opened.
func YubiKeyNoResponse(slot int) string {
	return fmt.Sprintf("YubiKey не дав відповіді (слот %d). Перевірте, що вставлено один ключ і цей слот налаштовано на challenge-response. Якщо ключ чекає на дотик, торкніться його вчасно. Спробуйте ще раз.", slot)
}

// YubiKeyNeedsKDBX4 refuses a copy of KDBX 3 with a key.
func YubiKeyNeedsKDBX4(path string) string {
	return fmt.Sprintf("Копію %s не можна відкрити з YubiKey: це формат KDBX 3, а YubiKey тула підтримує лише в KDBX 4. Перетворіть копію на KDBX 4 у налаштуваннях шифрування бази в KeePassXC і запустіть цю команду ще раз.", path)
}

// MergeYubiKeyKept says that the new file needs the key too.
func MergeYubiKeyKept(slot int) string {
	return fmt.Sprintf("Новий файл потребує і того самого YubiKey, слот %d.", slot)
}

// MergeYubiKeyNew says that the new keys of the file hold a key too.
func MergeYubiKeyNew(slot int) string {
	return fmt.Sprintf("Новий файл потребує і YubiKey, слот %d.", slot)
}

// JudgeTouches says how many touches the outside check takes. Each call of keepassxc-cli asks the
// key for itself, and the tool cannot keep a response for it.
func JudgeTouches(count int) string {
	return fmt.Sprintf("Кожен крок цієї перевірки просить YubiKey окремо. Дотиків буде до %d: торкайтесь ключа щоразу, коли він блимає.", count)
}
