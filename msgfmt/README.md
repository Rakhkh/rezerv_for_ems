# msgfmt

Библиотека C++17 и консольная программа для преобразования разметки в чистый
UTF-8-текст и стилевые диапазоны. Поддерживаются `**bold**`, `_italic_`,
`` `code` ``, `[ссылки](url)` и экранирование служебных символов.

Позиции и длины диапазонов измеряются в кодовых единицах UTF-16:
например, эмодзи 🚀 занимает две единицы. Полные правила
в [спецификации](../docs/SPECIFICATION.md#35-правила-разбора).

## Сборка и тесты

Нужны CMake ≥ 3.20 и компилятор с поддержкой C++17 (GCC или Clang).
Сторонних библиотек нет. Все команды ниже выполняются из корня репозитория.

```sh
cmake -S msgfmt -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

Для GCC на Windows нужны `g++` и `mingw32-make` в PATH:

```powershell
cmake -S msgfmt -B build-gcc -G "MinGW Makefiles" -DCMAKE_CXX_COMPILER=g++ -DCMAKE_BUILD_TYPE=Release
cmake --build build-gcc
ctest --test-dir build-gcc --output-on-failure
```

Для проверки с ASan/UBSan добавьте `-DMSGFMT_ENABLE_SANITIZERS=ON`
при конфигурации в отдельном каталоге сборки. Нужен компилятор
с соответствующими runtime-библиотеками.

## Командная строка

Программа читает stdin до конца. Без аргументов выводит JSON:

```sh
printf '%s' 'Привет, **мир**!' | ./build/msgfmt
```

```json
{"text":"Привет, мир!","ranges":[{"style":"bold","offset":8,"length":3,"url":""}]}
```

Команда `escape` добавляет обратный слеш перед каждым служебным символом:

```sh
printf '%s' '2 * 3' | ./build/msgfmt escape
# Результат: 2 \* 3
```

Ввод должен быть в UTF-8. На Windows файл можно передать без перекодирования
конвейером PowerShell: `cmd /d /c "build-gcc\msgfmt.exe < message.txt"`.
Перевод строки в конец результата не добавляется.

Коды завершения: **0** — успех, **2** — некорректный UTF-8
(`{"error":"invalid_utf8"}` в stdout), **1** — ошибка аргументов или ввода-вывода.

## Использование библиотеки

Добавьте каталог msgfmt в CMake своего проекта и свяжите приложение
с целью `msgfmt_lib` (замените путь и имя `my_app` на свои):

```cmake
add_subdirectory(path/to/repo/msgfmt msgfmt-build)
target_link_libraries(my_app PRIVATE msgfmt_lib)
```

```cpp
#include <msgfmt.hpp>
#include <iostream>
#include <variant>

int main() {
    auto result = msgfmt::parse("**hello**");
    if (const auto* message = std::get_if<msgfmt::ParsedMessage>(&result)) {
        std::cout << message->text << '\n'; // hello
        return 0;
    }
    return 2;
}
```

`ParsedMessage` содержит `text` и `ranges`; у диапазона есть `style`, `offset`,
`length` и `url`. Функция `msgfmt::escape` возвращает экранированную строку.
Обе функции возвращают `msgfmt::Error::invalid_utf8` вместо результата
при некорректном входе. Типы и сигнатуры — в [msgfmt.hpp](include/msgfmt.hpp).

## Особенности

Незавершённая разметка сохраняется как обычный текст. Различные стили можно
вкладывать друг в друга; внутри `code` разметка и экранирование не действуют.
Unicode не нормализуется.

Для границ курсива буквами считаются ASCII-латиница и буквы основного блока
кириллицы, цифрами — `0–9`. Остальные Unicode-символы сохраняются, но не
учитываются как буквы или цифры при распознавании `_`.
