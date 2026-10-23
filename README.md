# msgfmt — часть 1

Библиотека C++17 и консольная программа для преобразования разметки сообщений
в чистый UTF-8-текст и стилевые диапазоны с позициями UTF-16. Поддерживаются
bold, italic, code, ссылки и экранирование текста.

Активное техническое задание: [docs/task1_cplusplus.md](docs/task1_cplusplus.md).
Согласованные решения, этапы и результаты проверок:
[docs/PLAN_task1.md](docs/PLAN_task1.md).
Исходный код и тесты находятся в [msgfmt/](msgfmt/).
Части 2 и 3 в текущую работу не входят.

## Сборка и запуск

Зависимости: компилятор с C++17 и стандартной библиотекой, CMake >= 3.20,
совместимый инструмент сборки. Сторонних библиотек и тестовых фреймворков нет.
Проверенные команды для GCC на Windows, из корня репозитория:

```text
cmake -S msgfmt -B build-gcc -G "MinGW Makefiles" -DCMAKE_CXX_COMPILER=g++ -DCMAKE_BUILD_TYPE=Release
cmake --build build-gcc
ctest --test-dir build-gcc --output-on-failure
```

Для Linux подготовлены команды из ТЗ:

```sh
cmake -S msgfmt -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
printf '%s' 'Привет, **мир**!' | ./build/msgfmt
printf '%s' '2 * 3' | ./build/msgfmt escape
```

Примеры байтового ввода на Windows, описание API, правила разметки и ограничения
классификации Unicode приведены в [msgfmt/README.md](msgfmt/README.md).

## Проверки и инструменты

Реализация и тесты проверены на Windows с GCC 14.2.0 (w64devkit), CMake 4.1.2
и генератором MinGW Makefiles. Release собирается с `-Wall -Wextra` без
предупреждений; все пять групп CTest проходят, включая 51 запуск CLI.
Linux здесь не проверялся. ASan/UBSan не запускались: установленный GCC
не содержит `libasan` и `libubsan`; CMake сообщает об этом при включении опции.

При разработке использовались Codex (ИИ-ассистент), GCC, CMake/CTest, Git
и PowerShell. Python использовался для вспомогательного переноса тестового
кода между файлами; сборка и тесты проекта его не требуют. Git-коммиты
создаёт пользователь после проверки изменений.
