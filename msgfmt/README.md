# msgfmt

Библиотека C++17 для преобразования разметки сообщений в UTF-8-текст и
стилевые диапазоны с позициями UTF-16. Спецификация:
[task1_cplusplus.md](../docs/task1_cplusplus.md). Утверждённые решения и этапы:
[PLAN_task1.md](../docs/PLAN_task1.md).

## Текущее состояние: этап 1

Подготовлены CMake-цели, публичные типы и объявления API, тестовый механизм
CTest и каркас CLI. UTF-8, escape, парсер и JSON ещё не реализованы.

`parse` и `escape` пока только объявлены: вызов этих функций ещё не линкуется.
Каркас CLI всегда возвращает код 1 с сообщением о незавершённой реализации
в stderr; stdout пуст. Он не проверяет UTF-8 и не выдаёт результаты разбора.

## Сборка GCC на Windows

Требуются CMake >= 3.20, GCC с C++17 и `mingw32-make` в PATH.
Из корня репозитория:

```text
cmake -S msgfmt -B build-gcc -G "MinGW Makefiles" -DCMAKE_CXX_COMPILER=g++ -DCMAKE_BUILD_TYPE=Release
cmake --build build-gcc
ctest --test-dir build-gcc --output-on-failure
```

Цели: статическая библиотека `msgfmt_lib`, программа `msgfmt`, тестовая
программа `msgfmt_tests`. Дополнительные зависимости не загружаются.
Для сборки без тестов добавьте `-DBUILD_TESTING=OFF` при конфигурации.

Тесты этапа 1 проверяют публичные типы/сигнатуры и явный отказ каркаса CLI.
Они работают в Release и не проверяют ещё не реализованное форматирование.
Для GCC/Clang включены `-Wall -Wextra`; расширения C++ отключены.

Проверено на Windows: CMake 4.1.2, GCC 14.2.0 (w64devkit), генератор
`MinGW Makefiles`, Release; 2 из 2 тестов прошли, предупреждений компилятора
нет. Ninja в этом окружении зависал на проверке ABI компилятора, поэтому
для воспроизводимой локальной сборки выбран `MinGW Makefiles`.

## Санитайзеры

```text
cmake -S msgfmt -B build-gcc-sanitize -G "MinGW Makefiles" -DCMAKE_CXX_COMPILER=g++ -DCMAKE_BUILD_TYPE=Debug -DMSGFMT_ENABLE_SANITIZERS=ON
cmake --build build-gcc-sanitize
ctest --test-dir build-gcc-sanitize --output-on-failure
```

Опция добавляет ASan/UBSan при компиляции и линковке. CMake проверяет наличие
поддержки и runtime-библиотек; если проверка не проходит, конфигурация
завершается ошибкой. Такой результат не считается запуском санитайзеров.

В установленном GCC 14.2.0 проверка линковки обнаружила отсутствие `libasan`
и `libubsan` (`cannot find -lasan`, `cannot find -lubsan`). Конфигурация
с опцией ON отклонена ожидаемым образом; ASan/UBSan здесь не запускались.

## Последующая проверка Linux

```text
cmake -S msgfmt -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

Используйте отдельный каталог сборки для каждой платформы и конфигурации.
Linux проверит пользователь после завершения проекта; Windows-сборка
не подтверждает проверку на Linux.
