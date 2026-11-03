#!/usr/bin/env bash
#
# The manual check of cleanpass: a wizard that walks a person through what only a person can
# check, step by step. It began as a script of the /wizard skill. Only the helpers the stages use
# stay, and all text for the person is Ukrainian, as in the tool.

set -euo pipefail

# ──────────────────────────────────────────────────────────────────────────
# Helpers: how the wizard shows a step and asks the person.
# ──────────────────────────────────────────────────────────────────────────

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  BOLD=$(tput bold); DIM=$(tput dim); RESET=$(tput sgr0)
  BLUE=$(tput setaf 4); YELLOW=$(tput setaf 3)
else
  BOLD=""; DIM=""; RESET=""; BLUE=""; YELLOW=""
fi

# run_wizard sets this to the number of stages.
TOTAL_STAGES=0

_STAGE_INDEX=0

# _clear wipes the terminal so only the current step is on screen. No-op when
# output isn't a terminal, so piped logs stay readable.
_clear() {
  [[ -t 1 ]] || return 0
  if command -v tput >/dev/null 2>&1 && tput clear 2>/dev/null; then return 0; fi
  printf '\033[2J\033[3J\033[H'
}

# stage "Name" clears the screen, then announces a stage and shows progress.
stage() {
  _clear
  _STAGE_INDEX=$((_STAGE_INDEX + 1))
  printf '\n%s%s▸ Крок %s/%s · %s%s\n\n' "$BOLD" "$BLUE" "$_STAGE_INDEX" "$TOTAL_STAGES" "$1" "$RESET"
}

# say "..." prints a plain instruction line.
say()  { printf '  %s\n' "$1"; }
# step "..." is an action the person takes.
step() { printf '  %s•%s %s\n' "$BLUE" "$RESET" "$1"; }
note() { printf '  %s%s%s\n' "$DIM" "$1" "$RESET"; }
warn() { printf '  %s⚠ %s%s\n' "$YELLOW" "$1" "$RESET"; }

# pause "msg" waits for the person to confirm they've done the manual part.
pause() {
  printf '  %s%s%s ' "$DIM" "${1:-Натисніть Enter, щоб іти далі.}" "$RESET"
  read -r _ || true
}

# confirm "question" is a yes-or-no gate; returns success on yes. A person may answer in
# Ukrainian or in English. Anything else, Enter too, is no.
confirm() {
  local reply=""
  printf '  %s? %s [т або y — так, Enter — ні] ' "$YELLOW" "$1"
  read -r reply || true
  case "$reply" in
    [Yy]*|т*|Т*) return 0 ;;
  esac
  return 1
}

# ──────────────────────────────────────────────────────────────────────────
# Stages. Keep every stage inside run_wizard and the last line as is: bash then parses the whole
# file before the first prompt, so an edit mid-run can't break it.
# ──────────────────────────────────────────────────────────────────────────

# The manual check of cleanpass. It covers what no agent could check: a real ykman, a real
# YubiKey, a base KeePassXC made, a result opened in the KeePassXC window, the time a person has
# for a touch, the terminal of Git Bash, and real bases.
#
# It runs in the terminal of the person and sends nothing anywhere. A password goes from the
# keyboard straight to cleanpass or KeePassXC: this script never reads one, it keeps no output of a
# run with real data, and it writes nothing outside its own temporary directory of synthetic files.

# Opens the synthetic bases of this check. It is no secret, and it protects nothing real.
readonly SYNTHETIC_PASSWORD="cleanpass-synthetic-check"
# Lets a test run the stages that need no person, with stdin from a pipe. The stages with a real key
# and real bases never run in this mode, so no model can get to them this way.
readonly SYNTHETIC_ONLY="${CLEANPASS_MANUAL_CHECK_SYNTHETIC_ONLY:-}"

readonly YES="так"
readonly NO="НІ"
readonly NOT_CHECKED="не перевірено"

RESULTS=()
TOOL=""
KEEPASSXC=""
WORK=""
SYNTHETIC_BASE=""
SLOT=""
YUBIKEY_BASE=""
YUBIKEY_RESULT=""
CODE=0

record() { RESULTS+=("$1: $2"); }

# verdict asks a person what only a person can see. Enter leaves the check open.
verdict() {
  local name="$1" question="$2" reply=""
  printf '  %s? %s%s [y — так, n — ні, Enter — не перевіряв] ' "$YELLOW" "$question" "$RESET"
  read -r reply || true
  case "$reply" in
    [Yy]*|т*|Т*) record "$name" "$YES" ;;
    [Nn]*|н*|Н*) record "$name" "$NO" ;;
    *) record "$name" "$NOT_CHECKED" ;;
  esac
}

# code_of runs a command and keeps its exit code, whatever it is.
code_of() {
  CODE=0
  "$@" || CODE=$?
}

on_windows() {
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) return 0 ;;
  esac
  return 1
}

under_mintty() { on_windows && [[ "${TERM_PROGRAM:-}" == "mintty" ]]; }

# interactive gives a program the terminal of the person. In mintty only winpty hides a password.
interactive() {
  if under_mintty && command -v winpty >/dev/null 2>&1; then
    winpty "$@"
  else
    "$@"
  fi
}

# native_path gives a path in a form both Git Bash and a Windows program understand.
native_path() {
  if on_windows && command -v cygpath >/dev/null 2>&1; then
    cygpath -m "$1"
  else
    printf '%s' "$1"
  fi
}

# kxc runs keepassxc-cli. Git Bash must not turn a group path such as /group into a Windows path.
kxc() { MSYS_NO_PATHCONV=1 "$KEEPASSXC" "$@"; }

find_tool() {
  local here os arch name candidate
  here=$(cd "$(dirname "$0")" && pwd)
  case "$(uname -s)" in
    Linux*) os=linux ;;
    Darwin*) os=darwin ;;
    MINGW*|MSYS*|CYGWIN*) os=windows ;;
    *) os=unknown ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) arch=unknown ;;
  esac
  name="cleanpass-$os-$arch"
  if [[ "$os" == windows ]]; then name="$name.exe"; fi
  for candidate in "${1:-}" "$here/$name" "$here/cleanpass" "$here/cleanpass.exe" "$here/../cleanpass" "$here/../cleanpass.exe" ./cleanpass ./cleanpass.exe; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
      TOOL="$candidate"
      return 0
    fi
  done
  if command -v cleanpass >/dev/null 2>&1; then
    TOOL=$(command -v cleanpass)
    return 0
  fi
  return 1
}

find_keepassxc() {
  local candidate
  for candidate in "$(command -v keepassxc-cli 2>/dev/null || true)" "C:/Program Files/KeePassXC/keepassxc-cli.exe" "/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli"; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
      KEEPASSXC="$candidate"
      return 0
    fi
  done
  return 1
}

# make_synthetic creates a base with two entries, with the real keepassxc-cli.
make_synthetic() {
  printf '%s\n%s\n' "$SYNTHETIC_PASSWORD" "$SYNTHETIC_PASSWORD" | kxc db-create -q -t 100 -p "$1" &&
    printf '%s\n' "$SYNTHETIC_PASSWORD" | kxc add -q -u synthetic-user --url https://example.invalid "$1" "/Перший запис" &&
    printf '%s\n' "$SYNTHETIC_PASSWORD" | kxc add -q -u synthetic-other "$1" "/Другий запис"
}

# no_person_here says a stage needs a person and none is here. That counts as not checked.
no_person_here() {
  if [[ -z "$SYNTHETIC_ONLY" ]]; then return 1; fi
  record "$1" "$NOT_CHECKED"
  return 0
}

ask_slot() {
  if [[ -n "$SLOT" ]]; then return 0; fi
  printf '  Який слот YubiKey налаштовано на challenge-response? Введіть 1 або 2 (Enter — ключа немає): '
  read -r SLOT || true
  case "$SLOT" in
    1|2) ;;
    *) SLOT="" ;;
  esac
}

stage_start() {
  stage "Підготовка"
  say "Цей скрипт перевіряє те, чого не могла перевірити модель: справжній ykman, справжній"
  say "YubiKey, базу з KeePassXC, результат у вікні KeePassXC, час на дотик, термінал Git Bash і"
  say "справжні бази. Кроки зі справжніми даними необов'язкові: скрипт спитає перед кожним."
  say ""
  warn "Запускайте скрипт лише у власному терміналі. Не запускайте його через Claude чи іншу модель."
  warn "Майстер-паролі вводьте лише у запити cleanpass і KeePassXC, а не в чат і не в цей скрипт."
  say ""
  say "Скрипт нікуди нічого не надсилає. Паролі він не читає, вивід зі справжніми даними не зберігає."
  say "Усі файли цієї перевірки він кладе у тимчасову директорію і видаляє її наприкінці. Підсумок"
  say "наприкінці не містить ні паролів, ні шляхів, ні назв записів, тому його можна переслати."
  say ""
  say "Система: $(uname -s) $(uname -m)"
  say "cleanpass: $TOOL"
  say "keepassxc-cli: $KEEPASSXC, версія $(kxc --version | tr -d '\r')"
  if command -v ykman >/dev/null 2>&1; then
    say "ykman: $(command -v ykman)"
  else
    warn "ykman не знайдено. Кроки з YubiKey будуть пропущені, якщо ви не встановите його зараз."
    note "https://docs.yubico.com/software/yubikey/tools/ykman/Install_ykman.html"
  fi
  record "Система" "$(uname -s) $(uname -m)"
  record "Версія keepassxc-cli" "$(kxc --version | tr -d '\r')"
  say ""
  pause "Натисніть Enter, щоб почати."
}

stage_mintty() {
  stage "Git Bash: відмова в mintty"
  local name="Відмова в mintty за 2 с"
  if ! on_windows; then
    say "Це не Windows, тому цей крок не стосується цієї системи."
    record "$name" "не стосується цієї системи"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  if ! under_mintty; then
    warn "Цей термінал не mintty. Відкрийте вікно Git Bash і запустіть цей скрипт там."
    record "$name" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  say "Зараз запуститься: cleanpass compare <синтетична база>"
  say "Очікування: приблизно за 2 с тула відмовляється, називає winpty і не просить пароля."
  say "Нічого не вводьте. Якщо за 20 с нічого не станеться, скрипт зупинить тулу сам."
  say ""
  local took refusal="$WORK/refusal.txt"
  SECONDS=0
  code_of timeout --foreground 20 "$TOOL" compare "$SYNTHETIC_BASE" 2>"$refusal"
  took=$SECONDS
  cat "$refusal"
  say ""
  say "Код виходу: $CODE. Час: $took с."
  if [[ "$CODE" == 3 ]] && grep -q winpty "$refusal" && ! grep -q "Пароль для" "$refusal"; then
    if [[ "$took" -le 5 ]]; then
      record "$name" "$YES ($took с)"
    else
      record "$name" "$NO: відмова є, але $took с"
    fi
  else
    record "$name" "$NO: код $CODE, $took с"
  fi
  pause "Натисніть Enter, щоб іти далі."
}

stage_hidden() {
  stage "Прихований пароль і Ctrl+C"
  if no_person_here "Пароль не видно під час введення"; then return 0; fi
  say "Синтетичний пароль цього кроку, він нічого не захищає: $SYNTHETIC_PASSWORD"
  step "Коли тула запитає пароль, введіть його. Символи не повинні з'являтись."
  say ""
  code_of interactive "$TOOL" compare "$SYNTHETIC_BASE"
  say ""
  verdict "Пароль не видно під час введення" "Символи пароля НЕ з'являлись на екрані?"
  say ""
  step "Зараз тула запитає пароль ще раз. Натисніть Ctrl+C, нічого не вводячи."
  pause "Натисніть Enter, щоб почати."
  trap ':' INT
  code_of interactive "$TOOL" compare "$SYNTHETIC_BASE"
  trap - INT
  say ""
  say "Код виходу: $CODE. Очікування: 130."
  if [[ "$CODE" == 130 ]]; then
    record "Код виходу після Ctrl+C" "$YES (130)"
  else
    record "Код виходу після Ctrl+C" "$NO ($CODE)"
  fi
  printf '  Введіть будь-яке слово і натисніть Enter: '
  read -r _ || true
  verdict "Після Ctrl+C термінал знову показує введення" "Слово, яке ви ввели, було видно?"
}

stage_ykman() {
  stage "Справжній ykman"
  local format="Вивід ykman: один рядок із 40 hex-символів" window="Вікно дотику ykman"
  if ! command -v ykman >/dev/null 2>&1; then
    warn "ykman не знайдено. Кроки з YubiKey лишаються не перевіреними."
    record "$format" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  say "Тула викликає: ykman otp calculate <слот> <challenge у hex>. Цей крок дивиться, чи друкує"
  say "справжній ykman те, чого чекає тула. Сама відповідь ключа на екран не виводиться."
  say "Цей крок нічого не записує в ключ. Команду ykman otp chalresp скрипт не запускає: вона"
  say "перезаписала б слот."
  say ""
  ask_slot
  if [[ -z "$SLOT" ]]; then
    warn "Слот не вказано. Кроки з YubiKey лишаються не перевіреними."
    record "$format" "$NOT_CHECKED"
    return 0
  fi
  # The same shape as a real challenge: a salt of 32 bytes and PKCS#7 padding up to 64 bytes.
  local challenge output status took
  challenge="$(printf '5a%.0s' {1..32})$(printf '20%.0s' {1..32})"
  step "Торкніться ключа, коли він заблимає."
  SECONDS=0
  if output=$(ykman otp calculate "$SLOT" "$challenge"); then status=0; else status=$?; fi
  took=$SECONDS
  output=${output//$'\r'/}
  if [[ "$status" == 0 && "$output" =~ ^[0-9a-fA-F]{40}$ ]]; then
    say "Вивід має очікуваний вигляд."
    record "$format" "$YES"
  else
    warn "Вивід інший, або ykman завершився з кодом $status. Тула не прочитає таку відповідь."
    record "$format" "$NO (код $status)"
  fi
  output=""
  say ""
  step "Тепер НЕ торкайтесь ключа. Скрипт вимірює, скільки ykman чекає на дотик."
  pause "Натисніть Enter і не торкайтесь ключа."
  SECONDS=0
  if output=$(ykman otp calculate "$SLOT" "$challenge"); then status=0; else status=$?; fi
  took=$SECONDS
  output=""
  say "ykman чекав $took с і завершився з кодом $status."
  record "$window" "$took с (код $status)"
  pause "Натисніть Enter, щоб іти далі."
}

stage_yubikey_base() {
  stage "База з YubiKey, створена в KeePassXC"
  local name="cleanpass відкрив базу з YubiKey із KeePassXC"
  if no_person_here "$name"; then return 0; fi
  ask_slot
  if [[ -z "$SLOT" ]] || ! command -v ykman >/dev/null 2>&1; then
    warn "Потрібні ykman і слот YubiKey. Крок пропущено."
    record "$name" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  say "Створіть нову базу лише для цієї перевірки. Назви пунктів нижче написані з пам'яті і можуть"
  say "відрізнятись у вашій версії KeePassXC: шукайте Challenge-Response і YubiKey."
  say ""
  step "У вікні KeePassXC: База даних → Нова база даних. Формат і шифр лишіть типовими."
  step "Пароль бази, він нічого не захищає: $SYNTHETIC_PASSWORD"
  step "Там само: додатковий захист → Challenge-Response → ваш YubiKey, слот $SLOT."
  step "Додайте запис із вкладенням. Змініть цей запис ще раз, щоб у нього була історія."
  step "Збережіть базу в будь-якому місці. Справжніх паролів у ній бути не повинно."
  say ""
  printf '  Шлях до цієї бази (Enter — пропустити): '
  local path=""
  read -r path || true
  if [[ -z "$path" || ! -f "$path" ]]; then
    warn "Файла немає. Крок пропущено."
    record "$name" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  YUBIKEY_BASE=$(native_path "$path")
  say ""
  step "Введіть пароль, а потім торкніться ключа, коли тула попросить."
  say ""
  code_of interactive "$TOOL" compare "$YUBIKEY_BASE" --yubikey "$SLOT"
  say ""
  say "Код виходу: $CODE. Очікування: 0."
  if [[ "$CODE" == 0 ]]; then
    record "$name" "$YES"
  else
    record "$name" "$NO (код $CODE)"
    warn "Якщо тула сказала про неправильний пароль, це і є головна знахідка: зберігайте лише цей висновок."
  fi
  pause "Натисніть Enter, щоб іти далі."
}

stage_touch_windows() {
  stage "Злиття з YubiKey і час на дотики"
  local merged="Злиття копій із YubiKey" window="Вікно дотику keepassxc-cli"
  if no_person_here "$merged"; then return 0; fi
  if [[ -z "$YUBIKEY_BASE" ]]; then
    warn "Немає бази з попереднього кроку. Крок пропущено."
    record "$merged" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  local copy="$WORK/yubikey-copy.kdbx" took
  cp "$YUBIKEY_BASE" "$copy"
  step "Копія бази отримує нову групу через keepassxc-cli. Торкніться ключа, коли він заблимає."
  say ""
  if printf '%s\n' "$SYNTHETIC_PASSWORD" | kxc mkdir -q --yubikey "$SLOT" "$copy" /from-manual-check; then
    say "Групу додано."
  else
    warn "keepassxc-cli не зміг змінити копію. Чи використали ви пароль $SYNTHETIC_PASSWORD?"
    record "$merged" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  say ""
  step "Тепер НЕ торкайтесь ключа. Скрипт вимірює, скільки keepassxc-cli чекає на дотик."
  pause "Натисніть Enter і не торкайтесь ключа."
  SECONDS=0
  code_of kxc ls -q --yubikey "$SLOT" "$copy" < <(printf '%s\n' "$SYNTHETIC_PASSWORD") >/dev/null 2>&1
  took=$SECONDS
  say "keepassxc-cli чекав $took с і завершився з кодом $CODE."
  record "$window" "$took с (код $CODE)"
  say ""
  say "Зараз запуститься злиття. Пароль той самий: $SYNTHETIC_PASSWORD"
  step "Стежте за ключем і за написом тули: вона каже, скільки дотиків буде. Рахуйте їх."
  pause "Натисніть Enter, щоб почати."
  YUBIKEY_RESULT="$WORK/yubikey-result.kdbx"
  code_of interactive "$TOOL" merge "$YUBIKEY_BASE" --yubikey "$SLOT" "$copy" --yubikey "$SLOT" --output "$YUBIKEY_RESULT"
  say ""
  say "Код виходу: $CODE. Очікування: 0."
  if [[ "$CODE" == 0 ]]; then
    record "$merged" "$YES"
  else
    record "$merged" "$NO (код $CODE)"
    YUBIKEY_RESULT=""
  fi
  local touches=""
  printf '  Скільки разів ви торкались ключа? (Enter — не рахували): '
  read -r touches || true
  case "$touches" in
    ''|*[!0-9]*) record "Дотиків за одне злиття" "$NOT_CHECKED" ;;
    *) record "Дотиків за одне злиття" "$touches" ;;
  esac
  verdict "Часу вистачило на кожен дотик" "Ви встигали торкнутись ключа кожного разу?"
}

stage_result_in_keepassxc() {
  stage "Результат у вікні KeePassXC"
  local opened="KeePassXC відкрив результат із YubiKey"
  if no_person_here "$opened"; then return 0; fi
  if [[ -z "$YUBIKEY_RESULT" ]]; then
    warn "Результату з попереднього кроку немає. Крок пропущено."
    record "$opened" "$NOT_CHECKED"
    pause "Натисніть Enter, щоб іти далі."
    return 0
  fi
  say "Файл результату: $YUBIKEY_RESULT"
  say "Пароль: $SYNTHETIC_PASSWORD, ключ: YubiKey, слот $SLOT."
  say ""
  step "Відкрийте цей файл у вікні KeePassXC."
  verdict "$opened" "KeePassXC відкрив файл?"
  step "Перевірте: є запис, його історія, вкладення відкривається, є група from-manual-check."
  verdict "У результаті все на місці" "Усе це є?"
  step "Змініть щось у записі та збережіть файл у KeePassXC. Закрийте базу."
  pause "Натисніть Enter, коли збережете."
  say ""
  step "Тула порівнює збережений файл із копією. Очікування: результат випереджає, код виходу 1."
  say ""
  code_of interactive "$TOOL" status "$YUBIKEY_RESULT" --yubikey "$SLOT" "$YUBIKEY_BASE" --yubikey "$SLOT"
  say ""
  say "Код виходу: $CODE."
  record "Статус після збереження в KeePassXC" "код $CODE"
  pause "Натисніть Enter, щоб іти далі."
}

stage_desktop() {
  stage "Збереження у вікні KeePassXC без ключа"
  local name="Тула розуміє збереження у вікні KeePassXC"
  if no_person_here "$name"; then return 0; fi
  say "Усі перевірки поведінки йшли через keepassxc-cli. Цей крок дивиться на вікно KeePassXC."
  local copy="$WORK/desktop-copy.kdbx" report="$WORK/desktop-report.txt"
  cp "$SYNTHETIC_BASE" "$copy"
  say ""
  say "Файл: $copy"
  say "Пароль: $SYNTHETIC_PASSWORD"
  say ""
  step "Відкрийте цей файл у вікні KeePassXC."
  step "У записі «Перший запис» змініть нотатки."
  step "Запис «Другий запис» перемістіть у кошик."
  step "Збережіть файл і закрийте базу."
  pause "Натисніть Enter, коли збережете."
  # The password here is the synthetic one, and it stays out of the report.
  code_of "$TOOL" compare "$SYNTHETIC_BASE" "$copy" < <(printf '%s\n%s\n' "$SYNTHETIC_PASSWORD" "$SYNTHETIC_PASSWORD") >"$report" 2>&1
  say ""
  if [[ "$CODE" == 0 ]] && grep -q "Копія відстає" "$report" && grep -q "Видалення:" "$report" && ! grep -q "Версії сперечаються" "$report"; then
    say "Є відставання, є питання про видалення, суперечок немає."
    record "$name" "$YES"
  else
    warn "Очікування інше: зміна як відставання, кошик як видалення, без суперечок. Звіт нижче."
    cat "$report"
    record "$name" "$NO (код $CODE)"
  fi
  pause "Натисніть Enter, щоб іти далі."
}

stage_real_bases() {
  stage "Справжні бази (необов'язково)"
  local name="Злиття справжніх баз"
  if no_person_here "$name"; then return 0; fi
  say "Цей крок працює з вашими справжніми копіями. Оригінали не змінюються: новий файл ляже туди,"
  say "куди ви скажете, і наявний файл тула не перезапише. Тула питає паролі сама, а цей скрипт їх"
  say "не бачить і вивід не зберігає. Звіт на екрані містить назви і логіни: не показуйте його моделі."
  say ""
  if ! confirm "Перевірити на справжніх базах"; then
    record "$name" "$NOT_CHECKED"
    return 0
  fi
  local arguments=() path key slot count=0 output=""
  while :; do
    printf '  Шлях до копії %s (Enter — більше немає): ' "$((count + 1))"
    path=""
    read -r path || true
    if [[ -z "$path" ]]; then break; fi
    if [[ ! -f "$path" ]]; then
      warn "Файла немає."
      continue
    fi
    arguments+=("$(native_path "$path")")
    printf '  Файл-ключ цієї копії (Enter — немає): '
    key=""
    read -r key || true
    if [[ -n "$key" ]]; then arguments+=(--key-file "$(native_path "$key")"); fi
    printf '  Слот YubiKey цієї копії (Enter — немає): '
    slot=""
    read -r slot || true
    if [[ "$slot" == 1 || "$slot" == 2 ]]; then arguments+=(--yubikey "$slot"); fi
    count=$((count + 1))
  done
  if [[ "$count" == 0 ]]; then
    record "$name" "$NOT_CHECKED"
    return 0
  fi
  say ""
  step "Спершу порівняння: воно нічого не змінює."
  say ""
  code_of interactive "$TOOL" compare "${arguments[@]}"
  say ""
  say "Код виходу: $CODE."
  verdict "Звіт про справжні бази зрозумілий і правильний" "Звіт збігається з тим, що ви знаєте про свої копії?"
  say ""
  printf '  Шлях нового файла. Він має не існувати (Enter — не зливати): '
  read -r output || true
  if [[ -z "$output" ]]; then
    record "$name" "$NOT_CHECKED"
    return 0
  fi
  output=$(native_path "$output")
  say ""
  code_of interactive "$TOOL" merge "${arguments[@]}" --output "$output"
  say ""
  say "Код виходу: $CODE."
  if [[ "$CODE" == 0 ]]; then
    record "$name" "$YES"
    step "Відкрийте новий файл у вікні KeePassXC і перевірте записи, які знаєте напевно."
    verdict "Новий файл із справжніх баз відкрився і все на місці" "Усе на місці?"
    note "Замінювати копії на дисках чи ні, вирішуєте ви. Тула цього не робить."
  else
    record "$name" "$NO (код $CODE)"
  fi
  pause "Натисніть Enter, щоб іти далі."
}

stage_flash_drive() {
  stage "Флешка FAT32 або exFAT (необов'язково)"
  local name="Запис результату на флешку без hard link"
  say "На цих системах файлів немає hard link, тому тула публікує файл інакше. Досі цей шлях лише"
  say "імітували. Крок записує на флешку синтетичний файл і потім видаляє його."
  say ""
  printf '  Директорія на флешці FAT32 або exFAT (Enter — пропустити): '
  local target=""
  read -r target || true
  if [[ -z "$target" || ! -d "$target" ]]; then
    record "$name" "$NOT_CHECKED"
    return 0
  fi
  target=$(native_path "$target")
  local output="$target/cleanpass-manual-check-$$.kdbx"
  code_of "$TOOL" merge "$SYNTHETIC_BASE" --output "$output" < <(printf '%s\n' "$SYNTHETIC_PASSWORD") >/dev/null 2>&1
  local left
  left=$(find "$target" -maxdepth 1 -name 'cleanpass-*.tmp' 2>/dev/null | wc -l | tr -d ' ')
  if [[ "$CODE" == 0 && -f "$output" && "$left" == 0 ]] && printf '%s\n' "$SYNTHETIC_PASSWORD" | kxc ls -q "$output" >/dev/null 2>&1; then
    say "Файл записано, KeePassXC його читає, тимчасових файлів немає."
    record "$name" "$YES"
  else
    warn "Щось не так: код виходу $CODE, тимчасових файлів $left."
    record "$name" "$NO (код $CODE)"
  fi
  rm -f "$output"
  pause "Натисніть Enter, щоб іти далі."
}

stage_summary() {
  stage "Підсумок"
  say "У цьому підсумку немає паролів, шляхів і назв записів. Його можна переслати."
  say ""
  local line
  for line in "${RESULTS[@]}"; do
    say "$line"
  done
  say ""
  note "Той самий скрипт треба запустити на кожній системі, де ви працюєте з базами."
  note "Файли для Linux ARM і macOS Intel ще ніде не запускались."
}

run_wizard() {

  TOTAL_STAGES=11

  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    printf 'Ручна перевірка cleanpass у вашому власному терміналі: справжній YubiKey і справжні бази.\n\n'
    printf 'Використання: %s [шлях до файла cleanpass]\n\n' "$0"
    printf 'Без шляху скрипт шукає файл cleanpass поруч із собою, у поточній директорії та в PATH.\n'
    printf 'Скрипт нікуди нічого не надсилає і не читає паролів.\n'
    return 0
  fi
  if [[ -z "$SYNTHETIC_ONLY" ]] && ! { [[ -t 0 ]] && [[ -t 1 ]]; }; then
    printf 'Цей скрипт працює лише у власному терміналі людини: він веде до майстер-паролів і справжніх баз. Запустіть його самі, не через модель і не через канал.\n' >&2
    return 2
  fi
  if ! find_tool "${1:-}"; then
    printf 'Файл cleanpass не знайдено. Вкажіть шлях до нього першим аргументом.\n' >&2
    return 2
  fi
  if ! find_keepassxc; then
    printf 'keepassxc-cli не знайдено. Встановіть KeePassXC або додайте keepassxc-cli до PATH.\n' >&2
    return 2
  fi
  WORK=$(native_path "$(mktemp -d "${TMPDIR:-/tmp}/cleanpass-check.XXXXXX")")
  trap 'rm -rf "$WORK"' EXIT
  SYNTHETIC_BASE="$WORK/synthetic.kdbx"
  if ! make_synthetic "$SYNTHETIC_BASE"; then
    printf 'keepassxc-cli не зміг створити синтетичну базу.\n' >&2
    return 2
  fi

  stage_start
  stage_mintty
  stage_hidden
  stage_ykman
  stage_yubikey_base
  stage_touch_windows
  stage_result_in_keepassxc
  stage_desktop
  stage_real_bases
  stage_flash_drive
  stage_summary
}

run_wizard "$@"; exit
