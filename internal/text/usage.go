// Package text holds all the text for the human, in Ukrainian. No other package
// writes words for a person: they call this one.
package text

// Usage explains the commands.
const Usage = `cleanpass — безпечна робота з копіями бази KeePassXC.

Використання:
  cleanpass compare <файл> [--key-file <файл-ключ>] [<файл> [--key-file <файл-ключ>]]...
      Показує паспорт кожної копії і позначає найсвіжішу. Нічого не змінює на диску.

Опції:
  -k, --key-file <файл-ключ>   Файл-ключ для копії. Опція стосується файла перед нею.
  -h, --help                   Показати цю довідку.

Пароль:
  Майстер-пароль не передається через аргументи чи змінні середовища.
  З термінала він вводиться приховано, інакше читається рядками зі stdin.
  Enter повторює вже введений пароль. Після неправильного пароля є нова спроба.
`
